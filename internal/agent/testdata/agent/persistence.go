package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

// This store belongs to the fixture agent, outside Mu's data directory. Each
// test binary has its own directory; separate agent processes share that store.
type savedSession struct {
	ID                    string            `json:"sessionId"`
	Cwd                   string            `json:"cwd"`
	Title                 string            `json:"title"`
	UpdatedAt             string            `json:"updatedAt"`
	AdditionalDirectories []string          `json:"additionalDirectories,omitempty"`
	Updates               []json.RawMessage `json:"updates,omitempty"`
}

// Sessions open on this connection. Like real agents, one process serves
// several sessions; close and fork reject sessions that are not open here.
var open = map[string]*openSession{}

type openSession struct {
	*savedSession
	recording   bool
	pending     json.RawMessage
	waiting     string
	model, mode string
	enabled     bool
}

func storeDir() string {
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "fixture-sessions")
}
func sessionPath(id string) string { return filepath.Join(storeDir(), filepath.Base(id)+".json") }
func newSaved(cwd string) *savedSession {
	var bytes [16]byte
	_, _ = rand.Read(bytes[:])
	return &savedSession{ID: hex.EncodeToString(bytes[:]), Cwd: cwd, Title: "Fixture session", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
}
func save(s *savedSession) {
	_ = os.MkdirAll(storeDir(), 0700)
	b, _ := json.Marshal(s)
	f, err := os.CreateTemp(storeDir(), ".session-*")
	if err != nil {
		panic(err)
	}
	_, _ = f.Write(b)
	_ = f.Close()
	if err := os.Rename(f.Name(), sessionPath(s.ID)); err != nil {
		panic(err)
	}
}
func load(id string) (*savedSession, error) {
	b, err := os.ReadFile(sessionPath(id))
	if err != nil {
		return nil, err
	}
	var s savedSession
	err = json.Unmarshal(b, &s)
	return &s, err
}
func logCall(m message) {
	_ = os.MkdirAll(storeDir(), 0700)
	f, err := os.OpenFile(filepath.Join(storeDir(), "calls.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(m)
}
func recordNotification(v any) {
	b, _ := json.Marshal(v)
	var n struct {
		Method string
		Params struct {
			SessionID string
			Update    json.RawMessage
		}
	}
	_ = json.Unmarshal(b, &n)
	active := open[n.Params.SessionID]
	if n.Method != "session/update" || active == nil || !active.recording {
		return
	}
	var u struct {
		SessionUpdate string
		Title         string
	}
	_ = json.Unmarshal(n.Params.Update, &u)
	if u.SessionUpdate == "user_message_chunk" {
		return
	} // Prompt is stored exactly once below.
	if u.SessionUpdate == "session_info_update" && u.Title != "" {
		active.Title = u.Title
	}
	active.Updates = append(active.Updates, n.Params.Update)
	active.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	save(active.savedSession)
}
func savePrompt(active *savedSession, text string) {
	b, _ := json.Marshal(map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": text}})
	active.Updates = append(active.Updates, b)
	save(active)
}

// saveBlocks keeps a prompt with files in it, for replaying as sent.
func saveBlocks(active *savedSession, blocks []json.RawMessage) {
	for _, block := range blocks {
		b, _ := json.Marshal(map[string]any{"sessionUpdate": "user_message_chunk", "content": block})
		active.Updates = append(active.Updates, b)
	}
	save(active)
}
func replay(s *savedSession) {
	for _, u := range s.Updates {
		send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": s.ID, "update": u}})
	}
}
func listSaved(id json.RawMessage, cursor string) {
	if slices.Contains(os.Args, "fail-list") {
		reject(id)
		return
	}
	files, _ := filepath.Glob(filepath.Join(storeDir(), "*.json"))
	var all []*savedSession
	for _, path := range files {
		b, _ := os.ReadFile(path)
		var s savedSession
		if json.Unmarshal(b, &s) == nil {
			all = append(all, &s)
		}
	}
	offset, _ := strconv.Atoi(cursor)
	if offset < 0 || offset > len(all) {
		reject(id)
		return
	}
	end := min(offset+2, len(all))
	items := make([]map[string]any, 0, end-offset)
	for _, s := range all[offset:end] {
		items = append(items, map[string]any{"sessionId": s.ID, "cwd": s.Cwd, "title": s.Title, "updatedAt": s.UpdatedAt, "additionalDirectories": s.AdditionalDirectories})
	}
	result := map[string]any{"sessions": items}
	if end < len(all) {
		result["nextCursor"] = strconv.Itoa(end)
	}
	if slices.Contains(os.Args, "repeat-cursor") {
		result["nextCursor"] = "repeated"
	}
	reply(id, result)
}
func notOpen(id json.RawMessage, session string) {
	send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": fmt.Sprintf("session %q is not open", session)}})
}
func missing(id json.RawMessage) {
	send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": "session not found"}})
}
