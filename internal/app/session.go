package app

import (
	"context"
	"errors"
	"slices"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/state"
)

func (a *App) configuringLocked(id string) bool {
	return a.runtimes[id] != nil && (a.runtimes[id].configuring || a.runtimes[id].closing != nil)
}

// Connect creates a session or loads its authoritative history without a prompt.
func (a *App) Connect(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app is closing")
	}
	t := a.threadLocked(id)
	if t == nil {
		return errors.New("thread not found")
	}
	if t.Archived {
		return errors.New("restore the thread before connecting")
	}
	r := a.runtimeLocked(id)
	if r.configuring || t.Running {
		return nil
	}
	if r.process != nil && r.process.Alive() && r.session != "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	r.configuring, r.configCancel, r.sessionError, r.err = true, cancel, "", ""
	config := *t.Thread
	a.notifyLocked()
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer cancel()
		_, err := a.connect(ctx, config, r)
		a.mu.Lock()
		defer a.mu.Unlock()
		r.configuring, r.configCancel = false, nil
		if err != nil {
			r.sessionError = err.Error()
		}
		a.notifyLocked()
	}()
	return nil
}

// Reload replaces the view from a fresh agent replay, including external edits.
func (a *App) Reload(id string) error {
	a.mu.Lock()
	t := a.threadLocked(id)
	if a.closed || t == nil {
		a.mu.Unlock()
		return errors.New("thread unavailable")
	}
	if t.Running || a.configuringLocked(id) {
		a.mu.Unlock()
		return errors.New("wait for the current session request")
	}
	r := a.runtimeLocked(id)
	a.detachLocked(r)
	r.err, r.sessionError = "", ""
	a.mu.Unlock()
	return a.Connect(id)
}

// SetOption validates against the latest advertised choices. The agent's complete
// response replaces the option list, including any dependent option changes.
func (a *App) SetOption(id, configID string, value any) error {
	return a.changeSession(id, func(s state.Session, session acp.SessionId) (func(context.Context, *agent.Process) (func(*state.Session), error), error) {
		var request acp.SetSessionConfigOptionRequest
		for _, option := range s.Options {
			if o := option.Select; o != nil && string(o.Id) == configID {
				v, ok := value.(string)
				if !ok {
					return nil, errors.New("this option requires a selection")
				}
				if !validSelect(o, v) {
					return nil, errors.New("that value is no longer offered by the agent")
				}
				request.ValueId = &acp.SetSessionConfigOptionValueId{SessionId: session, ConfigId: o.Id, Value: acp.SessionConfigValueId(v)}
			}
			if o := option.Boolean; o != nil && string(o.Id) == configID {
				v, ok := value.(bool)
				if !ok {
					return nil, errors.New("this option requires a boolean")
				}
				request.Boolean = &acp.SetSessionConfigOptionBoolean{SessionId: session, ConfigId: o.Id, Type: "boolean", Value: v}
			}
		}
		if request.ValueId == nil && request.Boolean == nil {
			return nil, errors.New("session option not found")
		}
		return func(ctx context.Context, p *agent.Process) (func(*state.Session), error) {
			response, err := p.Conn.SetSessionConfigOption(ctx, request)
			return func(s *state.Session) {
				s.Options = response.ConfigOptions
				storeMeta(s, "configuration", response.Meta)
			}, err
		}, nil
	})
}

func validSelect(o *acp.SessionConfigOptionSelect, value string) bool {
	if o.Options.Ungrouped != nil {
		for _, v := range *o.Options.Ungrouped {
			if string(v.Value) == value {
				return true
			}
		}
	}
	if o.Options.Grouped != nil {
		for _, g := range *o.Options.Grouped {
			for _, v := range g.Options {
				if string(v.Value) == value {
					return true
				}
			}
		}
	}
	return false
}

func (a *App) SetMode(id, mode string) error {
	return a.changeSession(id, func(s state.Session, session acp.SessionId) (func(context.Context, *agent.Process) (func(*state.Session), error), error) {
		if len(s.Options) > 0 {
			return nil, errors.New("use the agent's configuration options instead of legacy modes")
		}
		if s.Modes == nil || !slices.ContainsFunc(s.Modes.AvailableModes, func(m acp.SessionMode) bool { return string(m.Id) == mode }) {
			return nil, errors.New("session mode not found")
		}
		return func(ctx context.Context, p *agent.Process) (func(*state.Session), error) {
			_, err := p.Conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: session, ModeId: acp.SessionModeId(mode)})
			return func(s *state.Session) {
				if s.Modes != nil {
					s.Modes.CurrentModeId = acp.SessionModeId(mode)
				}
			}, err
		}, nil
	})
}

type sessionChange func(state.Session, acp.SessionId) (func(context.Context, *agent.Process) (func(*state.Session), error), error)

func (a *App) changeSession(id string, prepare sessionChange) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app is closing")
	}
	t := a.threadLocked(id)
	if t == nil {
		return errors.New("thread not found")
	}
	if t.Archived {
		return errors.New("restore the thread before changing options")
	}
	r := a.runtimeLocked(id)
	if r.configuring {
		return errors.New("a session settings request is already running")
	}
	if r.process == nil || !r.process.Alive() || r.session == "" {
		return errors.New("connect to load current session options first")
	}
	request, err := prepare(t.Session, acp.SessionId(r.session))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	r.configuring, r.configCancel, r.sessionError = true, cancel, ""
	p := r.process
	a.wg.Add(1)
	a.notifyLocked()
	go func() {
		defer a.wg.Done()
		defer cancel()
		apply, err := request(ctx, p)
		a.mu.Lock()
		defer a.mu.Unlock()
		r.configuring, r.configCancel = false, nil
		if err != nil {
			r.sessionError = p.Error(err).Error()
		} else if current := a.threadLocked(id); current != nil {
			apply(&current.Session)
			_ = a.saveLocked()
		}
		a.notifyLocked()
	}()
	return nil
}
