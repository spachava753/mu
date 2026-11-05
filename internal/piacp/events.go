package piacp

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spachava753/acp-sdk/acp"
)

func (s *session) reportToolCall(id, name string, arguments map[string]any, status acp.ToolCallStatus) {
	s.mu.Lock()
	s.reported[id] = true
	s.mu.Unlock()
	update := acp.ToolCallSessionUpdate(acp.ToolCallId(id), toolTitle(name, arguments))
	kind := toolKind(name)
	update.Kind = &kind
	update.Status = &status
	if arguments != nil {
		update.RawInput = arguments
		if locations := toolLocations(s.cwd, arguments); locations != nil {
			update.Locations = &locations
		}
		if diffs := s.propose(id, name, arguments); diffs != nil {
			update.Content = diffs
		}
	}
	s.send(update)
}

// propose remembers what a file tool says it will change, to show until it's done.
func (s *session) propose(id, name string, arguments map[string]any) []acp.ToolCallContent {
	diffs := argumentDiffs(s.cwd, name, arguments)
	if diffs != nil {
		s.mu.Lock()
		s.proposed[id] = diffs
		s.mu.Unlock()
	}
	return diffs
}

// finishTool reports a tool's result. A file tool that worked shows what it changed
// rather than its message about it. One that failed shows why.
func (s *session) finishTool(id, name string, content []block, details json.RawMessage, failed bool, rawOutput any) {
	s.mu.Lock()
	known := s.reported[id]
	proposed := s.proposed[id]
	delete(s.proposed, id)
	if s.running == id {
		s.running = ""
	}
	s.mu.Unlock()
	if !known {
		s.reportToolCall(id, name, nil, acp.ToolCallStatusInProgress)
	}
	shown := toolContent(content)
	if !failed {
		if diffs := patchDiffs(s.cwd, details); diffs != nil {
			shown = diffs
		} else if proposed != nil {
			shown = proposed
		}
	}
	update := acp.ToolCallUpdateSessionUpdate(acp.ToolCallId(id))
	update.Status = toolStatus(failed)
	update.Content = shown
	if rawOutput != nil {
		update.RawOutput = rawOutput
	}
	s.send(update)
}

