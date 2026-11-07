package piacp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/acp-sdk/jsonrpc"
)

// object is a JSON object for Pi.
type object = map[string]any

// turn is a prompt in progress.
type turn struct {
	settled   chan struct{}
	settle    sync.Once
	aborted   bool
	cancelled bool
	stop      string // the last assistant message's stop reason
	failure   string // and its error
	usage     acp.Usage
}

// promptMessage turns ACP content into Pi's one message and its images, which Pi
// shows the model after the text, wherever they were among it. Only the kinds the
// adapter advertises are accepted.
func promptMessage(content []acp.ContentBlock) (string, []object, error) {
	var b strings.Builder
	onLine := func(text string) {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(text)
	}
	var images []object
	for _, c := range content {
		switch c.Type {
		case acp.ContentBlockTypeText:
			b.WriteString(c.Text)
		case acp.ContentBlockTypeImage:
			image := object{"type": "image", "data": c.Data}
			if c.MimeType != nil {
				image["mimeType"] = *c.MimeType
			}
			images = append(images, image)
		case acp.ContentBlockTypeResourceLink:
			if c.URI != nil {
				onLine(*c.URI)
			}
		case acp.ContentBlockTypeResource:
			if c.Resource.Text == nil {
				return "", nil, invalidParams("Pi can't take %s: only text and images reach its model", describeUnsupported(c))
			}
			// The model gets to know what it's reading.
			onLine(c.Resource.URI + ":\n" + *c.Resource.Text)
		default:
			return "", nil, invalidParams("Pi can't take %s: only text and images reach its model", describeUnsupported(c))
		}
	}
	return b.String(), images, nil
}

// describeUnsupported names content Pi has no way to receive.
func describeUnsupported(c acp.ContentBlock) string {
	if c.Type == acp.ContentBlockTypeResource {
		kind := "a binary file"
		if c.Resource.MimeType != nil {
			kind = "a " + *c.Resource.MimeType + " file"
		}
		return kind
	}
	return string(c.Type)
}

func (s *session) beginTurn() (*turn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn != nil {
		return nil, invalidParams("session %s is already running a turn", s.id)
	}
	s.turn = &turn{settled: make(chan struct{})}
	return s.turn, nil
}

func (s *session) endTurn() {
	s.mu.Lock()
	s.turn = nil
	s.mu.Unlock()
}

