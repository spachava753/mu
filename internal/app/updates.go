package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/media"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/textdiff"
)

func contentText(b acp.ContentBlock) string {
	switch b.Type {
	case acp.ContentBlockTypeText:
		return b.Text
	case acp.ContentBlockTypeResourceLink:
		return b.Name + " (" + deref(b.URI) + ")"
	case acp.ContentBlockTypeImage:
		return "\n[Image: " + deref(b.MimeType) + "; see content details.]\n"
	case acp.ContentBlockTypeAudio:
		return "\n[Audio: " + deref(b.MimeType) + "; see content details.]\n"
	case acp.ContentBlockTypeResource:
		if b.Resource.Text != nil {
			return *b.Resource.Text
		}
		return "\n[Binary resource; see content details.]\n"
	}
	return ""
}

func deref[T any](v *T) T {
	var zero T
	if v == nil {
		return zero
	}
	return *v
}

// displayTabWidth is how far a tab reaches in what Mu shows of tool calls. The text
// renderer drops tabs, which would flatten the indentation of code.
const displayTabWidth = 4

func expandTabs(text string) string {
	return strings.ReplaceAll(text, "\t", strings.Repeat(" ", displayTabWidth))
}

// toolInput says what a tool was asked to do, for any tool: when there's one piece of
// text among its arguments, such as a command or a script, that text with its real
// line breaks, and otherwise all of them. A tool that shows a diff needs neither.
func toolInput(raw any) string {
	if raw == nil {
		return ""
	}
	if arguments, ok := raw.(map[string]any); ok {
		var only string
		count := 0
		for _, value := range arguments {
			if text, ok := value.(string); ok {
				only = text
				count++
			}
		}
		if count == 1 {
			return expandTabs(only)
		}
	}
	return expandTabs(pretty(raw))
}

// toolText is a tool call's content as text. Diffs are left to toolDiffs, and a tool
// that has any isn't described by its raw output.
func toolText(content []acp.ToolCallContent, raw any) string {
	var b strings.Builder
	hasDiff := false
	for _, c := range content {
		switch c.Type {
		case acp.ToolCallContentTypeContent:
			b.WriteString(contentText(c.Content))
			b.WriteByte('\n')
		case acp.ToolCallContentTypeDiff:
			hasDiff = true
		case acp.ToolCallContentTypeTerminal:
			// Mu runs no terminals, so it has no output for this reference.
			b.WriteString("Terminal: " + string(c.TerminalID) + "\n")
		}
	}
	if b.Len() == 0 && raw != nil && !hasDiff {
		b.WriteString(pretty(raw))
	}
	return expandTabs(b.String())
}

// toolDiffs computes the diffs in a tool call's content. This is bounded work, done
// once when the content changes, so nothing that draws ever has to compute one.
func toolDiffs(content []acp.ToolCallContent) []textdiff.File {
	var changes []textdiff.Change
	for _, c := range content {
		if c.Type == acp.ToolCallContentTypeDiff {
			change := textdiff.Change{Path: c.Path, New: expandTabs(c.NewText)}
			if c.OldText != nil {
				old := expandTabs(*c.OldText)
				change.Old = &old
			}
			changes = append(changes, change)
		}
	}
	return textdiff.Files(changes)
}

func pretty(value any) string { b, _ := json.MarshalIndent(value, "", "  "); return string(b) }

// storeMeta keeps extension metadata by source and reports whether it did.
func storeMeta(s *state.Session, source string, meta map[string]any) bool {
	if len(meta) == 0 {
		return false
	}
	if s.Metadata == nil {
		s.Metadata = map[string]json.RawMessage{}
	}
	b, err := json.Marshal(meta)
	if err == nil {
		s.Metadata[source] = b
	}
	return err == nil
}