// handleEvent translates Pi's events, in order, on the process's pump goroutine.
// It must not wait for a command's response.
func (s *session) handleEvent(kind string, raw []byte) {
	switch kind {
	case "message_start":
		s.mu.Lock()
		s.stream++
		s.mu.Unlock()
	case "message_update":
		var event struct {
			Update struct {
				Type         string `json:"type"`
				ContentIndex int    `json:"contentIndex"`
				Delta        string `json:"delta"`
				ID           string `json:"id"`
				ToolName     string `json:"toolName"`
				ToolCall     *block `json:"toolCall"`
			} `json:"assistantMessageEvent"`
		}
		if json.Unmarshal(raw, &event) == nil {
			u := event.Update
			id := messageID(s.blockID(u.ContentIndex))
			switch u.Type {
			case "text_delta":
				update := acp.AgentMessageChunkSessionUpdate(textBlock(u.Delta))
				update.MessageID = id
				s.send(update)
			case "thinking_delta":
				update := acp.AgentThoughtChunkSessionUpdate(textBlock(u.Delta))
				update.MessageID = id
				s.send(update)
			case "toolcall_start":
				s.reportToolCall(u.ID, u.ToolName, nil, acp.ToolCallStatusPending)
			case "toolcall_end":
				if c := u.ToolCall; c != nil {
					s.updateToolInput(c.ID, c.Name, c.Arguments)
				}
			}
		}
	case "message_end":
		var event struct {
			Message message `json:"message"`
		}
		if json.Unmarshal(raw, &event) == nil && event.Message.Role == "assistant" {
			s.recordAssistant(event.Message)
		}
	case "tool_execution_start":
		var event struct {
			ToolCallID string         `json:"toolCallId"`
			ToolName   string         `json:"toolName"`
			Args       map[string]any `json:"args"`
		}
		if json.Unmarshal(raw, &event) == nil {
			s.mu.Lock()
			known := s.reported[event.ToolCallID]
			s.running = event.ToolCallID
			s.mu.Unlock()
			if !known {
				s.reportToolCall(event.ToolCallID, event.ToolName, event.Args, acp.ToolCallStatusInProgress)
				return
			}
			status := acp.ToolCallStatusInProgress
			update := acp.ToolCallUpdateSessionUpdate(acp.ToolCallId(event.ToolCallID))
			update.Status = &status
			s.send(update)
		}
	case "tool_execution_update":
		var event struct {
			ToolCallID    string `json:"toolCallId"`
			PartialResult struct {
				Content json.RawMessage `json:"content"`
			} `json:"partialResult"`
		}
		// Each partial result replaces the last, for bash at least.
		if json.Unmarshal(raw, &event) == nil {
			if content := toolContent(blocks(event.PartialResult.Content)); len(content) > 0 {
				update := acp.ToolCallUpdateSessionUpdate(acp.ToolCallId(event.ToolCallID))
				update.Content = content
				s.send(update)
			}
		}
	case "tool_execution_end":
		var event struct {
			ToolCallID string `json:"toolCallId"`
			ToolName   string `json:"toolName"`
			IsError    bool   `json:"isError"`
			Result     any    `json:"result"`
		}
		var result struct {
			Result struct {
				Content json.RawMessage `json:"content"`
				Details json.RawMessage `json:"details"`
			} `json:"result"`
		}
		if json.Unmarshal(raw, &event) == nil && json.Unmarshal(raw, &result) == nil {
			s.finishTool(event.ToolCallID, event.ToolName, blocks(result.Result.Content), result.Result.Details, event.IsError, event.Result)
		}
	case "auto_retry_start":
		var event struct {
			Attempt      int    `json:"attempt"`
			MaxAttempts  int    `json:"maxAttempts"`
			DelayMs      int64  `json:"delayMs"`
			ErrorMessage string `json:"errorMessage"`
		}
		if json.Unmarshal(raw, &event) == nil {
			wait := time.Duration(event.DelayMs) * time.Millisecond
			s.send(acp.AgentThoughtChunkSessionUpdate(textBlock(fmt.Sprintf("The model request failed (%s). Retrying in %s, attempt %d of %d.\n", event.ErrorMessage, wait, event.Attempt, event.MaxAttempts))))
		}
	case "session_info_changed":
		var event struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &event) == nil {
			s.mu.Lock()
			s.name = event.Name
			s.mu.Unlock()
			s.send(acp.SessionUpdate{SessionUpdate: acp.SessionUpdateTypeSessionInfoUpdate, Title: &event.Name})
		}
	case "thinking_level_changed":
		var event struct {
			Level string `json:"level"`
		}
		if json.Unmarshal(raw, &event) == nil {
			s.mu.Lock()
			s.thinking = event.Level
			loaded := s.models != nil
			options := s.optionsLocked()
			s.mu.Unlock()
			if loaded {
				s.send(acp.SessionUpdate{SessionUpdate: acp.SessionUpdateTypeConfigOptionUpdate, ConfigOptions: options})
			}
		}
	case "agent_settled":
		var event struct {
			Aborted bool `json:"aborted"`
		}
		_ = json.Unmarshal(raw, &event)
		s.mu.Lock()
		t := s.turn
		if t != nil {
			t.aborted = event.Aborted
		}
		s.mu.Unlock()
		if t != nil {
			t.settle.Do(func() { close(t.settled) })
		}
	case "extension_ui_request":
		var request uiRequest
		if json.Unmarshal(raw, &request) == nil {
			s.handleDialog(request)
		}
	case "extension_error":
		var event struct {
			ExtensionPath string `json:"extensionPath"`
			Error         string `json:"error"`
		}
		if json.Unmarshal(raw, &event) == nil {
			s.a.diag("Pi extension %s: %s", event.ExtensionPath, event.Error)
		}
	}
}

func (s *session) blockID(index int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("%s-%d-%d", s.id, s.stream, index)
}

func (s *session) updateToolInput(id, name string, arguments map[string]any) {
	update := acp.ToolCallUpdateSessionUpdate(acp.ToolCallId(id))
	title := toolTitle(name, arguments)
	update.Title = &title
	update.RawInput = arguments
	if locations := toolLocations(s.cwd, arguments); locations != nil {
		update.Locations = &locations
	}
	if diffs := s.propose(id, name, arguments); diffs != nil {
		update.Content = diffs
	}
	s.send(update)
}

