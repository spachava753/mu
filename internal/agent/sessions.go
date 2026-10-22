package agent

import (
	"context"
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
	if c.SessionCapabilities.Resume == nil {
		missing = append(missing, "session/resume")
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

// CloseSession releases agent resources before terminating this thread's process.
// A failed or hung close is reported, but cannot prevent local process cleanup.
func (p *Process) CloseSession(session string) error {
	defer p.Close()
	if session == "" || !p.Alive() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := p.Conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: acp.SessionId(session)})
	if err != nil {
		return fmt.Errorf("close session %s: %w", session, p.Error(err))
	}
	return nil
}
