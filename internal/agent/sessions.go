package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
)

// RequireSessions defines Mu's minimum agent contract, not optional fallbacks.
func RequireSessions(c acp.AgentCapabilities) error {
	var missing []string
	if !c.LoadSession {
		missing = append(missing, "session/load")
	}
	if c.SessionCapabilities.List == nil {
		missing = append(missing, "session/list")
	}
	if c.SessionCapabilities.Close == nil {
		missing = append(missing, "session/close")
	}
	if c.SessionCapabilities.Fork == nil {
		missing = append(missing, "session/fork")
	}
	if len(missing) != 0 {
		return fmt.Errorf("cannot use this ACP agent: missing required capabilities: %s. Mu requires agent-owned session history; limited agents are not supported", strings.Join(missing, ", "))
	}
	return nil
}

// Bounded runs a request under a hard deadline. A context deadline cannot
// interrupt an SDK write blocked on a full pipe, so on timeout the process is
// closed, which fails the write, and the request is joined before returning.
func (p *Process) Bounded(timeout time.Duration, request func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- request(ctx) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		p.kill()
		<-done
		return fmt.Errorf("agent did not respond within %s and was terminated", timeout)
	}
}

// CloseSession releases one session's agent resources. Other sessions on the
// process are unaffected unless the agent fails to respond within timeout, in
// which case Bounded terminates the process.
func (p *Process) CloseSession(session string, timeout time.Duration) error {
	if session == "" || !p.Alive() {
		return nil
	}
	err := p.Bounded(timeout, func(ctx context.Context) error {
		_, err := p.Conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: acp.SessionId(session)})
		return err
	})
	// Closing is idempotent: -32002 means the agent no longer has the session.
	var request *acp.RequestError
	if errors.As(err, &request) && request.Code == sessionNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("close session %s: %w", session, p.Error(err))
	}
	return nil
}

// sessionNotFound is ACP's "resource not found" error code.
const sessionNotFound = -32002
