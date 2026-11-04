package app

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/spachava753/acp-sdk/acp"
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
	retired bool // taken out of use and stopped; see retireLocked
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
	var c *connection
	for {
		if a.closed {
			a.mu.Unlock()
			return nil, errAppClosing
		}
		c = a.connections[key]
		if c != nil && c.started() && !c.alive() {
			// The transport ended, but the process may still be exiting. Stop
			// it now; threads still holding it release it themselves.
			a.retireLocked(c)
			c = nil
		}
		if c != nil {
			break
		}
		// A replacement never overlaps the old process's shutdown, during
		// which it may still be saving sessions or holding their store.
		stopping := a.stopping[key]
		if stopping == nil {
			startCtx, cancel := context.WithTimeout(context.Background(), startTimeout)
			c = &connection{key: key, ready: make(chan struct{}), cancel: cancel, routes: map[string]route{}, unrouted: map[string][]acp.SessionNotification{}}
			a.connections[key] = c
			a.wg.Add(1)
			go a.start(startCtx, c, command, a.workingDirectoryLocked())
			break
		}
		a.mu.Unlock()
		select {
		case <-stopping:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		a.mu.Lock()
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
	// Retirement is final, even if the configuration is saved again meanwhile.
	unused := c.retired || (c.users == 0 && !a.keepLocked(c.key))
	if err == nil && unused {
		err = context.Canceled
	}
	c.process, c.err = p, err
	if err != nil && a.connections[c.key] == c {
		delete(a.connections, c.key)
	}
	if unused && p != nil {
		a.stopLocked(c.key, p.Close)
	}
	close(c.ready)
	a.mu.Unlock()
}

// release drops one user and terminates the process once it has none,
// unless the connection is kept.
func (a *App) release(c *connection) {
	a.mu.Lock()
	a.releaseLocked(c)
	a.mu.Unlock()
}

func (a *App) releaseLocked(c *connection) {
	c.users--
	if c.users > 0 {
		return
	}
	if a.connections[c.key] == c && (!c.started() || c.alive()) && a.keepLocked(c.key) {
		return
	}
	a.retireLocked(c)
}

// retireLocked takes a connection out of use and ends its process, once:
// it cancels an unfinished startup or closes the process, behind the
// configuration's stopping barrier. Threads still holding it release it
// without stopping it again.
func (a *App) retireLocked(c *connection) {
	if a.connections[c.key] == c {
		delete(a.connections, c.key)
	}
	if c.retired {
		return
	}
	c.retired = true
	switch {
	case !c.started():
		// agent.Start closes a process whose startup is cancelled before it
		// returns, so the startup worker's end is this process's exit.
		c.cancel()
		a.stopLocked(c.key, func() { <-c.ready })
	case c.process != nil:
		a.stopLocked(c.key, c.process.Close)
	}
}

// stopLocked runs stop, which ends a process, off the app mutex, tracked by
// the wait group. Until it and any earlier stop for the configuration have
// finished, acquire does not start another process for that configuration.
func (a *App) stopLocked(key [2]string, stop func()) {
	done, previous := make(chan struct{}), a.stopping[key]
	a.stopping[key] = done
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		stop()
		if previous != nil {
			<-previous
		}
		a.mu.Lock()
		if a.stopping[key] == done {
			delete(a.stopping, key)
		}
		close(done)
		a.mu.Unlock()
	}()
}

// awaitCreation runs a request that creates an agent session, such as
// session/new or session/fork, on a started connection. Only its response
// names the session, so the request is never abandoned: if Mu stops waiting
// after timeout, the request keeps its own reference to the connection, and a
// session it creates late is closed. Only closing the app cancels it; a
// request still outstanding a deadline later, such as a write blocked on an
// agent that stopped reading, then closes the process.
func awaitCreation[T any](a *App, c *connection, timeout time.Duration, request func(context.Context) (T, error), created func(T) acp.SessionId) (T, error) {
	a.mu.Lock()
	c.users++ // The request's own reference, released when it ends.
	deadline := a.agentDeadline
	a.mu.Unlock()
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		finished := a.unblockAtShutdown(c)
		value, err := request(a.ctx)
		finished()
		done <- outcome{value, err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case o := <-done:
		a.release(c)
		return o.value, o.err
	case <-timer.C:
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer a.release(c)
		o := <-done
		if o.err != nil || created(o.value) == "" {
			return
		}
		session := string(created(o.value))
		// Coordinate with loads of the same session, as a detached thread's
		// close does: a thread that imported and opened it meanwhile keeps it,
		// and a close already in progress, such as that thread's, suffices.
		// Otherwise a load waits for this close.
		identity := [3]string{c.key[0], c.key[1], session}
		a.mu.Lock()
		_, claimed := c.routes[session]
		_, alreadyClosing := a.closingSessions[identity]
		if claimed || alreadyClosing {
			a.mu.Unlock()
			return
		}
		closing := make(chan struct{})
		a.closingSessions[identity] = closing
		a.mu.Unlock()
		err := c.process.CloseSession(session, deadline)
		a.mu.Lock()
		if a.closingSessions[identity] == closing {
			delete(a.closingSessions, identity)
		}
		close(closing)
		if err != nil {
			a.shutdownErrors = append(a.shutdownErrors, fmt.Errorf("the agent created session %s after Mu stopped waiting, and closing it failed: %w", session, err))
		}
		a.notifyLocked()
		a.mu.Unlock()
	}()
	var zero T
	return zero, fmt.Errorf("the agent did not respond within %s; a session it creates later will be closed", timeout)
}

// awaitSessionClosedLocked waits, releasing the mutex meanwhile, until no
// close of the session identity is outstanding.
func (a *App) awaitSessionClosedLocked(ctx context.Context, identity [3]string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for barrier := a.closingSessions[identity]; barrier != nil; barrier = a.closingSessions[identity] {
		a.mu.Unlock()
		select {
		case <-barrier:
		case <-ctx.Done():
			a.mu.Lock()
			return ctx.Err()
		}
		a.mu.Lock()
	}
	return nil
}

// unblockAtShutdown guards a request under the app's context. A context cannot
// interrupt a blocked pipe write, so if the request is still outstanding an
// agent deadline after the app starts closing, its process is closed, which
// lets shutdown join the request. Call the returned function when it returns.
func (a *App) unblockAtShutdown(c *connection) (finished func()) {
	done := make(chan struct{})
	a.mu.Lock()
	deadline := a.agentDeadline
	a.mu.Unlock()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		select {
		case <-done:
		case <-a.ctx.Done():
			timer := time.NewTimer(deadline)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				c.process.Close()
			}
		}
	}()
	return func() { close(done) }
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
		a.retireLocked(c)
	}
}

func (a *App) route(c *connection, n acp.SessionNotification) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := string(n.SessionID)
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
	rt, ok := c.routes[string(req.SessionID)]
	a.mu.Unlock()
	if !ok {
		return agent.CancelledPermission(), nil
	}
	return a.permission(ctx, rt.thread, rt.r, rt.epoch, req)
}