// compactRequest recognizes the one Pi command the adapter handles itself. Other
// terminal commands would reach the model as plain text.
func compactRequest(text string) (instructions string, ok bool) {
	fields := strings.TrimSpace(text)
	rest, ok := strings.CutPrefix(fields, "/compact")
	if !ok || (rest != "" && !strings.ContainsAny(rest[:1], " \t\n")) {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

func (s *session) prompt(ctx context.Context, content []acp.ContentBlock) (*acp.PromptResponse, error) {
	text, images, err := promptMessage(content)
	if err != nil {
		return nil, err
	}
	t, err := s.beginTurn()
	if err != nil {
		return nil, err
	}
	defer s.endTurn()
	if instructions, ok := compactRequest(text); ok {
		if len(images) > 0 {
			return nil, invalidParams("/compact can't take images. Send it without them")
		}
		return s.compact(ctx, t, instructions)
	}
	// An extension's command answers only after its dialogs do, so this has no
	// deadline of its own. The process ending or a cancellation ends it.
	var accepted struct {
		Disposition string `json:"disposition"`
	}
	fields := map[string]any{"message": text}
	if len(images) > 0 {
		fields["images"] = images
	}
	err = s.pi.call(ctx, "prompt", fields, &accepted)
	var refused *rejected
	switch {
	case errors.As(err, &refused):
		return nil, s.refusal(refused)
	case err != nil:
		return s.ended(ctx, t, err)
	case accepted.Disposition == "handled":
		return s.finished(ctx, t)
	}
	select {
	case <-t.settled:
		return s.finished(ctx, t)
	case <-s.pi.exited:
		return s.ended(ctx, t, s.pi.exitError(errors.New("Pi exited during the turn")))
	}
}

// refusal explains a prompt Pi wouldn't start. With no provider configured the
// fix is outside Mu.
func (s *session) refusal(r *rejected) error {
	if strings.HasPrefix(r.message, "No API key") {
		return &jsonrpc.Error{Code: codeAuthRequired, Message: "Pi isn't signed in to a model provider. Run pi in a terminal, then enter /login."}
	}
	return errors.New(r.message)
}

// ended reports a turn cut short by its process stopping, which is expected if
// the turn was being cancelled or the session closed.
func (s *session) ended(ctx context.Context, t *turn, err error) (*acp.PromptResponse, error) {
	s.mu.Lock()
	cancelled := t.cancelled
	s.mu.Unlock()
	if cancelled {
		return &acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	if !s.pi.alive() {
		s.a.forget(s)
	}
	return nil, err
}

// finished turns the settled turn into ACP's answer. Pi reports a failed turn as
// an accepted prompt whose last assistant message has an error, so that's where
// it becomes an error response.
func (s *session) finished(ctx context.Context, t *turn) (*acp.PromptResponse, error) {
	timed, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	s.reportUsage(timed)
	if s.hasFile() {
		s.a.forgetUnsent(string(s.ID()))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	usage := t.usage
	response := &acp.PromptResponse{Usage: &usage}
	if usage.TotalTokens == 0 {
		response.Usage = nil
	}
	switch {
	case t.cancelled || t.aborted || t.stop == "aborted":
		response.StopReason = acp.StopReasonCancelled
	case t.stop == "error":
		message := t.failure
		if message == "" {
			message = "Pi's model request failed"
		}
		return nil, errors.New(message)
	case t.stop == "length":
		response.StopReason = acp.StopReasonMaxTokens
	default:
		response.StopReason = acp.StopReasonEndTurn
	}
	return response, nil
}

func (s *session) compact(ctx context.Context, t *turn, instructions string) (*acp.PromptResponse, error) {
	fields := map[string]any{}
	if instructions != "" {
		fields["customInstructions"] = instructions
	}
	var result struct {
		TokensBefore         uint64 `json:"tokensBefore"`
		EstimatedTokensAfter uint64 `json:"estimatedTokensAfter"`
	}
	err := s.pi.call(ctx, "compact", fields, &result)
	s.mu.Lock()
	cancelled := t.cancelled
	s.mu.Unlock()
	if cancelled {
		return &acp.PromptResponse{StopReason: acp.StopReasonCancelled}, nil
	}
	// Pi declining, such as a session that's too small, isn't a failure of the turn.
	var refused *rejected
	if errors.As(err, &refused) {
		s.send(acp.AgentMessageChunkSessionUpdate(textBlock(refused.message)))
		return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	}
	if err != nil {
		return s.ended(ctx, t, err)
	}
	s.send(acp.AgentMessageChunkSessionUpdate(textBlock(fmt.Sprintf("Compacted the context from about %d tokens to about %d.", result.TokensBefore, result.EstimatedTokensAfter))))
	// Pi has no context figure until the next response, so its own estimate has to do
	// for now, rather than the size from before compacting.
	s.mu.Lock()
	size, cost := s.contextSize, s.cost
	s.mu.Unlock()
	if size > 0 {
		s.send(acp.SessionUpdate{SessionUpdate: acp.SessionUpdateTypeUsageUpdate, Used: result.EstimatedTokensAfter, Size: size, Cost: cost})
	}
	return &acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

// cancel stops the turn in progress. Abort covers a streaming response, a running
// tool and a retry delay. Pi can't reach any of those while a dialog waits, so
// those are answered first.
func (s *session) cancel() {
	s.mu.Lock()
	t := s.turn
	if t == nil || s.closing {
		s.mu.Unlock()
		return
	}
	t.cancelled = true
	pending := make([]string, 0, len(s.dialogs))
	for id := range s.dialogs {
		pending = append(pending, id)
	}
	clear(s.dialogs)
	s.background.Add(1)
	s.mu.Unlock()
	for _, id := range pending {
		_ = s.pi.write(map[string]any{"type": "extension_ui_response", "id": id, "cancelled": true})
	}
	go func() {
		defer s.background.Done()
		ctx, cancel := context.WithTimeout(s.ctx, commandTimeout)
		defer cancel()
		if err := s.pi.call(ctx, "abort", nil, nil); err != nil && s.pi.alive() {
			s.a.diag("could not abort session %s: %v", s.ID(), err)
		}
	}()
}
