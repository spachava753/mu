package app

import (
	"encoding/json"
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
		return "\n[Image received; this text-only client does not render attachments.]\n"
	case b.Audio != nil:
		return "\n[Audio received; this text-only client does not play attachments.]\n"
	case b.Resource != nil:
		if b.Resource.Resource.TextResourceContents != nil {
			return b.Resource.Resource.TextResourceContents.Text
		}
		return "\n[Binary resource received.]\n"
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
		data, _ := json.MarshalIndent(raw, "", "  ")
		b.Write(data)
	}
	return b.String()
}

func (a *App) update(id string, r *runtime, n acp.SessionNotification) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !r.acceptUpdates || r.session != string(n.SessionId) {
		return
	}
	t := a.threadLocked(id)
	if t == nil {
		return
	}
	u := n.Update
	appendChunk := func(role, text string) {
		if text == "" {
			return
		}
		i := len(t.Entries) - 1
		if i >= 0 && t.Entries[i].Role == role {
			t.Entries[i].Text += text
		} else {
			t.Entries = append(t.Entries, state.Entry{ID: state.ID(), Role: role, Text: text})
		}
	}
	switch {
	case u.AgentMessageChunk != nil:
		appendChunk("assistant", contentText(u.AgentMessageChunk.Content))
	case u.AgentThoughtChunk != nil:
		appendChunk("thought", contentText(u.AgentThoughtChunk.Content))
	case u.ToolCall != nil:
		v := u.ToolCall
		t.Entries = append(t.Entries, state.Entry{ID: state.ID(), Role: "tool", ToolID: string(v.ToolCallId), Title: v.Title, Status: string(v.Status), Text: toolText(v.Content, v.RawOutput)})
		applyTerminal(&t.Entries[len(t.Entries)-1], v.Content, v.Meta, r)
	case u.ToolCallUpdate != nil:
		v := u.ToolCallUpdate
		i := len(t.Entries) - 1
		for ; i >= 0; i-- {
			if t.Entries[i].Role == "tool" && t.Entries[i].ToolID == string(v.ToolCallId) {
				break
			}
		}
		if i < 0 {
			t.Entries = append(t.Entries, state.Entry{ID: state.ID(), Role: "tool", ToolID: string(v.ToolCallId), Title: "Tool"})
			i = len(t.Entries) - 1
		}
		e := &t.Entries[i]
		if v.Title != nil {
			e.Title = *v.Title
		}
		if v.Status != nil {
			e.Status = string(*v.Status)
		}
		if v.Content != nil || v.RawOutput != nil {
			e.Text = toolText(v.Content, v.RawOutput)
		}
		applyTerminal(e, v.Content, v.Meta, r)
	case u.Plan != nil:
		var b strings.Builder
		for _, e := range u.Plan.Entries {
			b.WriteString(string(e.Status) + ": " + e.Content + "\n")
		}
		appendChunk("plan", b.String())
	default:
		return
	}
	a.dirty = true
	a.notifyLocked()
}

func applyTerminal(e *state.Entry, content []acp.ToolCallContent, meta map[string]any, r *runtime) {
	for _, c := range content {
		if c.Terminal != nil {
			e.TerminalID = c.Terminal.TerminalId
		}
	}
	// Pi's ACP adapter uses the terminal_output extension for agent-owned terminals.
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
	text = text[len(text)-limit:]
	return "[Earlier output truncated]\n" + strings.ToValidUTF8(text, "�")
}

func (a *App) terminalOutput(id string, r *runtime, session acp.SessionId, terminalID, output string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !r.acceptUpdates || r.session != string(session) {
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
