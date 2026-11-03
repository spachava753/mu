package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
)

func notify(session string, payload map[string]any) {
	send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": payload}})
}
func modes(current string) map[string]any {
	return map[string]any{"currentModeId": current, "availableModes": []any{map[string]any{"id": "ask", "name": "Ask"}, map[string]any{"id": "code", "name": "Code"}}}
}
func options(model string, enabled bool) []any {
	level := "low"
	if model == "deep" {
		level = "high"
	}
	models := []any{map[string]any{"value": "fast", "name": "Fast"}, map[string]any{"value": "deep", "name": "Deep"}, map[string]any{"value": "reject", "name": "Rejected model"}}
	if slices.Contains(os.Args, "long-menus") {
		for i := range 100 {
			models = append(models, map[string]any{"value": fmt.Sprintf("model-%02d", i), "name": fmt.Sprintf("Model %02d", i)})
		}
	}
	return []any{
		map[string]any{"id": "model", "name": "Model", "type": "select", "category": "model", "currentValue": model, "options": []any{
			map[string]any{"group": "local", "name": "Available models", "options": models},
		}},
		map[string]any{"id": "reasoning", "name": "Reasoning", "type": "select", "currentValue": level, "options": []any{map[string]any{"value": level, "name": "Reasoning: " + level}}},
		map[string]any{"id": "enabled", "name": "Optional behavior", "type": "boolean", "currentValue": enabled},
	}
}
func initialState(session string) {
	commands := []any{map[string]any{"name": "review", "description": "Review the current changes", "input": map[string]any{"hint": "What should be reviewed?"}}}
	if slices.Contains(os.Args, "long-menus") {
		for i := range 80 {
			commands = append(commands, map[string]any{"name": fmt.Sprintf("command%02d", i), "description": fmt.Sprintf("Run fixture action %02d", i)})
		}
	}
	notify(session, map[string]any{"sessionUpdate": "available_commands_update", "availableCommands": commands})
	notify(session, map[string]any{"sessionUpdate": "usage_update", "used": 12000, "size": 128000, "cost": map[string]any{"amount": 0.125, "currency": "USD"}, "_meta": map[string]any{"limits": map[string]any{"remaining": 42}}})
}
func sessionUpdates(session string) {
	update(session, "user_message_chunk", "up")
	update(session, "user_message_chunk", "dates")
	update(session, "agent_thought_chunk", "Checking the requested changes.")
	notify(session, map[string]any{"sessionUpdate": "plan", "entries": []any{map[string]any{"content": "Inspect configuration", "priority": "high", "status": "pending"}}})
	notify(session, map[string]any{"sessionUpdate": "plan", "entries": []any{map[string]any{"content": "Inspect configuration", "priority": "high", "status": "completed"}}})
	notify(session, map[string]any{"sessionUpdate": "plan_update", "plan": map[string]any{"type": "items", "planId": "discard", "entries": []any{map[string]any{"content": "Temporary plan", "priority": "low", "status": "pending"}}}})
	notify(session, map[string]any{"sessionUpdate": "plan_removed", "planId": "discard"})
	notify(session, map[string]any{"sessionUpdate": "plan_update", "plan": map[string]any{"type": "markdown", "planId": "summary", "content": "- [x] Inspect the configuration\n- [x] Verify the session options"}})
	notify(session, map[string]any{"sessionUpdate": "plan_update", "plan": map[string]any{"type": "file", "planId": "file", "uri": "file:///workspace/plan.md"}})
	notify(session, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "read-1", "title": "Read configuration", "kind": "read", "status": "in_progress", "rawInput": map[string]any{"path": "config.json"}, "locations": []any{map[string]any{"path": "/workspace/config.json", "line": 1}}})
	notify(session, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "read-1", "status": "completed", "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": "Configuration loaded."}}}, "rawOutput": map[string]any{"ok": true}})
	notify(session, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": "code"})
	notify(session, map[string]any{"sessionUpdate": "config_option_update", "configOptions": options("deep", true)})
	notify(session, map[string]any{"sessionUpdate": "session_info_update", "title": "Session controls and usage", "updatedAt": "2026-10-21T08:30:00Z", "_meta": map[string]any{"customStatus": "ready"}})
	update(session, "agent_message_chunk", "The session is ready.\n\n### Checked\n\n- Agent configuration is saved.\n- Session options reflect the agent’s state.\n- Context usage and cost are visible below.\n\n```go\nfmt.Println(\"ready\")\n```\n")
}
func reject(id json.RawMessage) {
	send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": "option rejected by fixture"}})
}
