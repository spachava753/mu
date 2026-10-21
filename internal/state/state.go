// Package state holds Mu's local thread history. Agent-owned sessions remain with the agent.
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
)

type Entry struct {
	ID         string `json:"id"`
	Role       string `json:"role"`
	Text       string `json:"text"`
	ToolID     string `json:"toolId,omitempty"`
	TerminalID string `json:"terminalId,omitempty"`
	Title      string `json:"title,omitempty"`
	Status     string `json:"status,omitempty"`
}

type Thread struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Command   string    `json:"command"`
	Cwd       string    `json:"cwd"`
	SessionID string    `json:"sessionId,omitempty"`
	ParentID  string    `json:"parentId,omitempty"`
	Archived  bool      `json:"archived,omitempty"`
	Running   bool      `json:"running,omitempty"`
	Notice    string    `json:"notice,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	Entries   []Entry   `json:"entries"`
}

type Data struct {
	Version     int      `json:"version"`
	LastCommand string   `json:"lastCommand,omitempty"`
	LastCwd     string   `json:"lastCwd,omitempty"`
	Threads     []Thread `json:"threads"`
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
		return s, Data{Version: 1}, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &data)
	}
	if err == nil && data.Version != 1 {
		err = fmt.Errorf("unsupported data version %d", data.Version)
	}
	if err == nil {
		seen := map[string]bool{}
		for _, t := range data.Threads {
			if t.ID == "" || seen[t.ID] {
				err = errors.New("invalid or duplicate thread ID")
				break
			}
			seen[t.ID] = true
		}
	}
	if err != nil {
		_ = s.Close()
		return nil, Data{}, fmt.Errorf("read %s/threads.json (left untouched): %w", dir, err)
	}
	return s, data, nil
}

// Save replaces the file atomically; incomplete writes never replace the last good history.
func (s *Store) Save(data Data) error {
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