// A connection generation prevents late notifications from a closed process
// from mutating a reconnected thread, even if it resumes the same session ID.
func (a *App) receiveLocked(id string, r *runtime, epoch uint64, n acp.SessionNotification) {
	if r.epoch != epoch {
		return
	}
	if r.initializing {
		r.initialUpdates = append(r.initialUpdates, n)
		if len(r.pending) > 0 {
			// A pending permission's card is derived from buffered tool calls.
			a.notifyLocked()
		}
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
	if r.session != string(n.SessionID) {
		return
	}
	t := a.threadLocked(id)
	if t == nil {
		return
	}
	u := n.Update
	// Preserve extension metadata without guessing provider-specific semantics.
	sessionChanged := storeMeta(&t.Session, "notification", n.Meta)
	sessionChanged = storeMeta(&t.Session, string(u.SessionUpdate), u.Meta) || sessionChanged
	switch u.SessionUpdate {
	case acp.SessionUpdateTypeUserMessageChunk:
		block, _ := u.Content.(acp.ContentBlock)
		if r.acceptUpdates && !echoLocked(t, r, messageID(u.MessageID), block) {
			appendChunk(t, "user", messageID(u.MessageID), block)
		}
	case acp.SessionUpdateTypeAgentMessageChunk:
		if block, _ := u.Content.(acp.ContentBlock); r.acceptUpdates {
			appendChunk(t, "assistant", messageID(u.MessageID), block)
		}
	case acp.SessionUpdateTypeAgentThoughtChunk:
		if block, _ := u.Content.(acp.ContentBlock); r.acceptUpdates {
			appendChunk(t, "thought", messageID(u.MessageID), block)
		}
	case acp.SessionUpdateTypeToolCall, acp.SessionUpdateTypeToolCallUpdate:
		if !r.acceptUpdates {
			break
		}
		id := string(u.ToolCallID)
		call := r.tools[id]
		if call == nil || u.SessionUpdate == acp.SessionUpdateTypeToolCall {
			call = &toolCall{}
			r.tools[id] = call
		}
		patch := updatePatch(u)
		call.patch(patch)
		if patch.Content != nil {
			call.diffs = toolDiffs(call.Content)
		}
		if r.job != nil && !r.initializing { // Not replayed history.
			r.turnTools[id] = true
		}
		renderTool(t, id, call)
	case acp.SessionUpdateTypePlan:
		setPlan(t, "legacy", planText(u.Entries), pretty(u.Entries))
	case acp.SessionUpdateTypePlanUpdate:
		p := u.Plan
		switch p.Type {
		case acp.PlanUpdateContentTypeItems:
			setPlan(t, string(p.PlanID), planText(p.Entries), pretty(p))
		case acp.PlanUpdateContentTypeMarkdown:
			setPlan(t, string(p.PlanID), p.Content, pretty(p))
		case acp.PlanUpdateContentTypeFile:
			setPlan(t, string(p.PlanID), "Plan file: "+p.URI, pretty(p))
		}
	case acp.SessionUpdateTypePlanRemoved:
		t.Entries = slices.DeleteFunc(t.Entries, func(e state.Entry) bool { return e.Role == "plan" && e.PlanID == string(u.PlanID) })
	case acp.SessionUpdateTypeAvailableCommandsUpdate:
		t.Session.Commands = u.AvailableCommands
		sessionChanged = true
	case acp.SessionUpdateTypeConfigOptionUpdate:
		t.Session.Options = u.ConfigOptions
		sessionChanged = true
	case acp.SessionUpdateTypeCurrentModeUpdate:
		modes := acp.SessionModeState{}
		if t.Session.Modes != nil {
			modes = *t.Session.Modes
		}
		modes.CurrentModeID = u.CurrentModeID
		t.Session.Modes = &modes
		r.modeUpdates++
		sessionChanged = true
	case acp.SessionUpdateTypeSessionInfoUpdate:
		if u.Title != nil && strings.TrimSpace(*u.Title) != "" && *u.Title != t.Title && !t.Renamed {
			t.Title = *u.Title
			a.dirty = true // The title is the only persisted field an update changes.
		}
		if u.UpdatedAt != nil {
			t.Session.AgentUpdatedAt = *u.UpdatedAt
			sessionChanged = true
		}
	case acp.SessionUpdateTypeUsageUpdate:
		t.Session.Usage = &state.Usage{Used: u.Used, Size: u.Size, Cost: u.Cost}
		sessionChanged = true
	}
	if sessionChanged {
		r.sessionVersion++
	}
	a.notifyLocked()
}

func messageID(id *acp.MessageId) string { return string(deref(id)) }

// appendChunk groups chunks by the agent's message ID. Agents may omit IDs in
// ACP v1; only then are adjacent same-role text chunks treated as one message, and
// the files in a user's message go with its text, where they were in it.
func appendChunk(t *thread, role, id string, block acp.ContentBlock) {
	text := contentText(block)
	var file *media.File
	if role == "user" {
		if file = media.FromBlock(block); file != nil {
			text = file.Name
		}
	}
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
	} else if last := len(t.Entries) - 1; last >= 0 && t.Entries[last].Role == role && t.Entries[last].MessageID == "" && (block.Type == acp.ContentBlockTypeText || role == "user") {
		i = last
	}
	if i < 0 {
		t.Entries = append(t.Entries, state.Entry{ID: state.ID(), Role: role, MessageID: id})
		i = len(t.Entries) - 1
	}
	e := &t.Entries[i]
	switch {
	case file != nil:
		if e.Parts == nil && e.Text != "" {
			e.Parts = []state.Part{{Text: e.Text}}
		}
		e.Parts = state.AddPart(e.Parts, state.Part{File: file})
	case e.Parts != nil:
		e.Parts = state.AddPart(e.Parts, state.Part{Text: text})
	}
	e.Text += text
	if file == nil && block.Type != acp.ContentBlockTypeText {
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
// from the prompt is an agent-authored user message and gets its own entry. The
// prompt's files are echoed as files, and the local copy already has them.
func echoLocked(t *thread, r *runtime, id string, block acp.ContentBlock) bool {
	e := &r.echo
	if r.job == nil || e.local == "" {
		return false
	}
	file := media.FromBlock(block)
	if id != "" {
		if e.id == "" && e.text == "" {
			// Adopt a message as the echo only if its first chunk starts like the
			// prompt: text it starts with, or a file if it has any. Otherwise it
			// is the agent's own user message, shown separately. The decision is
			// made once per message.
			if e.rejected[id] {
				return false
			}
			if !startsEcho(e, file, block) {
				if e.rejected == nil {
					e.rejected = map[string]bool{}
				}
				e.rejected[id] = true
				return false
			}
			e.id = id
		}
		if id != e.id {
			return false
		}
	} else if e.id != "" {
		return false
	}
	local := entryByID(t, e.local)
	if file != nil {
		// Once a message without IDs has diverged, its files are its own.
		if diverged := id == "" && e.entry != ""; !diverged {
			if e.files == 0 {
				return false
			}
			e.files--
		}
		e.parts = state.AddPart(e.parts, state.Part{File: file})
	} else if block.Type != acp.ContentBlockTypeText {
		return false
	} else {
		e.text += block.Text
		e.parts = state.AddPart(e.parts, state.Part{Text: block.Text})
	}
	if id != "" {
		if local != nil {
			local.MessageID = id
			if !strings.HasPrefix(e.target, e.text) {
				local.Text = state.PartsText(e.parts)
				if local.Parts != nil {
					local.Parts = e.parts
				}
			}
		}
		return true
	}
	if strings.HasPrefix(e.target, e.text) {
		return true
	}
	divergent := entryByID(t, e.entry)
	if divergent == nil {
		e.entry = state.ID()
		t.Entries = append(t.Entries, state.Entry{ID: e.entry, Role: "user"})
		divergent = &t.Entries[len(t.Entries)-1]
	}
	divergent.Text = state.PartsText(e.parts)
	if slices.ContainsFunc(e.parts, func(p state.Part) bool { return p.File != nil }) {
		divergent.Parts = e.parts
	}
	return true
}

// startsEcho reports whether a message's first chunk could be the prompt's echo.
func startsEcho(e *promptEcho, file *media.File, block acp.ContentBlock) bool {
	if file != nil {
		return e.files > 0
	}
	return block.Type == acp.ContentBlockTypeText && strings.HasPrefix(e.target, block.Text)
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
	// cancelled marks an unfinished call of a turn the user stopped. It only
	// changes the displayed status; later agent updates still apply.
	cancelled bool
	diffs     []textdiff.File // of Content, as last computed
}

func (c *toolCall) finished() bool {
	return c.Status == acp.ToolCallStatusCompleted || c.Status == acp.ToolCallStatusFailed
}

// toolPatch holds the fields a tool call notification or permission request
// supplies; nil fields are left unchanged.
type toolPatch struct {
	Title     *string
	Kind      *acp.ToolKind
	Status    *acp.ToolCallStatus
	Content   *[]acp.ToolCallContent
	Locations *[]acp.ToolCallLocation
	RawInput  any
	RawOutput any
	Meta      acp.Meta
}

// updatePatch reads a tool_call or tool_call_update. The SDK decodes the
// content of the former as a slice and of the latter as an optional slice.
func updatePatch(u acp.SessionUpdate) toolPatch {
	p := toolPatch{Title: u.Title, Kind: u.Kind, Status: u.Status, Locations: u.Locations, RawInput: u.RawInput, RawOutput: u.RawOutput, Meta: u.Meta}
	switch content := u.Content.(type) {
	case []acp.ToolCallContent:
		p.Content = &content
	case *[]acp.ToolCallContent:
		p.Content = content
	}
	return p
}

// bufferedTool projects one tool call from setup notifications that are
// buffered until session/load succeeds, without committing them.
func bufferedTool(updates []acp.SessionNotification, id string) *toolCall {
	var call *toolCall
	for _, n := range updates {
		u := n.Update
		if string(u.ToolCallID) != id {
			continue
		}
		switch u.SessionUpdate {
		case acp.SessionUpdateTypeToolCall:
			call = &toolCall{}
		case acp.SessionUpdateTypeToolCallUpdate:
			if call == nil {
				call = &toolCall{}
			}
		default:
			continue
		}
		call.patch(updatePatch(u))
	}
	return call
}

func (c *toolCall) patch(u toolPatch) {
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
		c.Content = *u.Content
	}
	if u.Locations != nil {
		c.Locations = *u.Locations
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

func renderTool(t *thread, id string, c *toolCall) {
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
	if c.cancelled && !c.finished() {
		e.Status = "cancelled"
	}
	if e.Title == "" {
		e.Title = "Tool"
	}
	e.Text, e.Details, e.Diffs = toolText(c.Content, c.RawOutput), pretty(c), c.diffs
	e.Input = ""
	if len(c.diffs) == 0 {
		e.Input = toolInput(c.RawInput)
	}
}

// permissionTool describes a permission request's tool call: the known
// projection, if any, patched with the request's fields. Patching replaces
// fields and never modifies the projection's nested values.
//
// This runs for every snapshot, so requestDiffs were computed when the request
// arrived, and it only chooses which diffs apply.
func permissionTool(known *toolCall, u acp.ToolCallUpdate, requestDiffs []textdiff.File) PermissionTool {
	var call toolCall
	if known != nil {
		call = *known
	}
	call.patch(toolPatch{Title: u.Title, Kind: u.Kind, Status: u.Status, Content: u.Content, Locations: u.Locations, RawInput: u.RawInput, RawOutput: u.RawOutput, Meta: u.Meta})
	var b strings.Builder
	for _, l := range call.Locations {
		b.WriteString(l.Path)
		if l.Line != nil {
			fmt.Fprintf(&b, ":%d", *l.Line)
		}
		b.WriteByte('\n')
	}
	diffs := call.diffs
	if u.Content != nil {
		diffs = requestDiffs
	}
	if text := toolText(call.Content, nil); text != "" {
		b.WriteString(text)
	} else if call.RawInput != nil && len(diffs) == 0 {
		b.WriteString(pretty(call.RawInput) + "\n")
	}
	return PermissionTool{Title: call.Title, Kind: string(call.Kind), Text: b.String(), Details: pretty(call), Diffs: diffs}
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
