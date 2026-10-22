package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/state"
)

func contentText(b acp.ContentBlock) string {
	switch {
	case b.Text != nil:
		return b.Text.Text
	case b.ResourceLink != nil:
		return b.ResourceLink.Name + " (" + b.ResourceLink.Uri + ")"
	case b.Image != nil:
		return "\n[Image: " + b.Image.MimeType + "; see content details.]\n"
	case b.Audio != nil:
		return "\n[Audio: " + b.Audio.MimeType + "; see content details.]\n"
	case b.Resource != nil:
		if b.Resource.Resource.TextResourceContents != nil {
			return b.Resource.Resource.TextResourceContents.Text
		}
		return "\n[Binary resource; see content details.]\n"
	}
	return ""
}

func toolText(content []acp.ToolCallContent, raw any) string {
	var b strings.Builder
	for _, c := range content {
		if c.Content != nil {
			b.WriteString(contentText(c.Content.Content))
			b.WriteByte('\n')
		}
		if c.Diff != nil {
			b.WriteString("File: " + c.Diff.Path + "\n")
			if c.Diff.OldText != nil {
				b.WriteString("Before:\n" + *c.Diff.OldText + "\n")
			}
			b.WriteString("After:\n" + c.Diff.NewText + "\n")
		}
		if c.Terminal != nil {
			b.WriteString("Terminal: " + c.Terminal.TerminalId + "\n")
		}
	}
	if b.Len() == 0 && raw != nil {
		b.WriteString(pretty(raw))
	}
	return b.String()
}

func pretty(value any) string { b, _ := json.MarshalIndent(value, "", "  "); return string(b) }
func storeMeta(s *state.Session, source string, meta map[string]any) {
	if len(meta) == 0 {
		return
	}
	if s.Metadata == nil {
		s.Metadata = map[string]json.RawMessage{}
	}
	b, err := json.Marshal(meta)
	if err == nil {
		s.Metadata[source] = b
	}
}

// A connection generation prevents late notifications from a closed process
// from mutating a reconnected thread, even if it resumes the same session ID.
func (a *App) receive(id string, r *runtime, epoch uint64, n acp.SessionNotification) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r.epoch != epoch {
		return
	}
	if r.initializing {
		r.initialUpdates = append(r.initialUpdates, n)
		return
	}
	a.updateLocked(id, r, n)
}

func (a *App) update(id string, r *runtime, n acp.SessionNotification) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.updateLocked(id, r, n)
}

func (a *App) updateLocked(id string, r *runtime, n acp.SessionNotification) {
	if r.session != string(n.SessionId) {
		return
	}
	t := a.threadLocked(id)
	if t == nil {
		return
	}
	u := n.Update
	storeMeta(&t.Session, "notification", n.Meta)
	// Preserve extension metadata without guessing provider-specific semantics.
	var envelope struct {
		Kind string         `json:"sessionUpdate"`
		Meta map[string]any `json:"_meta"`
	}
	if b, err := json.Marshal(u); err == nil {
		_ = json.Unmarshal(b, &envelope)
		storeMeta(&t.Session, envelope.Kind, envelope.Meta)
	}
	appendChunk := func(role string, block acp.ContentBlock) {
		if !r.acceptUpdates {
			return
		}
		text := contentText(block)
		if text == "" {
			return
		}
		i := len(t.Entries) - 1
		if i < 0 || t.Entries[i].Role != role || block.Text == nil {
			t.Entries = append(t.Entries, state.Entry{ID: state.ID(), Role: role})
			i = len(t.Entries) - 1
		}
		t.Entries[i].Text += text
		if block.Text == nil {
			t.Entries[i].Details = pretty(block)
		}
	}
	switch {
	case u.UserMessageChunk != nil:
		if !r.acceptUpdates {
			break
		}
		block := u.UserMessageChunk.Content
		if r.job != nil && block.Text != nil && r.echoTarget != "" {
			r.echoText += block.Text.Text
			if strings.HasPrefix(r.echoTarget, r.echoText) {
				break
			}
			// An agent-authored/revised user message is not an echo of our prompt.
			if r.echoEntry == "" {
				r.echoEntry = state.ID()
				t.Entries = append(t.Entries, state.Entry{ID: r.echoEntry, Role: "user", Text: r.echoText})
			} else {
				for i := range t.Entries {
					if t.Entries[i].ID == r.echoEntry {
						t.Entries[i].Text = r.echoText
					}
				}
			}
		} else {
			appendChunk("user", block)
		}
	case u.AgentMessageChunk != nil:
		appendChunk("assistant", u.AgentMessageChunk.Content)
	case u.AgentThoughtChunk != nil:
		appendChunk("thought", u.AgentThoughtChunk.Content)
	case u.ToolCall != nil:
		if !r.acceptUpdates {
			break
		}
		v := u.ToolCall
		e := toolEntry(t, string(v.ToolCallId))
		e.Title, e.Status, e.Text = v.Title, string(v.Status), toolText(v.Content, v.RawOutput)
		e.Details = mergeToolDetails(e.Details, v)
		applyTerminal(e, v.Content, v.Meta, r)
	case u.ToolCallUpdate != nil:
		if !r.acceptUpdates {
			break
		}
		v := u.ToolCallUpdate
		e := toolEntry(t, string(v.ToolCallId))
		if v.Title != nil {
			e.Title = *v.Title
		}
		if v.Status != nil {
			e.Status = string(*v.Status)
		}
		if v.Content != nil || v.RawOutput != nil {
			e.Text = toolText(v.Content, v.RawOutput)
		}
		e.Details = mergeToolDetails(e.Details, v)
		applyTerminal(e, v.Content, v.Meta, r)
	case u.Plan != nil:
		setPlan(t, "legacy", planText(u.Plan.Entries), pretty(u.Plan))
	case u.PlanUpdate != nil:
		p := u.PlanUpdate.Plan
		switch {
		case p.Items != nil:
			setPlan(t, string(p.Items.Id), planText(p.Items.Entries), pretty(p))
		case p.Markdown != nil:
			setPlan(t, string(p.Markdown.Id), p.Markdown.Content, pretty(p))
		case p.File != nil:
			setPlan(t, string(p.File.Id), "Plan file: "+p.File.Uri, pretty(p))
		}
	case u.PlanRemoved != nil:
		t.Entries = slices.DeleteFunc(t.Entries, func(e state.Entry) bool { return e.Role == "plan" && e.PlanID == string(u.PlanRemoved.Id) })
	case u.AvailableCommandsUpdate != nil:
		t.Session.Commands = u.AvailableCommandsUpdate.AvailableCommands
	case u.ConfigOptionUpdate != nil:
		t.Session.Options = u.ConfigOptionUpdate.ConfigOptions
	case u.CurrentModeUpdate != nil:
		if t.Session.Modes == nil {
			t.Session.Modes = &acp.SessionModeState{}
		}
		t.Session.Modes.CurrentModeId = u.CurrentModeUpdate.CurrentModeId
	case u.SessionInfoUpdate != nil:
		v := u.SessionInfoUpdate
		if v.Title != nil && strings.TrimSpace(*v.Title) != "" {
			t.Title = *v.Title
		}
		if v.UpdatedAt != nil {
			t.Session.AgentUpdatedAt = *v.UpdatedAt
		}
	case u.UsageUpdate != nil:
		t.Session.Usage = u.UsageUpdate
	}
	a.dirty = true
	a.notifyLocked()
}

