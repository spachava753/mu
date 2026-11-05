package app

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/state"
)

// BuiltinState is what's known about a built-in agent's own installation.
type BuiltinState string

const (
	BuiltinUnknown   BuiltinState = ""           // not checked yet
	BuiltinReady     BuiltinState = "ready"      // usable
	BuiltinNotFound  BuiltinState = "not_found"  // not installed, or not where it's said to be
	BuiltinSignedOut BuiltinState = "signed_out" // starts, but has no model to use
	BuiltinOutdated  BuiltinState = "outdated"   // older than the adapter supports
	BuiltinBroken    BuiltinState = "broken"     // there, but couldn't be asked
)

// BuiltinStatus is the result of checking a built-in agent.
type BuiltinStatus struct {
	State   BuiltinState
	Version string
	Path    string
	Detail  string // what went wrong, for BuiltinBroken
}

// BuiltinFact is a line of explanation about a built-in agent, for Settings.
type BuiltinFact struct{ Label, Text string }

// Builtin describes an agent whose ACP adapter runs inside Mu. Everything specific
// to that agent is supplied here, so the app and the view treat them all alike.
type Builtin struct {
	Name  string // what its command names, such as "pi"
	Title string // what people call it, such as "Pi"
	Mark  string // a character standing in for a logo
	// Site is where to get it, shown when it isn't found or needs updating.
	Site, SiteLabel string
	MinimumVersion  string
	// SignInHelp and SignInCommand say how to sign in, which happens outside Mu.
	SignInHelp, SignInCommand string
	Facts                     []BuiltinFact
	// Detect checks an executable, empty meaning the default. It blocks, and is
	// only called off the UI thread.
	Detect func(ctx context.Context, executable, cwd string) BuiltinStatus
	// Serve runs the adapter. executable says which installation to use, and is
	// asked each time the adapter starts one, so a change applies to new sessions.
	Serve func(ctx context.Context, link agent.Link, executable func() string) error
}

// ID is the agent ID a built-in has where a saved agent would have its own.
func (b Builtin) ID() string { return builtinIDPrefix + b.Name }

func (b Builtin) config() state.AgentConfig {
	return state.AgentConfig{ID: b.ID(), Name: b.Title, Command: agent.BuiltinCommand(b.Name)}
}

const builtinIDPrefix = "builtin:"

// BuiltinView is a built-in agent as the UI sees it.
type BuiltinView struct {
	Builtin
	Status     BuiltinStatus
	Executable string // the path the user set, if any
	Checking   bool
}

// Offered reports whether it can be chosen as an agent. Until it has been checked
// it's assumed to be there, so that the usual case doesn't flicker.
func (v BuiltinView) Offered() bool { return v.Status.State != BuiltinNotFound }

// Usable reports whether a new thread could start with it.
func (v BuiltinView) Usable() bool {
	return v.Status.State == BuiltinReady || v.Status.State == BuiltinSignedOut
}

// builtinRuntime is the live state of one built-in.
type builtinRuntime struct {
	status   BuiltinStatus
	checking bool
	checked  time.Time
	wake     chan struct{}
}

// Timing of checks: how long one may take, and how recent one must be for opening
// a screen not to bother with another.
const (
	builtinCheckTimeout = 30 * time.Second
	builtinCheckFresh   = 30 * time.Second
)

func (a *App) startBuiltins() {
	for _, b := range a.builtins {
		r := &builtinRuntime{checking: true, wake: make(chan struct{}, 1)}
		a.builtinRuntimes[b.Name] = r
		a.wg.Add(1)
		go a.watchBuiltin(b, r)
		r.wake <- struct{}{}
	}
}

// watchBuiltin runs checks one at a time as they're asked for. Requests made during
// a check coalesce into one more.
func (a *App) watchBuiltin(b Builtin, r *builtinRuntime) {
	defer a.wg.Done()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-r.wake:
		}
		a.mu.Lock()
		executable, cwd := a.builtinExecutableLocked(b.Name), a.workingDirectoryLocked()
		a.mu.Unlock()
		status := a.detect(b, executable, cwd)
		a.mu.Lock()
		if a.ctx.Err() == nil {
			r.status, r.checked = status, time.Now()
			r.checking = len(r.wake) > 0
			a.notifyLocked()
		}
		a.mu.Unlock()
	}
}

func (a *App) detect(b Builtin, executable, cwd string) BuiltinStatus {
	ctx, cancel := context.WithTimeout(a.ctx, builtinCheckTimeout)
	defer cancel()
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return BuiltinStatus{State: BuiltinBroken, Detail: "working directory: " + err.Error()}
	}
	return b.Detect(ctx, executable, cwd)
}

