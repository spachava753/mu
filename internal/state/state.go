// Package state persists session references and preferences, never conversations.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/spachava753/acp-sdk/acp"
)

type Entry struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	ToolID    string `json:"toolId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	Title     string `json:"title,omitempty"`
	Status    string `json:"status,omitempty"`
	Details   string `json:"details,omitempty"`
	PlanID    string `json:"planId,omitempty"`
}

type Thread struct {
	ID                    string    `json:"id"`
	Title                 string    `json:"title"`
	Command               string    `json:"command"`
	AgentID               string    `json:"agentId,omitempty"`
	AgentName             string    `json:"agentName,omitempty"`
	ProjectID             string    `json:"projectId,omitempty"`
	Cwd                   string    `json:"cwd"`
	SessionID             string    `json:"sessionId,omitempty"`
	ParentID              string    `json:"parentId,omitempty"`
	Archived              bool      `json:"archived,omitempty"`
	Created               time.Time `json:"created"`
	Updated               time.Time `json:"updated"`
	AdditionalDirectories []string  `json:"additionalDirectories,omitempty"`
}

// Session is transient state reported by the agent. It is never included in Data.
type Session struct {
	Options        []acp.SessionConfigOption  `json:"options,omitempty"`
	Modes          *acp.SessionModeState      `json:"modes,omitempty"`
	Commands       []acp.AvailableCommand     `json:"commands,omitempty"`
	Usage          *Usage                     `json:"usage,omitempty"`
	TurnUsage      *acp.Usage                 `json:"turnUsage,omitempty"`
	StopReason     acp.StopReason             `json:"stopReason,omitempty"`
	AgentUpdatedAt string                     `json:"agentUpdatedAt,omitempty"`
	Metadata       map[string]json.RawMessage `json:"metadata,omitempty"`
}

// Usage is the agent's latest usage_update: context tokens and session cost.
type Usage struct {
	Used uint64    `json:"used"`
	Size uint64    `json:"size"`
	Cost *acp.Cost `json:"cost,omitempty"`
}

// Clone isolates nested SDK unions, maps and slices from background updates.
func (s Session) Clone() Session {
	b, _ := json.Marshal(s)
	var copy Session
	_ = json.Unmarshal(b, &copy)
	return copy
}

type AgentConfig struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
}

// Project is a named folder that all of its threads work in.
type Project struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Dir  string `json:"dir"`
}

type Data struct {
	Agents           []AgentConfig `json:"agents,omitempty"`
	Projects         []Project     `json:"projects,omitempty"`
	DefaultAgentID   string        `json:"defaultAgentId,omitempty"`
	SidebarCollapsed bool          `json:"sidebarCollapsed,omitempty"`
	LastCommand      string        `json:"lastCommand,omitempty"`
	WorkingDirectory string        `json:"workingDirectory,omitempty"`
	Threads          []Thread      `json:"threads"`
}

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

func DefaultDir() (string, error) {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, "mu"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "mu"), nil
}

// DefaultWorkingDir is where new threads get their folders until the user
// chooses another general working directory in Settings.
func DefaultWorkingDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "mu"), nil
}

// Store owns an exclusive lock for its lifetime. A second app cannot lose another's edits.
type Store struct {
	dir  string
	lock *flock.Flock
}

func Open(dir string) (*Store, Data, error) {
	var data Data
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, data, err
	}
	lock := flock.New(filepath.Join(dir, "mu.lock"))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, data, err
	}
	if !ok {
		return nil, data, fmt.Errorf("Mu is already using %s", dir)
	}
	s := &Store{dir: dir, lock: lock}
	b, err := os.ReadFile(filepath.Join(dir, "threads.json"))
	if errors.Is(err, os.ErrNotExist) {
		return s, Data{}, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &data)
	}
	if err == nil {
		err = validateThreads(data.Threads)
	}
	if err != nil {
		_ = s.Close()
		return nil, Data{}, fmt.Errorf("read %s/threads.json (left untouched): %w", dir, err)
	}
	return s, data, nil
}

func validateThreads(threads []Thread) error {
	seen := map[string]bool{}
	references := map[[3]string]bool{}
	for _, t := range threads {
		if t.ID == "" || seen[t.ID] {
			return errors.New("invalid or duplicate thread ID")
		}
		seen[t.ID] = true
		if t.SessionID != "" {
			key := [3]string{t.AgentID, t.Command, t.SessionID}
			if references[key] {
				return errors.New("duplicate session reference for an agent connection")
			}
			references[key] = true
		}
	}
	return nil
}

// Save replaces the file atomically; incomplete writes never replace the last good registry.
func (s *Store) Save(data Data) error {
	if err := validateThreads(data.Threads); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".threads-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), filepath.Join(s.dir, "threads.json")); err != nil {
		return err
	}
	// Best effort directory sync; some platforms don't support syncing directories.
	if d, err := os.Open(s.dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func (s *Store) Close() error { return s.lock.Unlock() }
