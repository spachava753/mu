package piacp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spachava753/acp-sdk/acp"
)

// sessionFile is one session as Pi's files describe it. RPC mode has no listing
// command, so this reads Pi's session format directly, which ties the adapter to
// it. Files it can't make sense of are skipped: they may be from another version.
type sessionFile struct {
	id, cwd, title, path string
	updated              time.Time
}

// validID guards session IDs that reach Pi's command line and file names.
var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// readSessionFile reads the header and title. A session's name is its last
// session_info entry, so the whole file is read; lines are only decoded when they
// could matter. An unnamed session is titled by its first user message, as Pi's
// own picker shows it.
func readSessionFile(path string) (*sessionFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(f)
	first, err := reader.ReadBytes('\n')
	if err != nil && len(first) == 0 {
		return nil, err
	}
	var header struct {
		Type, ID, Cwd string
	}
	if err := json.Unmarshal(first, &header); err != nil {
		return nil, err
	}
	if header.Type != "session" || !validID.MatchString(header.ID) || !filepath.IsAbs(header.Cwd) {
		return nil, errors.New("not a session file")
	}
	result := &sessionFile{id: header.ID, cwd: header.Cwd, path: path, updated: info.ModTime()}
	var name, firstMessage string
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			text := string(line)
			switch {
			case strings.Contains(text, `"session_info"`):
				var entry struct {
					Type string  `json:"type"`
					Name *string `json:"name"`
				}
				if json.Unmarshal(line, &entry) == nil && entry.Type == "session_info" {
					name = ""
					if entry.Name != nil {
						name = *entry.Name
					}
				}
			case firstMessage == "" && strings.Contains(text, `"user"`):
				var entry struct {
					Type    string  `json:"type"`
					Message message `json:"message"`
				}
				if json.Unmarshal(line, &entry) == nil && entry.Type == "message" && entry.Message.Role == "user" {
					for _, b := range blocks(entry.Message.Content) {
						if b.Type == "text" && firstMessage == "" {
							firstMessage = b.Text
						}
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	result.title = name
	if result.title == "" {
		line, _, _ := strings.Cut(strings.TrimSpace(firstMessage), "\n")
		result.title = shorten(line, 80)
	}
	return result, nil
}

// scanSessions reads every session under root. By default Pi nests files in a
// folder per working directory; a custom session directory holds them directly.
func scanSessions(root string) []sessionFile {
	var found []sessionFile
	for _, pattern := range []string{"*.jsonl", "*/*.jsonl"} {
		paths, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, path := range paths {
			if s, err := readSessionFile(path); err == nil {
				found = append(found, *s)
			}
		}
	}
	return found
}

// sessionsRoot asks Pi where its sessions are, since that depends on a flag, an
// environment variable or a setting. get_state names the file a session would
// use even before it exists, and a process that receives no prompt writes nothing.
func (a *adapter) sessionsRoot(ctx context.Context) (string, error) {
	a.mu.Lock()
	root := a.root
	a.mu.Unlock()
	if root != "" {
		return root, nil
	}
	p, err := startPi(a.cfg.executable(), a.cfg.WorkDir, nil, func(string, []byte) {})
	if err != nil {
		return "", notFoundError(err)
	}
	defer p.close()
	ctx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	var state piState
	if err := p.call(ctx, "get_state", nil, &state); err != nil {
		return "", err
	}
	if state.SessionFile == "" {
		return "", errors.New("Pi did not say where it keeps sessions")
	}
	dir := filepath.Dir(state.SessionFile)
	if base := filepath.Base(dir); strings.HasPrefix(base, "--") && strings.HasSuffix(base, "--") {
		dir = filepath.Dir(dir)
	}
	a.mu.Lock()
	a.root = dir
	a.mu.Unlock()
	return dir, nil
}

func (a *adapter) findSession(ctx context.Context, id string) (*sessionFile, error) {
	root, err := a.sessionsRoot(ctx)
	if err != nil {
		return nil, err
	}
	for _, s := range scanSessions(root) {
		if s.id == id {
			return &s, nil
		}
	}
	return nil, nil
}

// SessionList lists Pi's sessions, plus sessions Mu created that have no file yet
// because nothing was ever sent in them. It returns one page.
func (a *adapter) SessionList(ctx context.Context, req *acp.ListSessionsRequest) (*acp.ListSessionsResponse, error) {
	root, err := a.sessionsRoot(ctx)
	if err != nil {
		return nil, err
	}
	found := scanSessions(root)
	known := map[string]bool{}
	for _, s := range found {
		known[s.id] = true
	}
	for _, unsent := range a.unsentSessions() {
		if known[unsent.id] {
			a.forgetUnsent(unsent.id) // Its first turn happened after the last check.
		} else {
			found = append(found, unsent)
		}
	}
	slices.SortStableFunc(found, func(x, y sessionFile) int { return y.updated.Compare(x.updated) })
	response := &acp.ListSessionsResponse{Sessions: []acp.SessionInfo{}}
	for _, s := range found {
		if req.Cwd != nil && *req.Cwd != s.cwd {
			continue
		}
		updated := s.updated.UTC().Format(time.RFC3339Nano)
		info := acp.SessionInfo{SessionID: acp.SessionId(s.id), Cwd: s.cwd, UpdatedAt: &updated}
		if s.title != "" {
			title := s.title
			info.Title = &title
		}
		response.Sessions = append(response.Sessions, info)
	}
	return response, nil
}

// Pi writes nothing for a session until its first turn, and reopening an unknown
// ID makes a new empty session. So a session that was never sent to and one that
// has since been deleted look alike. A marker for each session Mu created, removed
// after its first turn, tells them apart: the first reopens, the second is an
// error rather than a silent replacement.

type unsentMarker struct {
	Cwd string `json:"cwd"`
}

func (a *adapter) unsentPath(id string) string {
	return filepath.Join(a.cfg.StateDir, "unsent", id+".json")
}

func (a *adapter) markUnsent(id, cwd string) error {
	if a.cfg.StateDir == "" {
		return nil
	}
	data, err := json.Marshal(unsentMarker{Cwd: cwd})
	if err != nil {
		return err
	}
	path := a.unsentPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (a *adapter) isUnsent(id, cwd string) bool {
	if a.cfg.StateDir == "" {
		return false
	}
	data, err := os.ReadFile(a.unsentPath(id))
	if err != nil {
		return false
	}
	var marker unsentMarker
	return json.Unmarshal(data, &marker) == nil && marker.Cwd == cwd
}

func (a *adapter) forgetUnsent(id string) {
	if a.cfg.StateDir != "" {
		_ = os.Remove(a.unsentPath(id))
	}
}

func (a *adapter) unsentSessions() []sessionFile {
	if a.cfg.StateDir == "" {
		return nil
	}
	paths, _ := filepath.Glob(a.unsentPath("*"))
	var sessions []sessionFile
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var marker unsentMarker
		if json.Unmarshal(data, &marker) != nil || !filepath.IsAbs(marker.Cwd) {
			continue
		}
		sessions = append(sessions, sessionFile{id: strings.TrimSuffix(filepath.Base(path), ".json"), cwd: marker.Cwd, updated: info.ModTime()})
	}
	return sessions
}
