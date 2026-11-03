package app

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/state"
)

// connection is one agent process shared by every thread whose sessions use
// the same agent configuration: saved agent ID and command snapshot. ACP
// multiplexes sessions on a connection, so notifications and permission
// requests are routed to threads by session ID. Discovery lists sessions on it
// too. The process starts in the general working directory. A saved agent's
// process keeps running while idle (see keepLocked); others are terminated
// once nothing uses them.
type connection struct {
	key     [2]string
	process *agent.Process
	ready   chan struct{} // closed once startup has finished
	err     error         // startup error, valid after ready
	cancel  context.CancelFunc
	users   int
	routes  map[string]route
	// Notifications for a session/new request arrive before its response
	// reveals the session ID. They are held only while such a request is
	// outstanding, and claimed by the thread that receives the ID.
	creating int
	unrouted map[string][]acp.SessionNotification
}

// route delivers one session's traffic to a thread's runtime. The epoch
// rejects traffic queued before the thread reconnected or detached.
type route struct {
	thread string
	r      *runtime
	epoch  uint64
}

func (c *connection) started() bool {
	select {
	case <-c.ready:
		return true
	default:
		return false
	}
}

func (c *connection) alive() bool {
	return c != nil && c.started() && c.err == nil && c.process.Alive()
}

// acquire returns a started connection for an agent configuration, starting
// its process if needed. Every successful call must be released.
func (a *App) acquire(ctx context.Context, agentID, command string) (*connection, error) {
	key := [2]string{agentID, command}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, errAppClosing
	}
	c := a.connections[key]
	if c != nil && c.started() && !c.alive() {
		delete(a.connections, key) // Threads still holding it release it themselves.
		c = nil
	}
	if c == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		c = &connection{key: key, ready: make(chan struct{}), cancel: cancel, routes: map[string]route{}, unrouted: map[string][]acp.SessionNotification{}}
		a.connections[key] = c
		a.wg.Add(1)
		go a.start(ctx, c, command, a.workingDirectoryLocked())
	}
	c.users++
	a.mu.Unlock()
	select {
	case <-c.ready:
	case <-ctx.Done():
		a.release(c)
		return nil, ctx.Err()
	}
	if c.err != nil {
		a.release(c)
		return nil, c.err
	}
	return c, nil
}

// start runs independently of any one thread, so a thread that stops waiting
// does not fail startup for others. It is cancelled once nobody waits, unless
// the connection is kept.
func (a *App) start(ctx context.Context, c *connection, command, cwd string) {
	defer a.wg.Done()
	defer c.cancel()
	var p *agent.Process
	err := os.MkdirAll(cwd, 0o755)
	if err != nil {
		err = fmt.Errorf("working directory: %w", err)
	} else {
		p, err = agent.Start(ctx, command, cwd, agent.Hooks{
			Update: func(n acp.SessionNotification) { a.route(c, n) },
			Permission: func(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
				return a.routePermission(ctx, c, req)
			},
		})
	}
	a.mu.Lock()
	unused := c.users == 0 && !a.keepLocked(c.key)
	if err == nil && unused {
		err = context.Canceled
	}
	c.process, c.err = p, err
	if err != nil && a.connections[c.key] == c {
		delete(a.connections, c.key)
	}
	close(c.ready)
	a.mu.Unlock()
	if unused && p != nil {
		p.Close()
	}
}

// release drops one user and terminates the process once it has none,
// unless the connection is kept.
func (a *App) release(c *connection) {
	a.mu.Lock()
	p := a.releaseLocked(c)
	a.mu.Unlock()
	if p != nil {
		p.Close()
	}
}

// releaseLocked returns a process the caller must close after unlocking.
func (a *App) releaseLocked(c *connection) *agent.Process {
	c.users--
	if c.users > 0 {
		return nil
	}
	if a.connections[c.key] == c && (!c.started() || c.alive()) && a.keepLocked(c.key) {
		return nil
	}
	if a.connections[c.key] == c {
		delete(a.connections, c.key)
	}
	if !c.started() {
		c.cancel()
		return nil
	}
	return c.process
}

// keepLocked reports whether an idle connection keeps running. A saved agent's
// process stays up until the app closes or the saved configuration changes, so
// opening threads, closing sessions and discovery do not restart it. One-off
// commands and older command snapshots stop with their last user.
func (a *App) keepLocked(key [2]string) bool {
	return !a.closed && slices.ContainsFunc(a.data.Agents, func(c state.AgentConfig) bool {
		return c.ID == key[0] && c.Command == key[1]
	})
}

// stopIdleLocked terminates idle connections that are no longer kept. The
// processes are closed off the app mutex, tracked by the wait group.
func (a *App) stopIdleLocked() {
	for key, c := range a.connections {
		if c.users > 0 || a.keepLocked(key) {
			continue
		}
		delete(a.connections, key)
		if !c.started() {
			c.cancel()
			continue
		}
		p := c.process
		a.wg.Add(1)
		go func() { defer a.wg.Done(); p.Close() }()
	}
}

func (a *App) route(c *connection, n acp.SessionNotification) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := string(n.SessionId)
	rt, ok := c.routes[id]
	if !ok {
		if c.creating > 0 {
			c.unrouted[id] = append(c.unrouted[id], n)
		}
		return
	}
	a.receiveLocked(rt.thread, rt.r, rt.epoch, n)
}

func (a *App) routePermission(ctx context.Context, c *connection, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	a.mu.Lock()
	rt, ok := c.routes[string(req.SessionId)]
	a.mu.Unlock()
	if !ok {
		return agent.CancelledPermission(), nil
	}
	return a.permission(ctx, rt.thread, rt.r, rt.epoch, req)
}
