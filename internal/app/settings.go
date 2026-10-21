package app

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spachava753/mu/internal/agent"
	"github.com/spachava753/mu/internal/state"
)

func (a *App) migrateSettings() {
	if a.data.SettingsVersion != 0 {
		return
	}
	a.data.SettingsVersion = 1
	// This is a command the user already configured, never an agent default.
	if a.data.LastCommand != "" && len(a.data.Agents) == 0 {
		if args, err := agent.ParseCommand(a.data.LastCommand); err == nil {
			config := state.AgentConfig{ID: state.ID(), Name: filepath.Base(args[0]), Command: a.data.LastCommand}
			a.data.Agents = append(a.data.Agents, config)
			a.data.DefaultAgentID = config.ID
		}
	}
	a.dirty = true
}

func (a *App) SaveAgent(config state.AgentConfig, makeDefault bool) (string, error) {
	config.Name, config.Command = strings.TrimSpace(config.Name), strings.TrimSpace(config.Command)
	if config.Name == "" {
		return "", errors.New("enter an agent name")
	}
	if _, err := agent.ParseCommand(config.Command); err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return "", errors.New("app is closing")
	}
	index := -1
	for i, existing := range a.data.Agents {
		if existing.ID == config.ID {
			index = i
		} else if strings.EqualFold(existing.Name, config.Name) {
			return "", errors.New("an agent with that name already exists")
		}
	}
	if config.ID != "" && index < 0 {
		return "", errors.New("agent configuration not found")
	}
	old, oldDefault := slices.Clone(a.data.Agents), a.data.DefaultAgentID
	if index < 0 {
		config.ID = state.ID()
		a.data.Agents = append(a.data.Agents, config)
	} else {
		a.data.Agents[index] = config
	}
	if makeDefault || a.data.DefaultAgentID == "" {
		a.data.DefaultAgentID = config.ID
	}
	if err := a.saveLocked(); err != nil {
		a.data.Agents, a.data.DefaultAgentID = old, oldDefault
		return "", err
	}
	return config.ID, nil
}

func (a *App) RemoveAgent(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app is closing")
	}
	index := slices.IndexFunc(a.data.Agents, func(c state.AgentConfig) bool { return c.ID == id })
	if index < 0 {
		return errors.New("agent configuration not found")
	}
	old, oldDefault := slices.Clone(a.data.Agents), a.data.DefaultAgentID
	a.data.Agents = slices.Delete(slices.Clone(a.data.Agents), index, index+1)
	if a.data.DefaultAgentID == id {
		a.data.DefaultAgentID = ""
		if len(a.data.Agents) > 0 {
			a.data.DefaultAgentID = a.data.Agents[0].ID
		}
	}
	if err := a.saveLocked(); err != nil {
		a.data.Agents, a.data.DefaultAgentID = old, oldDefault
		return err
	}
	return nil // Existing threads keep their own command snapshot.
}

func (a *App) SetDefaultAgent(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app is closing")
	}
	if !slices.ContainsFunc(a.data.Agents, func(c state.AgentConfig) bool { return c.ID == id }) {
		return errors.New("agent configuration not found")
	}
	old := a.data.DefaultAgentID
	a.data.DefaultAgentID = id
	if err := a.saveLocked(); err != nil {
		a.data.DefaultAgentID = old
		return err
	}
	return nil
}

func (a *App) SetSidebarCollapsed(collapsed bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errors.New("app is closing")
	}
	old := a.data.SidebarCollapsed
	a.data.SidebarCollapsed = collapsed
	if err := a.saveLocked(); err != nil {
		a.data.SidebarCollapsed = old
		return err
	}
	return nil
}

func (a *App) CreateWithAgent(agentID, cwd string) (string, error) {
	a.mu.Lock()
	var config state.AgentConfig
	for _, c := range a.data.Agents {
		if c.ID == agentID {
			config = c
			break
		}
	}
	a.mu.Unlock()
	if config.ID == "" {
		return "", errors.New("choose a configured agent in Settings")
	}
	return a.create(config.Command, cwd, config.ID, config.Name)
}