func (a *App) builtinNamed(name string) (Builtin, bool) {
	i := slices.IndexFunc(a.builtins, func(b Builtin) bool { return b.Name == name })
	if i < 0 {
		return Builtin{}, false
	}
	return a.builtins[i], true
}

func (a *App) builtinExecutableLocked(name string) string {
	return a.data.Builtins[name].Executable
}

// requestCheckLocked asks for a check of a built-in. It never waits for one.
func (a *App) requestCheckLocked(name string) {
	r := a.builtinRuntimes[name]
	if r == nil {
		return
	}
	r.checking = true
	select {
	case r.wake <- struct{}{}:
	default:
	}
	a.notifyLocked()
}

// CheckBuiltin saves where a built-in's executable is, if that changed, and checks
// it again. It returns at once; the result arrives in a later snapshot. A session
// already open keeps the installation it started with.
func (a *App) CheckBuiltin(name, executable string) error {
	executable = strings.TrimSpace(executable)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errAppClosing
	}
	if _, ok := a.builtinNamed(name); !ok {
		return errors.New("no such built-in agent")
	}
	if old := a.builtinExecutableLocked(name); old != executable {
		previous := a.data.Builtins
		next := make(map[string]state.BuiltinSettings, len(previous)+1)
		for k, v := range previous {
			next[k] = v
		}
		if executable == "" {
			delete(next, name)
		} else {
			next[name] = state.BuiltinSettings{Executable: executable}
		}
		a.data.Builtins = next
		if err := a.saveLocked(); err != nil {
			a.data.Builtins = previous
			return err
		}
	}
	a.requestCheckLocked(name)
	return nil
}

// RefreshBuiltins checks built-ins again unless that happened recently, for screens
// that show their state, where someone may have just installed one.
func (a *App) RefreshBuiltins() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	for _, b := range a.builtins {
		if r := a.builtinRuntimes[b.Name]; !r.checking && time.Since(r.checked) > builtinCheckFresh {
			a.requestCheckLocked(b.Name)
		}
	}
}

func (a *App) builtinViewsLocked() []BuiltinView {
	if len(a.builtins) == 0 {
		return nil
	}
	views := make([]BuiltinView, 0, len(a.builtins))
	for _, b := range a.builtins {
		r := a.builtinRuntimes[b.Name]
		views = append(views, BuiltinView{Builtin: b, Status: r.status, Executable: a.builtinExecutableLocked(b.Name), Checking: r.checking})
	}
	return views
}

// agentConfigLocked finds a saved or built-in agent by ID.
func (a *App) agentConfigLocked(id string) (state.AgentConfig, bool) {
	if id == "" {
		return state.AgentConfig{}, false
	}
	if i := slices.IndexFunc(a.data.Agents, func(c state.AgentConfig) bool { return c.ID == id }); i >= 0 {
		return a.data.Agents[i], true
	}
	if name, ok := strings.CutPrefix(id, builtinIDPrefix); ok {
		if b, ok := a.builtinNamed(name); ok {
			return b.config(), true
		}
	}
	return state.AgentConfig{}, false
}

func (a *App) builtinOfferedLocked(b Builtin) bool {
	return a.builtinRuntimes[b.Name].status.State != BuiltinNotFound
}

// defaultAgentLocked is the agent selected for new threads: the one chosen, if it
// can be used, otherwise a saved agent, otherwise a built-in that's available. So a
// built-in becomes the default only when nothing else is, and a chosen built-in that
// goes missing doesn't lose the choice for when it returns.
func (a *App) defaultAgentLocked() string {
	id := a.data.DefaultAgentID
	if name, ok := strings.CutPrefix(id, builtinIDPrefix); ok {
		if b, known := a.builtinNamed(name); known && a.builtinOfferedLocked(b) {
			return id
		}
	} else if _, ok := a.agentConfigLocked(id); ok {
		return id
	}
	if len(a.data.Agents) > 0 {
		return a.data.Agents[0].ID
	}
	for _, b := range a.builtins {
		if a.builtinOfferedLocked(b) {
			return b.ID()
		}
	}
	return ""
}

// serveBuiltin starts a command naming a built-in inside Mu.
func (a *App) serveBuiltin(ctx context.Context, name, cwd string, hooks agent.Hooks) (*agent.Process, error) {
	b, ok := a.builtinNamed(name)
	if !ok {
		return nil, errors.New("Mu has no built-in agent named " + name)
	}
	server := func(ctx context.Context, link agent.Link) error {
		return b.Serve(ctx, link, func() string {
			a.mu.Lock()
			defer a.mu.Unlock()
			return a.builtinExecutableLocked(name)
		})
	}
	return agent.Serve(ctx, server, cwd, hooks)
}
