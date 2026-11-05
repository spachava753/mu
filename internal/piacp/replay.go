package piacp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/spachava753/acp-sdk/acp"
)

// sessionEntry is one node of Pi's append-only session tree.
type sessionEntry struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	ParentID string          `json:"parentId"`
	Message  message         `json:"message"`
	Summary  string          `json:"summary"`
	Content  json.RawMessage `json:"content"`
	Display  bool            `json:"display"`
}

// replay reports a session's history: everything on its active branch, from Pi's
// entries rather than its messages. The messages are what the model sees next, which
// after a compaction leaves out most of the conversation, and abandoned branches
// have to be left out as well.
func (s *session) replay(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	var history struct {
		Entries []sessionEntry `json:"entries"`
		LeafID  string         `json:"leafId"`
	}
	if err := s.pi.call(ctx, "get_entries", nil, &history); err != nil {
		return err
	}
	for i, entry := range activeBranch(history.Entries, history.LeafID) {
		s.replayEntry(i, entry)
	}
	return nil
}

// activeBranch follows parents back from the leaf, and returns that path in order.
func activeBranch(entries []sessionEntry, leaf string) []sessionEntry {
	byID := make(map[string]sessionEntry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	var branch []sessionEntry
	for id := leaf; id != ""; {
		entry, ok := byID[id]
		if !ok {
			break
		}
		delete(byID, id) // A cycle in a damaged file can't loop forever.
		branch = append(branch, entry)
		id = entry.ParentID
	}
	slices.Reverse(branch)
	return branch
}

func (s *session) replayEntry(index int, entry sessionEntry) {
	id := func(part int) *acp.MessageId { return messageID(fmt.Sprintf("replay-%d-%d", index, part)) }
	switch entry.Type {
	case "message":
		s.replayMessage(index, entry.Message)
	case "compaction", "branch_summary":
		if entry.Summary != "" {
			update := acp.AgentThoughtChunkSessionUpdate(textBlock(entry.Summary))
			update.MessageID = id(0)
			s.send(update)
		}
	case "custom_message":
		if entry.Display {
			for part, b := range blocks(entry.Content) {
				update := acp.AgentMessageChunkSessionUpdate(contentFor(b))
				update.MessageID = id(part)
				s.send(update)
			}
		}
	}
}

// replayMessage reports one message. A system message isn't part of the conversation.
func (s *session) replayMessage(index int, m message) {
	id := func(part int) *acp.MessageId { return messageID(fmt.Sprintf("replay-%d-%d", index, part)) }
	switch m.Role {
	case "user":
		for part, b := range blocks(m.Content) {
			update := acp.UserMessageChunkSessionUpdate(contentFor(b))
			update.MessageID = id(part)
			s.send(update)
		}
	case "assistant":
		for part, b := range blocks(m.Content) {
			switch b.Type {
			case "text":
				if b.Text != "" {
					update := acp.AgentMessageChunkSessionUpdate(textBlock(b.Text))
					update.MessageID = id(part)
					s.send(update)
				}
			case "thinking":
				if b.Thinking != "" {
					update := acp.AgentThoughtChunkSessionUpdate(textBlock(b.Thinking))
					update.MessageID = id(part)
					s.send(update)
				}
			case "toolCall":
				s.reportToolCall(b.ID, b.Name, b.Arguments, acp.ToolCallStatusPending)
			}
		}
	case "toolResult":
		s.finishTool(m.ToolCallID, m.ToolName, blocks(m.Content), m.Details, m.IsError, nil)
	case "bashExecution":
		// A shell command the user ran in Pi directly.
		toolID := fmt.Sprintf("replay-bash-%d", index)
		arguments := map[string]any{"command": m.Command}
		s.reportToolCall(toolID, "bash", arguments, acp.ToolCallStatusPending)
		failed := m.Cancelled || (m.ExitCode != nil && *m.ExitCode != 0)
		s.finishTool(toolID, "bash", []block{{Type: "text", Text: bashOutput(m)}}, nil, failed, nil)
	}
}

func contentFor(b block) acp.ContentBlock {
	if b.Type == "image" {
		mime := b.MimeType
		return acp.ContentBlock{Type: acp.ContentBlockTypeImage, Data: b.Data, MimeType: &mime}
	}
	return textBlock(b.Text)
}
