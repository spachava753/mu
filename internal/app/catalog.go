package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/state"
)

// DiscoveredSession is metadata, not a conversation or a managed thread.
type DiscoveredSession struct {
	ID, Cwd, Title, UpdatedAt string
	AdditionalDirectories     []string
	ThreadID                  string
}

type Catalog struct {
	Generation        uint64
	Agent             state.AgentConfig
	Loading, Complete bool
	Error             string
	Sessions          []DiscoveredSession
}

func sameConnection(t state.Thread, c state.AgentConfig) bool {
	return t.AgentID == c.ID && t.Command == c.Command
}

// Discover lists all pages on a separate process. Listing never creates or loads
// a session, and an incomplete listing never reconciles the managed references.
func (a *App) Discover(agentID, launchCwd string) error {
	if !filepath.IsAbs(launchCwd) {
		return errors.New("choose an absolute working directory for discovery")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app is closing")
	}
	var config state.AgentConfig
	for _, c := range a.data.Agents {
		if c.ID == agentID {
			config = c
			break
		}
	}
	if config.ID == "" {
		return errors.New("choose a saved agent in Settings")
	}
	if a.catalogCancel != nil {
		a.catalogCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	a.catalogGeneration++
	generation := a.catalogGeneration
	a.catalogCancel = cancel
	a.catalog = Catalog{Generation: generation, Agent: config, Loading: true}
	a.notifyLocked()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer cancel()
		sessions, err := listSessions(ctx, config.Command, launchCwd)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.closed || a.catalog.Generation != generation {
			return
		}
		a.catalog.Loading, a.catalogCancel = false, nil
		if err != nil {
			a.catalog.Error = err.Error()
			a.notifyLocked()
			return
		}
		a.catalog.Sessions, a.catalog.Complete = sessions, true
		found := map[string]DiscoveredSession{}
		for _, s := range sessions {
			found[s.ID] = s
		}
		for i := range a.data.Threads {
			t := &a.data.Threads[i]
			if !sameConnection(*t, config) || t.SessionID == "" {
				continue
			}
			r := a.runtimeLocked(t.ID)
			if s, ok := found[t.SessionID]; ok {
				r.Notice = ""
				// cwd is immutable in ACP. Do not silently retarget filesystem delegation.
				if s.Cwd != t.Cwd || !slices.Equal(s.AdditionalDirectories, t.AdditionalDirectories) {
					r.Notice = "The agent reports different workspace roots. Remove and re-import this reference after checking the agent configuration."
					continue
				}
				if s.Title != "" {
					t.Title = s.Title
				}
				if updated, err := time.Parse(time.RFC3339Nano, s.UpdatedAt); err == nil {
					t.Updated = updated
				}
			} else {
				r.Notice = "Not returned by the agent's latest listing. It may be unavailable or outside this connection's scope. Retry loading or remove it from Mu."
			}
		}
		_ = a.saveLocked()
	}()
	return nil
}

func listSessions(ctx context.Context, command, cwd string) ([]DiscoveredSession, error) {
	p, err := agent.Start(ctx, command, cwd, agent.Hooks{})
	if err != nil {
		return nil, err
	}
	defer p.Close()
	var cursor *string
	seenCursors, seenSessions := map[string]bool{}, map[string]bool{}
	var result []DiscoveredSession
	for page := 0; page < 1000; page++ {
		response, err := p.Conn.ListSessions(ctx, acp.ListSessionsRequest{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("list sessions: %w", p.Error(err))
		}
		for _, s := range response.Sessions {
			id := string(s.SessionId)
			if id == "" || !filepath.IsAbs(s.Cwd) {
				return nil, errors.New("agent returned invalid session metadata")
			}
			for _, root := range s.AdditionalDirectories {
				if !filepath.IsAbs(root) || p.Info.AgentCapabilities.SessionCapabilities.AdditionalDirectories == nil {
					return nil, errors.New("agent returned unsupported additional workspace directories")
				}
			}
			if seenSessions[id] {
				continue
			}
			seenSessions[id] = true
			item := DiscoveredSession{ID: id, Cwd: s.Cwd, AdditionalDirectories: slices.Clone(s.AdditionalDirectories)}
			if s.Title != nil {
				item.Title = *s.Title
			}
			if s.UpdatedAt != nil {
				item.UpdatedAt = *s.UpdatedAt
			}
			result = append(result, item)
		}
		if response.NextCursor == nil {
			return result, nil
		}
		if seenCursors[*response.NextCursor] {
			return nil, errors.New("agent repeated a session-list cursor; discovery is incomplete")
		}
		seenCursors[*response.NextCursor] = true
		cursor = response.NextCursor
	}
	return nil, errors.New("agent session listing exceeded 1000 pages; discovery is incomplete")
}

// Import adds only explicitly selected references from this completed discovery.
// It does not load history, execute prompts, or change the agent's session store.
func (a *App) Import(generation uint64, sessionIDs []string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, errors.New("app is closing")
	}
	c := a.catalog
	if generation != c.Generation || !c.Complete || c.Loading || c.Error != "" {
		return nil, errors.New("refresh discovery before importing sessions")
	}
	if !slices.ContainsFunc(a.data.Agents, func(config state.AgentConfig) bool {
		return config.ID == c.Agent.ID && config.Command == c.Agent.Command
	}) {
		return nil, errors.New("agent configuration changed; refresh discovery before importing")
	}
	old := a.data.Threads
	next := slices.Clone(old)
	var ids []string
	for _, sessionID := range sessionIDs {
		index := slices.IndexFunc(c.Sessions, func(s DiscoveredSession) bool { return s.ID == sessionID })
		if index < 0 {
			return nil, fmt.Errorf("session %s was not in this discovery", sessionID)
		}
		existing := slices.IndexFunc(next, func(t state.Thread) bool { return sameConnection(t, c.Agent) && t.SessionID == sessionID })
		if existing >= 0 {
			ids = append(ids, next[existing].ID)
			continue
		}
		s := c.Sessions[index]
		title := s.Title
		if title == "" {
			title = s.ID
		}
		updated, err := time.Parse(time.RFC3339Nano, s.UpdatedAt)
		if err != nil {
			updated = time.Now()
		}
		t := state.Thread{ID: state.ID(), SessionID: s.ID, Title: title, Command: c.Agent.Command, AgentID: c.Agent.ID, AgentName: c.Agent.Name, Cwd: s.Cwd, AdditionalDirectories: slices.Clone(s.AdditionalDirectories), Created: time.Now(), Updated: updated}
		next = append(next, t)
		ids = append(ids, t.ID)
	}
	a.data.Threads = next
	if err := a.saveLocked(); err != nil {
		a.data.Threads = old
		return nil, err
	}
	return ids, nil
}

func (a *App) catalogSnapshotLocked() Catalog {
	c := a.catalog
	c.Sessions = slices.Clone(c.Sessions)
	for i := range c.Sessions {
		s := &c.Sessions[i]
		s.AdditionalDirectories = slices.Clone(s.AdditionalDirectories)
		for _, t := range a.data.Threads {
			if sameConnection(t, c.Agent) && t.SessionID == s.ID {
				s.ThreadID = t.ID
				break
			}
		}
	}
	return c
}
