// A deterministic, separate-process ACP agent used by integration tests and manual QA.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

var mu sync.Mutex

func send(v any) {
	mu.Lock()
	defer mu.Unlock()
	recordNotification(v)
	_ = json.NewEncoder(os.Stdout).Encode(v)
}
func reply(id json.RawMessage, result any) {
	send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
func update(session, kind, text string) {
	send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": kind, "content": map[string]any{"type": "text", "text": text}}}})
}
func finish(s *openSession, reason string) {
	s.recording = false
	if s.pending != nil {
		reply(s.pending, map[string]any{"stopReason": reason})
	}
	s.pending, s.waiting = nil, ""
}

var (
	requests int
	answered = map[string]int{}
	loading  = map[string]json.RawMessage{}
)

func toolCall(session string, call map[string]any) {
	call["sessionUpdate"] = "tool_call"
	send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": call}})
}

func askPermission(id, session string, call map[string]any) {
	send(map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/request_permission", "params": map[string]any{"sessionId": session, "toolCall": call, "options": []any{map[string]any{"optionId": "yes", "name": "Allow once", "kind": "allow_once"}, map[string]any{"optionId": "always", "name": "Always allow edits", "kind": "allow_always"}, map[string]any{"optionId": "no", "name": "Reject", "kind": "reject_once"}}}})
}

func main() {
	noLoad := slices.Contains(os.Args, "no-load")
	withOptions := slices.Contains(os.Args, "session-ui")
	legacy := slices.Contains(os.Args, "legacy-options")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	opened := func(saved *savedSession) *openSession {
		s := &openSession{savedSession: saved, model: "fast", mode: "ask"}
		open[saved.ID] = s
		return s
	}
	setup := func(s *openSession) map[string]any {
		result := map[string]any{}
		if withOptions {
			initialState(s.ID)
			result["configOptions"] = options(s.model, s.enabled)
		}
		if withOptions || legacy {
			result["modes"] = modes(s.mode)
		}
		return result
	}
	for scanner.Scan() {
		var m message
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			continue
		}
		logCall(m)
		var p struct {
			Cursor                string          `json:"cursor"`
			AdditionalDirectories []string        `json:"additionalDirectories"`
			SessionID             string          `json:"sessionId"`
			Cwd                   string          `json:"cwd"`
			ConfigID              string          `json:"configId"`
			Value                 json.RawMessage `json:"value"`
			ModeID                string          `json:"modeId"`
			ClientCapabilities    struct {
				Fs struct {
					Read  bool `json:"readTextFile"`
					Write bool `json:"writeTextFile"`
				} `json:"fs"`
				Terminal bool      `json:"terminal"`
				Plan     *struct{} `json:"planCapabilities"`
				Session  struct {
					ConfigOptions struct {
						Boolean *struct{} `json:"boolean"`
					} `json:"configOptions"`
				} `json:"session"`
			} `json:"clientCapabilities"`
			Prompt []struct {
				Text string `json:"text"`
			} `json:"prompt"`
		}
		_ = json.Unmarshal(m.Params, &p)
		s := open[p.SessionID]
		switch m.Method {
		case "initialize":
			if slices.Contains(os.Args, "slow-init") {
				time.Sleep(500 * time.Millisecond)
			}
			// Mu offers no client file system or terminal methods.
			if c := p.ClientCapabilities; c.Fs.Read || c.Fs.Write || c.Terminal {
				reject(m.ID)
				continue
			}
			if withOptions && (p.ClientCapabilities.Plan == nil || p.ClientCapabilities.Session.ConfigOptions.Boolean == nil) {
				reject(m.ID)
				continue
			}
			caps := map[string]any{}
			for _, name := range []string{"list", "close", "fork", "additionalDirectories"} {
				if !slices.Contains(os.Args, "no-"+name) {
					caps[name] = map[string]any{}
				}
			}
			reply(m.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": !noLoad, "sessionCapabilities": caps}, "agentInfo": map[string]any{"name": "test-agent", "version": "1"}})
		case "session/new":
			saved := newSaved(p.Cwd)
			saved.AdditionalDirectories = p.AdditionalDirectories
			save(saved)
			s := opened(saved)
			result := setup(s)
			result["sessionId"] = s.ID
			reply(m.ID, result)
		case "session/list":
			if slices.Contains(os.Args, "wait-list") {
				continue
			}
			listSaved(m.ID, p.Cursor)
		case "session/close":
			if slices.Contains(os.Args, "close-missing") {
				// The agent no longer has the session: ACP's -32002.
				delete(open, p.SessionID)
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32002, "message": "Resource not found"}})
				continue
			}
			if s == nil {
				notOpen(m.ID, p.SessionID)
				continue
			}
			delete(open, p.SessionID)
			if slices.Contains(os.Args, "slow-close") {
				time.Sleep(150 * time.Millisecond)
				update(p.SessionID, "agent_message_chunk", "late close notification")
			}
			if slices.Contains(os.Args, "wait-close") {
				continue
			}
			if slices.Contains(os.Args, "close-error") {
				reject(m.ID)
				continue
			}
			finish(s, "cancelled")
			reply(m.ID, map[string]any{})
		case "session/fork":
			if slices.Contains(os.Args, "wait-fork") {
				continue
			}
			if s == nil {
				notOpen(m.ID, p.SessionID)
				continue
			}
			source, err := load(p.SessionID)
			if err != nil {
				missing(m.ID)
				continue
			}
			fork := newSaved(p.Cwd)
			fork.Updates = source.Updates
			fork.AdditionalDirectories = p.AdditionalDirectories
			save(fork)
			opened(fork)
			reply(m.ID, map[string]any{"sessionId": fork.ID})
		case "session/load":
			saved, err := load(p.SessionID)
			if err != nil {
				missing(m.ID)
				continue
			}
			if p.Cwd != saved.Cwd || !slices.Equal(p.AdditionalDirectories, saved.AdditionalDirectories) {
				reject(m.ID)
				continue
			}
			if slices.Contains(os.Args, "wait-load") {
				opened(saved) // Loaded, but the reply never comes.
				continue
			}
			if slices.Contains(os.Args, "partial-load-fail") {
				update(saved.ID, "agent_message_chunk", "partial replay must not appear")
				reject(m.ID)
				continue
			}
			replay(saved)
			s := opened(saved)
			if slices.Contains(os.Args, "load-permission") {
				// Ask about a tool call before the load completes.
				toolCall(saved.ID, map[string]any{"toolCallId": "tool-load", "title": "Resume edit", "kind": "edit"})
				askPermission("load:"+saved.ID, saved.ID, map[string]any{"toolCallId": "tool-load"})
				loading[saved.ID] = m.ID
				continue
			}
			reply(m.ID, setup(s))
		case "session/set_config_option":
			if slices.Contains(os.Args, "wait-config") {
				continue
			}
			if s == nil {
				notOpen(m.ID, p.SessionID)
				continue
			}
			if p.ConfigID == "model" {
				var value string
				_ = json.Unmarshal(p.Value, &value)
				if value == "reject" {
					reject(m.ID)
					continue
				}
				s.model = value
			} else if p.ConfigID == "enabled" {
				_ = json.Unmarshal(p.Value, &s.enabled)
			}
			reply(m.ID, map[string]any{"configOptions": options(s.model, s.enabled)})
			notify(s.ID, map[string]any{"sessionUpdate": "usage_update", "used": 16000, "size": 128000, "cost": map[string]any{"amount": 0.250, "currency": "USD"}})
		case "session/set_mode":
			if s == nil {
				notOpen(m.ID, p.SessionID)
				continue
			}
			s.mode = p.ModeID
			if slices.Contains(os.Args, "mode-fallback") {
				s.mode = "ask" // The agent keeps another mode and says so before replying.
			}
			notify(s.ID, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": s.mode})
			reply(m.ID, map[string]any{})
		case "session/prompt":
			text := ""
			for _, b := range p.Prompt {
				text += b.Text
			}
			if s == nil {
				missing(m.ID)
				continue
			}
			savePrompt(s.savedSession, text)
			s.recording, s.pending = true, m.ID
			session := s.ID
			switch text {
			case "updates":
				sessionUpdates(session)
				s.recording, s.pending = false, nil
				reply(m.ID, map[string]any{"stopReason": "end_turn", "usage": map[string]any{"totalTokens": 30, "inputTokens": 10, "outputTokens": 20, "thoughtTokens": 3, "cachedReadTokens": 5, "cachedWriteTokens": 2}})
			case "exit":
				fmt.Fprintln(os.Stderr, "test agent disconnected")
				os.Exit(7)
			case "empty":
				finish(s, "end_turn")
			case "wait", "hang":
				s.waiting = text
				update(session, "agent_message_chunk", "Waiting")
			case "exit-slow":
				// The transport ends before the process exits.
				_ = os.Stdout.Close()
				time.Sleep(500 * time.Millisecond)
				logCall(message{Method: "exited"})
				os.Exit(0)
			case "prompt-error":
				s.recording, s.pending = false, nil
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32603, "message": "provider overloaded"}})
			case "bad-update":
				// An undecodable notification, then an error.
				send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": 5}})
				s.recording, s.pending = false, nil
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32603, "message": "bad update sent"}})
			case "refuse":
				finish(s, "refusal")
			case "max-tokens":
				update(session, "agent_message_chunk", "Partial")
				finish(s, "max_tokens")
			case "tool-wait":
				toolCall(session, map[string]any{"toolCallId": "tool-done", "title": "Read", "kind": "read", "status": "completed"})
				toolCall(session, map[string]any{"toolCallId": "tool-run", "title": "Run", "kind": "execute", "status": "in_progress"})
				s.waiting = "wait"
				update(session, "agent_message_chunk", "Waiting")
			case "permission-ref":
				// The request names only a tool call reported earlier.
				line := 3
				toolCall(session, map[string]any{"toolCallId": "tool-ref", "title": "Edit notes", "kind": "edit", "status": "pending", "locations": []any{map[string]any{"path": "/workspace/notes.txt", "line": line}}, "content": []any{map[string]any{"type": "diff", "path": "/workspace/notes.txt", "oldText": "old", "newText": "new"}}})
				askPermission("permission:"+session, session, map[string]any{"toolCallId": "tool-ref"})
			case "permissions-two":
				toolCall(session, map[string]any{"toolCallId": "tool-a", "title": "First", "kind": "execute"})
				toolCall(session, map[string]any{"toolCallId": "tool-b", "title": "Second", "kind": "read"})
				askPermission("two:"+session+":1", session, map[string]any{"toolCallId": "tool-a"})
				time.Sleep(100 * time.Millisecond)
				askPermission("two:"+session+":2", session, map[string]any{"toolCallId": "tool-b"})
			case "permission-idle":
				// Agents may ask for permission outside a prompt turn.
				finish(s, "end_turn")
				requests++
				askPermission(fmt.Sprintf("idle:%s:%d", session, requests), session, map[string]any{"toolCallId": "tool-idle", "title": "Background task", "kind": "execute"})
			case "permission":
				send(map[string]any{"jsonrpc": "2.0", "id": "permission:" + session, "method": "session/request_permission", "params": map[string]any{"sessionId": session, "toolCall": map[string]any{"toolCallId": "tool-1", "title": "Run test command", "rawInput": map[string]any{"command": "printf test"}}, "options": []any{map[string]any{"optionId": "yes", "name": "Allow once", "kind": "allow_once"}, map[string]any{"optionId": "no", "name": "Reject", "kind": "reject_once"}}}})
			default:
				if withOptions {
					update(session, "user_message_chunk", text)
				}
				update(session, "agent_message_chunk", "Echo: ")
				update(session, "agent_message_chunk", text)
				finish(s, "end_turn")
			}
		case "session/cancel":
			if slices.Contains(os.Args, "slow-cancel") {
				time.Sleep(3 * time.Second) // Like finishing a tool call first.
			}
			if s != nil && s.pending != nil && s.waiting != "hang" {
				finish(s, "cancelled")
			}
		case "":
			var id string
			_ = json.Unmarshal(m.ID, &id)
			if session, ok := strings.CutPrefix(id, "permission:"); ok {
				if s := open[session]; s != nil && s.pending != nil {
					update(session, "agent_message_chunk", "Permission: "+string(m.Result))
					finish(s, "end_turn")
				}
			}
			if rest, ok := strings.CutPrefix(id, "two:"); ok {
				session, n, _ := strings.Cut(rest, ":")
				if s := open[session]; s != nil && s.pending != nil {
					update(session, "agent_message_chunk", "Answer "+n+": "+string(m.Result)+"\n")
					answered[session]++
					if answered[session] == 2 {
						delete(answered, session)
						finish(s, "end_turn")
					}
				}
			}
			if session, ok := strings.CutPrefix(id, "load:"); ok {
				if load, s := loading[session], open[session]; load != nil && s != nil {
					delete(loading, session)
					reply(load, setup(s))
				}
			}
			if rest, ok := strings.CutPrefix(id, "idle:"); ok {
				session, _, _ := strings.Cut(rest, ":")
				if open[session] != nil {
					update(session, "agent_message_chunk", "Idle permission: "+string(m.Result))
				}
			}
		default:
			if len(m.ID) > 0 && !strings.HasPrefix(m.Method, "_") {
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "not implemented"}})
			}
		}
	}
	// Input closed: the client is shutting the agent down.
	if slices.Contains(os.Args, "slow-exit") {
		time.Sleep(300 * time.Millisecond) // Like persisting session state.
		logCall(message{Method: "exited"})
	}
	if slices.Contains(os.Args, "ignore-eof") {
		terminated := make(chan os.Signal, 1)
		signal.Notify(terminated, syscall.SIGTERM)
		<-terminated
		logCall(message{Method: "terminated"})
	}
}
