package piacp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spachava753/acp-sdk/acp"
)

// message is Pi's AgentMessage: the fields of every role Mu shows. Roles this
// doesn't know, which extensions can add, are skipped.
type message struct {
	Role         string          `json:"role"`
	Content      json.RawMessage `json:"content"`
	StopReason   string          `json:"stopReason"`
	ErrorMessage string          `json:"errorMessage"`
	Usage        *messageUsage   `json:"usage"`
	// toolResult
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	IsError    bool            `json:"isError"`
	Details    json.RawMessage `json:"details"`
	// bashExecution
	Command   string `json:"command"`
	Output    string `json:"output"`
	ExitCode  *int   `json:"exitCode"`
	Cancelled bool   `json:"cancelled"`
}

type messageUsage struct {
	Input       uint64 `json:"input"`
	Output      uint64 `json:"output"`
	CacheRead   uint64 `json:"cacheRead"`
	CacheWrite  uint64 `json:"cacheWrite"`
	Reasoning   uint64 `json:"reasoning"`
	TotalTokens uint64 `json:"totalTokens"`
}

// block is a content block of any message role.
type block struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Thinking string `json:"thinking"`
	// toolCall
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	// image
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

// blocks decodes content that is either a plain string or a list of blocks.
func blocks(raw json.RawMessage) []block {
	if len(raw) == 0 {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if text == "" {
			return nil
		}
		return []block{{Type: "text", Text: text}}
	}
	var list []block
	_ = json.Unmarshal(raw, &list)
	return list
}

func textBlock(text string) acp.ContentBlock {
	return acp.ContentBlock{Type: acp.ContentBlockTypeText, Text: text}
}

func messageID(id string) *acp.MessageId {
	value := acp.MessageId(id)
	return &value
}

// toolContent is what a tool call shows of Pi's result blocks.
func toolContent(list []block) []acp.ToolCallContent {
	content := []acp.ToolCallContent{}
	for _, b := range list {
		switch b.Type {
		case "text":
			if b.Text != "" {
				content = append(content, acp.ToolCallContent{Type: acp.ToolCallContentTypeContent, Content: textBlock(b.Text)})
			}
		case "image":
			mime := b.MimeType
			content = append(content, acp.ToolCallContent{Type: acp.ToolCallContentTypeContent, Content: acp.ContentBlock{Type: acp.ContentBlockTypeImage, Data: b.Data, MimeType: &mime}})
		}
	}
	return content
}

// toolKind classifies Pi's built-in tools. Anything else, such as an extension's
// tool, is other.
func toolKind(name string) acp.ToolKind {
	switch name {
	case "read":
		return acp.ToolKindRead
	case "edit", "write":
		return acp.ToolKindEdit
	case "bash":
		return acp.ToolKindExecute
	case "grep", "find", "ls":
		return acp.ToolKindSearch
	}
	return acp.ToolKindOther
}

// toolTitle names a call by the tool and what it acts on.
func toolTitle(name string, arguments map[string]any) string {
	for _, key := range []string{"command", "path", "pattern", "query", "url", "code", "objective"} {
		if value, ok := arguments[key].(string); ok && value != "" {
			line, _, _ := strings.Cut(value, "\n")
			return shorten(name+" "+line, 120)
		}
	}
	return name
}

// toolLocations reports the file a built-in tool acts on, made absolute against
// the session's folder.
func toolLocations(cwd string, arguments map[string]any) []acp.ToolCallLocation {
	path, ok := arguments["path"].(string)
	if !ok || path == "" {
		return nil
	}
	return []acp.ToolCallLocation{{Path: absolute(cwd, path)}}
}

// argumentDiffs shows what a file tool is about to do, from its arguments. A write
// doesn't say what it replaces, so all of its text reads as added.
func argumentDiffs(cwd, name string, arguments map[string]any) []acp.ToolCallContent {
	path, _ := arguments["path"].(string)
	if path == "" {
		return nil
	}
	path = absolute(cwd, path)
	var diffs []acp.ToolCallContent
	switch name {
	case "edit":
		edits, _ := arguments["edits"].([]any)
		for _, item := range edits {
			edit, _ := item.(map[string]any)
			before, okBefore := edit["oldText"].(string)
			after, okAfter := edit["newText"].(string)
			if okBefore && okAfter {
				diffs = append(diffs, acp.ToolCallContent{Type: acp.ToolCallContentTypeDiff, Path: path, OldText: &before, NewText: after})
			}
		}
	case "write":
		if content, ok := arguments["content"].(string); ok {
			diffs = append(diffs, acp.ToolCallContent{Type: acp.ToolCallContentTypeDiff, Path: path, NewText: content})
		}
	}
	return diffs
}

// patchDiffs reads the unified patch a tool reports in its result's details, which
// has the changes as applied and the lines around them, as one ACP diff per hunk.
func patchDiffs(cwd string, details json.RawMessage) []acp.ToolCallContent {
	var result struct {
		Patch string `json:"patch"`
	}
	if len(details) == 0 || json.Unmarshal(details, &result) != nil || result.Patch == "" {
		return nil
	}
	var diffs []acp.ToolCallContent
	var path string
	var before, after strings.Builder
	inHunk := false
	flush := func() {
		if inHunk && path != "" {
			old := before.String()
			diffs = append(diffs, acp.ToolCallContent{Type: acp.ToolCallContentTypeDiff, Path: path, OldText: &old, NewText: after.String()})
		}
		before.Reset()
		after.Reset()
	}
	for line := range strings.Lines(result.Patch) {
		switch {
		case !inHunk && strings.HasPrefix(line, "--- "):
		case !inHunk && strings.HasPrefix(line, "+++ "):
			path = absolute(cwd, strings.TrimSpace(line[4:]))
		case strings.HasPrefix(line, "@@"):
			flush()
			inHunk = true
		case !inHunk:
		case strings.HasPrefix(line, "-"):
			before.WriteString(line[1:])
		case strings.HasPrefix(line, "+"):
			after.WriteString(line[1:])
		case strings.HasPrefix(line, " "):
			before.WriteString(line[1:])
			after.WriteString(line[1:])
		}
	}
	flush()
	return diffs
}

func absolute(cwd, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

func shorten(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

func toolStatus(failed bool) *acp.ToolCallStatus {
	status := acp.ToolCallStatusCompleted
	if failed {
		status = acp.ToolCallStatusFailed
	}
	return &status
}

// bashOutput is how a direct shell command's result reads, since it has no
// result blocks.
func bashOutput(m message) string {
	switch {
	case m.Cancelled:
		return m.Output + "\n(cancelled)"
	case m.ExitCode != nil && *m.ExitCode != 0:
		return fmt.Sprintf("%s\n(exit code %d)", m.Output, *m.ExitCode)
	}
	return m.Output
}