func toolEntry(t *thread, id string) *state.Entry {
	for i := len(t.Entries) - 1; i >= 0; i-- {
		if t.Entries[i].Role == "tool" && t.Entries[i].ToolID == id {
			return &t.Entries[i]
		}
	}
	t.Entries = append(t.Entries, state.Entry{ID: state.ID(), Role: "tool", ToolID: id, Title: "Tool"})
	return &t.Entries[len(t.Entries)-1]
}

func mergeToolDetails(old string, update any) string {
	data := map[string]any{}
	_ = json.Unmarshal([]byte(old), &data)
	var next map[string]any
	b, _ := json.Marshal(update)
	_ = json.Unmarshal(b, &next)
	for k, v := range next {
		if k != "sessionUpdate" {
			data[k] = v
		}
	}
	return pretty(data)
}

func planText(entries []acp.PlanEntry) string {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s: %s (%s priority)\n", e.Status, e.Content, e.Priority)
	}
	return b.String()
}

func setPlan(t *thread, id, text, details string) {
	for i := range t.Entries {
		if t.Entries[i].Role == "plan" && t.Entries[i].PlanID == id {
			t.Entries[i].Text, t.Entries[i].Details = text, details
			return
		}
	}
	t.Entries = append(t.Entries, state.Entry{ID: state.ID(), Role: "plan", PlanID: id, Title: "Plan", Text: text, Details: details})
}

func applyTerminal(e *state.Entry, content []acp.ToolCallContent, meta map[string]any, r *runtime) {
	for _, c := range content {
		if c.Terminal != nil {
			e.TerminalID = c.Terminal.TerminalId
		}
	}
	// This terminal-output extension is also used for agent-owned terminals.
	if output, ok := meta["terminal_output"].(map[string]any); ok {
		id, _ := output["terminal_id"].(string)
		data, _ := output["data"].(string)
		if id != "" && id == e.TerminalID {
			r.outputs[id] = tailText(r.outputs[id] + data)
		}
	}
	if output, ok := r.outputs[e.TerminalID]; ok {
		e.Text = output
	}
}
func tailText(text string) string {
	const limit = 1024 * 1024
	if len(text) <= limit {
		return text
	}
	return "[Earlier output truncated]\n" + strings.ToValidUTF8(text[len(text)-limit:], "�")
}
func (a *App) terminalOutput(id string, r *runtime, epoch uint64, session acp.SessionId, terminalID, output string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !r.acceptUpdates || r.epoch != epoch || r.session != string(session) {
		return
	}
	r.outputs[terminalID] = output
	if t := a.threadLocked(id); t != nil {
		for i := range t.Entries {
			if t.Entries[i].TerminalID == terminalID {
				t.Entries[i].Text = output
			}
		}
	}
	a.dirty = true
	a.notifyLocked()
}
