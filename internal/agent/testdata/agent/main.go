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
)

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

var mu sync.Mutex

func send(v any) { mu.Lock(); defer mu.Unlock(); _ = json.NewEncoder(os.Stdout).Encode(v) }
func reply(id json.RawMessage, result any) {
	send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}
func update(session, kind, text string) {
	send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": map[string]any{"sessionUpdate": kind, "content": map[string]any{"type": "text", "text": text}}}})
}
func finish(id json.RawMessage, reason string) { reply(id, map[string]any{"stopReason": reason}) }

func main() {
	noLoad := slices.Contains(os.Args, "no-load")
	withOptions := slices.Contains(os.Args, "session-ui")
	legacy := slices.Contains(os.Args, "legacy-options")
	model, enabled, mode := "fast", false, "ask"
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	var pending json.RawMessage
	var session, waiting string
	for scanner.Scan() {
		var m message
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			continue
		}
		var p struct {
			SessionID          string          `json:"sessionId"`
			Cwd                string          `json:"cwd"`
			ConfigID           string          `json:"configId"`
			Value              json.RawMessage `json:"value"`
			ModeID             string          `json:"modeId"`
			ClientCapabilities struct {
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
		switch m.Method {
		case "initialize":
			if withOptions && (p.ClientCapabilities.Plan == nil || p.ClientCapabilities.Session.ConfigOptions.Boolean == nil) {
				reject(m.ID)
				continue
			}
			reply(m.ID, map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{"loadSession": !noLoad}, "agentInfo": map[string]any{"name": "test-agent", "version": "1"}})
		case "session/new":
			session = fmt.Sprintf("session-%d", os.Getpid())
			result := map[string]any{"sessionId": session}
			if withOptions {
				initialState(session)
				result["configOptions"] = options(model, enabled)
			}
			if withOptions || legacy {
				result["modes"] = modes(mode)
			}
			reply(m.ID, result)
		case "session/load":
			session = p.SessionID
			update(session, "user_message_chunk", "replayed user")
			update(session, "agent_message_chunk", "replayed answer")
			result := map[string]any{}
			if withOptions {
				initialState(session)
				result["configOptions"] = options(model, enabled)
			}
			if withOptions || legacy {
				result["modes"] = modes(mode)
			}
			reply(m.ID, result)
		case "session/set_config_option":
			if slices.Contains(os.Args, "wait-config") {
				continue
			}
			if p.ConfigID == "model" {
				var value string
				_ = json.Unmarshal(p.Value, &value)
				if value == "reject" {
					reject(m.ID)
					continue
				}
				model = value
			} else if p.ConfigID == "enabled" {
				_ = json.Unmarshal(p.Value, &enabled)
			}
			reply(m.ID, map[string]any{"configOptions": options(model, enabled)})
			notify(session, map[string]any{"sessionUpdate": "usage_update", "used": 16000, "size": 128000, "cost": map[string]any{"amount": 0.250, "currency": "EUR"}})
		case "session/set_mode":
			mode = p.ModeID
			notify(session, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": mode})
			reply(m.ID, map[string]any{})
		case "session/prompt":
			text := ""
			for _, b := range p.Prompt {
				text += b.Text
			}
			session = p.SessionID
			switch text {
			case "updates":
				sessionUpdates(session)
				reply(m.ID, map[string]any{"stopReason": "end_turn", "usage": map[string]any{"totalTokens": 30, "inputTokens": 10, "outputTokens": 20, "thoughtTokens": 3, "cachedReadTokens": 5, "cachedWriteTokens": 2}})
			case "exit":
				fmt.Fprintln(os.Stderr, "test agent disconnected")
				os.Exit(7)
			case "empty":
				finish(m.ID, "end_turn")
			case "wait", "hang":
				pending = m.ID
				waiting = text
				update(session, "agent_message_chunk", "Waiting")
			case "permission":
				pending = m.ID
				send(map[string]any{"jsonrpc": "2.0", "id": "permission-1", "method": "session/request_permission", "params": map[string]any{"sessionId": session, "toolCall": map[string]any{"toolCallId": "tool-1", "title": "Run test command", "rawInput": map[string]any{"command": "printf test"}}, "options": []any{map[string]any{"optionId": "yes", "name": "Allow once", "kind": "allow_once"}, map[string]any{"optionId": "no", "name": "Reject", "kind": "reject_once"}}}})
			default:
				if withOptions {
					update(session, "user_message_chunk", text)
				}
				update(session, "agent_message_chunk", "Echo: ")
				update(session, "agent_message_chunk", text)
				finish(m.ID, "end_turn")
			}
		case "session/cancel":
			if pending != nil && waiting != "hang" {
				finish(pending, "cancelled")
				pending = nil
				waiting = ""
			}
		case "":
			if string(m.ID) == `"permission-1"` && pending != nil {
				update(session, "agent_message_chunk", "Permission: "+string(m.Result))
				finish(pending, "end_turn")
				pending = nil
			}
		default:
			if len(m.ID) > 0 && !strings.HasPrefix(m.Method, "_") {
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "not implemented"}})
			}
		}
	}
}
