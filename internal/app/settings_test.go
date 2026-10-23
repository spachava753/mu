package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spachava753/mu/internal/state"
)

func TestSavedAgents(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	first, err := a.SaveAgent(state.AgentConfig{Name: "First", Command: "agent-one --stdio"}, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.SaveAgent(state.AgentConfig{Name: "Second", Command: "agent-two --acp"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if a.Snapshot().DefaultAgentID != second {
		t.Fatal("default not selected")
	}
	thread, err := a.CreateWithAgent(second, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveAgent(state.AgentConfig{ID: second, Name: "Renamed", Command: "changed-agent"}, false); err != nil {
		t.Fatal(err)
	}
	v := findThread(t, a, thread)
	if v.Command != "agent-two --acp" || v.AgentName != "Second" {
		t.Fatal("editing an agent rewrote an existing thread")
	}
	if _, err := a.SaveAgent(state.AgentConfig{Name: "renamed", Command: "duplicate"}, false); err == nil {
		t.Fatal("duplicate name accepted")
	}
	if err := a.SetSidebarCollapsed(true); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveAgent(second); err != nil {
		t.Fatal(err)
	}
	if a.Snapshot().DefaultAgentID != first {
		t.Fatal("removed default did not fall back to another user configuration")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTest(t, dir)
	s := a.Snapshot()
	if len(s.Agents) != 1 || !s.SidebarCollapsed || s.DefaultAgentID != first {
		t.Fatal("settings did not survive restart", s)
	}
	if findThread(t, a, thread).Command != "agent-two --acp" {
		t.Fatal("removing configuration changed thread command")
	}
}

func TestLastCommandDoesNotCreateSavedAgent(t *testing.T) {
	dir := t.TempDir()
	store, _, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(state.Data{LastCommand: "my-agent --stdio"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	a := openTest(t, dir)
	s := a.Snapshot()
	if len(s.Agents) != 0 || s.DefaultAgentID != "" || s.LastCommand != "my-agent --stdio" {
		t.Fatal("last-used command became a saved agent", s)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTest(t, dir)
	if len(a.Snapshot().Agents) != 0 {
		t.Fatal("restart invented a saved agent")
	}
}

func TestSettingsSaveFailureRollsBack(t *testing.T) {
	dir := t.TempDir()
	a := openTest(t, dir)
	if err := os.Mkdir(filepath.Join(dir, "threads.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveAgent(state.AgentConfig{Name: "Test", Command: "test-agent"}, true); err == nil {
		t.Fatal("expected write failure")
	}
	if len(a.Snapshot().Agents) != 0 || a.Snapshot().DefaultAgentID != "" {
		t.Fatal("failed save changed configuration")
	}
	if err := a.SetSidebarCollapsed(true); err == nil {
		t.Fatal("expected write failure")
	}
	if a.Snapshot().SidebarCollapsed {
		t.Fatal("failed save changed sidebar preference")
	}
	if err := os.Remove(filepath.Join(dir, "threads.json")); err != nil {
		t.Fatal(err)
	}
}