func (s *session) recordAssistant(m message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.turn
	if t == nil {
		return
	}
	t.stop, t.failure = m.StopReason, m.ErrorMessage
	if u := m.Usage; u != nil {
		t.usage.InputTokens += u.Input
		t.usage.OutputTokens += u.Output
		t.usage.TotalTokens += u.TotalTokens
		add := func(field **uint64, value uint64) {
			if value == 0 {
				return
			}
			if *field == nil {
				*field = new(uint64)
			}
			**field += value
		}
		add(&t.usage.CachedReadTokens, u.CacheRead)
		add(&t.usage.CachedWriteTokens, u.CacheWrite)
		add(&t.usage.ThoughtTokens, u.Reasoning)
	}
}

// uiRequest is a request from an extension through ctx.ui.
type uiRequest struct {
	ID         string   `json:"id"`
	Method     string   `json:"method"`
	Title      string   `json:"title"`
	Message    string   `json:"message"`
	Options    []string `json:"options"`
	NotifyType string   `json:"notifyType"`
}

// handleDialog maps what an extension asks onto ACP's permission requests. Pi's
// tools don't ask, so this is the only source of them. Text input has no ACP
// equivalent and is declined, since an unanswered dialog would block Pi for good.
func (s *session) handleDialog(r uiRequest) {
	switch r.Method {
	case "confirm", "select":
		s.mu.Lock()
		s.dialogs[r.ID] = true
		running := s.running
		s.background.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.background.Done()
			s.askClient(r, running)
		}()
	case "input", "editor":
		_ = s.pi.write(map[string]any{"type": "extension_ui_response", "id": r.ID, "cancelled": true})
		s.send(acp.AgentThoughtChunkSessionUpdate(textBlock(fmt.Sprintf("A Pi extension asked for text (%s), which Mu can't show. It was told no answer was given.\n", r.Title))))
	case "notify":
		if r.NotifyType == "warning" || r.NotifyType == "error" {
			s.send(acp.AgentThoughtChunkSessionUpdate(textBlock(r.Message + "\n")))
		}
	}
	// Status, widgets, the terminal title and the editor have nowhere to go in Mu.
}

// askClient asks the client on its own goroutine, because the answer can take as
// long as the person does. A request has no tool call ID, so it's taken to be about
// the tool now running, which is how a tool_call handler is called.
func (s *session) askClient(r uiRequest, running string) {
	call := acp.ToolCallUpdate{ToolCallID: acp.ToolCallId(running)}
	text := strings.TrimSpace(r.Title + "\n" + r.Message)
	content := []acp.ToolCallContent{{Type: acp.ToolCallContentTypeContent, Content: textBlock(text)}}
	call.Content = &content
	if running == "" {
		call.ToolCallID = acp.ToolCallId("pi-dialog-" + r.ID)
		call.Title = &r.Title
	}
	options := []acp.PermissionOption{{OptionID: "yes", Name: "Allow", Kind: acp.PermissionOptionKindAllowOnce}, {OptionID: "no", Name: "Deny", Kind: acp.PermissionOptionKindRejectOnce}}
	if r.Method == "select" {
		options = options[:0]
		for _, choice := range r.Options {
			options = append(options, acp.PermissionOption{OptionID: acp.PermissionOptionId(choice), Name: choice, Kind: acp.PermissionOptionKindAllowOnce})
		}
	}
	response, err := s.a.conn.SessionRequestPermission(s.ctx, &acp.RequestPermissionRequest{SessionID: s.ID(), ToolCall: call, Options: options})
	s.mu.Lock()
	waiting := s.dialogs[r.ID]
	delete(s.dialogs, r.ID)
	s.mu.Unlock()
	if !waiting {
		return // Already answered by a cancellation.
	}
	answer := map[string]any{"type": "extension_ui_response", "id": r.ID, "cancelled": true}
	if err == nil && response.Outcome.Outcome == acp.RequestPermissionOutcomeTypeSelected {
		delete(answer, "cancelled")
		chosen := string(response.Outcome.OptionID)
		if r.Method == "confirm" {
			answer["confirmed"] = chosen == "yes"
		} else {
			answer["value"] = chosen
		}
	}
	_ = s.pi.write(answer)
}
