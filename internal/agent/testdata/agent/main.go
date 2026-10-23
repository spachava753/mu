// A deterministic, separate-process ACP agent used by integration tests and manual QA.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
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
				Plan    *struct{} `json:"planCapabilities"`
				Session struct {
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
				continue
			}
			if slices.Contains(os.Args, "partial-load-fail") {
				update(saved.ID, "agent_message_chunk", "partial replay must not appear")
				reject(m.ID)
				continue
			}
			replay(saved)
			reply(m.ID, setup(opened(saved)))
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
		default:
			if len(m.ID) > 0 && !strings.HasPrefix(m.Method, "_") {
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "not implemented"}})
			}
		}
	}
}
