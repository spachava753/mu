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

func toolText(content []acp.ToolCallContent, raw any, outputs map[string]string) string {
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
			if output, ok := outputs[c.Terminal.TerminalId]; ok {
				b.WriteString(output)
			} else {
				b.WriteString("Terminal: " + c.Terminal.TerminalId + "\n")
			}
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
func (a *App) receiveLocked(id string, r *runtime, epoch uint64, n acp.SessionNotification) {
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
	switch {
	case u.UserMessageChunk != nil:
		c := u.UserMessageChunk
		if r.acceptUpdates && !echoLocked(t, r, messageID(c.MessageId), c.Content) {
			appendChunk(t, "user", messageID(c.MessageId), c.Content)
		}
	case u.AgentMessageChunk != nil:
		if r.acceptUpdates {
			appendChunk(t, "assistant", messageID(u.AgentMessageChunk.MessageId), u.AgentMessageChunk.Content)
		}
	case u.AgentThoughtChunk != nil:
		if r.acceptUpdates {
			appendChunk(t, "thought", messageID(u.AgentThoughtChunk.MessageId), u.AgentThoughtChunk.Content)
		}
	case u.ToolCall != nil:
		if !r.acceptUpdates {
			break
		}
		v := u.ToolCall
		call := &toolCall{Title: v.Title, Kind: v.Kind, Status: v.Status, Content: v.Content, Locations: v.Locations, RawInput: v.RawInput, RawOutput: v.RawOutput, Meta: v.Meta}
		r.tools[string(v.ToolCallId)] = call
		renderTool(t, r, string(v.ToolCallId), call)
	case u.ToolCallUpdate != nil:
		if !r.acceptUpdates {
			break
		}
		v := u.ToolCallUpdate
		call := r.tools[string(v.ToolCallId)]
		if call == nil {
			call = &toolCall{}
			r.tools[string(v.ToolCallId)] = call
		}
		call.patch(v)
		renderTool(t, r, string(v.ToolCallId), call)
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

func messageID(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}

// appendChunk groups chunks by the agent's message ID. Agents may omit IDs in
// ACP v1; only then are adjacent same-role text chunks treated as one message.
func appendChunk(t *thread, role, id string, block acp.ContentBlock) {
	text := contentText(block)
	if text == "" {
		return
	}
	i := -1
	if id != "" {
		for j := len(t.Entries) - 1; j >= 0; j-- {
			if t.Entries[j].Role == role && t.Entries[j].MessageID == id {
				i = j
				break
			}
		}
	} else if last := len(t.Entries) - 1; last >= 0 && t.Entries[last].Role == role && t.Entries[last].MessageID == "" && block.Text != nil {
		i = last
	}
	if i < 0 {
		t.Entries = append(t.Entries, state.Entry{ID: state.ID(), Role: role, MessageID: id})
		i = len(t.Entries) - 1
	}
	t.Entries[i].Text += text
	if block.Text == nil {
		t.Entries[i].Details = joinNonEmpty("\n", t.Entries[i].Details, pretty(block))
	}
}

func joinNonEmpty(sep string, values ...string) string {
	var parts []string
	for _, v := range values {
		if v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, sep)
}

// echoLocked reports whether a user chunk is the agent's account of the prompt
// Mu just sent, which is already shown as Mu's local user entry. With message
// IDs, the first user message of the turn is identified once and the agent's
// text replaces the local copy if it differs. Without IDs, text that diverges
// from the prompt is an agent-authored user message and gets its own entry.
func echoLocked(t *thread, r *runtime, id string, block acp.ContentBlock) bool {
	e := &r.echo
	if r.job == nil || e.target == "" || block.Text == nil {
		return false
	}
	if id != "" {
		if e.id == "" && e.text == "" {
			e.id = id
		}
		if id != e.id {
			return false
		}
	} else if e.id != "" {
		return false
	}
	e.text += block.Text.Text
	local := entryByID(t, e.local)
	if id != "" {
		if local != nil {
			local.MessageID = id
			if !strings.HasPrefix(e.target, e.text) {
				local.Text = e.text
			}
		}
		return true
	}
	if strings.HasPrefix(e.target, e.text) {
		return true
	}
	if divergent := entryByID(t, e.entry); divergent != nil {
		divergent.Text = e.text
	} else {
		e.entry = state.ID()
		t.Entries = append(t.Entries, state.Entry{ID: e.entry, Role: "user", Text: e.text})
	}
	return true
}

func entryByID(t *thread, id string) *state.Entry {
	if id == "" {
		return nil
	}
	for i := range t.Entries {
		if t.Entries[i].ID == id {
			return &t.Entries[i]
		}
	}
	return nil
}

// toolCall is the projection of one ACP tool call. Updates patch the fields
// they supply; collections such as content are replaced as a whole. The
// visible entry is derived from it, never edited directly.
type toolCall struct {
	Title     string                 `json:"title,omitempty"`
	Kind      acp.ToolKind           `json:"kind,omitempty"`
	Status    acp.ToolCallStatus     `json:"status,omitempty"`
	Content   []acp.ToolCallContent  `json:"content,omitempty"`
	Locations []acp.ToolCallLocation `json:"locations,omitempty"`
	RawInput  any                    `json:"rawInput,omitempty"`
	RawOutput any                    `json:"rawOutput,omitempty"`
	Meta      map[string]any         `json:"_meta,omitempty"`
}

func (c *toolCall) patch(u *acp.SessionToolCallUpdate) {
	if u.Title != nil {
		c.Title = *u.Title
	}
	if u.Kind != nil {
		c.Kind = *u.Kind
	}
	if u.Status != nil {
		c.Status = *u.Status
	}
	if u.Content != nil {
		c.Content = u.Content
	}
	if u.Locations != nil {
		c.Locations = u.Locations
	}
	if u.RawInput != nil {
		c.RawInput = u.RawInput
	}
	if u.RawOutput != nil {
		c.RawOutput = u.RawOutput
	}
	if u.Meta != nil {
		c.Meta = u.Meta
	}
}

func (c *toolCall) terminal(id string) bool {
	return slices.ContainsFunc(c.Content, func(item acp.ToolCallContent) bool {
		return item.Terminal != nil && item.Terminal.TerminalId == id
	})
}

func renderTool(t *thread, r *runtime, id string, c *toolCall) {
	var e *state.Entry
	for i := len(t.Entries) - 1; i >= 0; i-- {
		if t.Entries[i].Role == "tool" && t.Entries[i].ToolID == id {
			e = &t.Entries[i]
			break
		}
	}
	if e == nil {
		t.Entries = append(t.Entries, state.Entry{ID: state.ID(), Role: "tool", ToolID: id})
		e = &t.Entries[len(t.Entries)-1]
	}
	e.Title, e.Status = c.Title, string(c.Status)
	if e.Title == "" {
		e.Title = "Tool"
	}
	e.Text, e.Details = toolText(c.Content, c.RawOutput, r.outputs), pretty(c)
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

func (a *App) terminalOutput(id string, r *runtime, epoch uint64, session acp.SessionId, terminalID, output string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !r.acceptUpdates || r.epoch != epoch || r.session != string(session) {
		return
	}
	r.outputs[terminalID] = output
	if t := a.threadLocked(id); t != nil {
		for toolID, call := range r.tools {
			if call.terminal(terminalID) {
				renderTool(t, r, toolID, call)
			}
		}
	}
	a.dirty = true
	a.notifyLocked()
}
