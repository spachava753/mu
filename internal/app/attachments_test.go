package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spachava753/acp-sdk/acp"
	"github.com/spachava753/mu/internal/media"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

var (
	testPNG = media.Pasted("a.png", []byte("\x89PNG\r\n\x1a\n"))
	testPDF = &media.File{Name: "b.pdf", Path: "/tmp/b.pdf", MimeType: "application/pdf", Size: 3, Data: []byte("%PD")}
)

// describeParts is a message's parts in order, compactly.
func describeParts(parts []state.Part) string {
	var out []string
	for _, part := range parts {
		if part.File != nil {
			out = append(out, "file:"+part.File.Name)
		} else {
			out = append(out, "text:"+part.Text)
		}
	}
	return strings.Join(out, " | ")
}

// lastPrompt is the blocks of the last prompt the fixture agent received.
func lastPrompt(t *testing.T, command string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(command), "fixture-sessions", "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var prompt []map[string]any
	for line := range strings.Lines(string(data)) {
		var call struct {
			Method string `json:"method"`
			Params struct {
				Prompt []map[string]any `json:"prompt"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(line), &call) == nil && call.Method == "session/prompt" {
			prompt = call.Params.Prompt
		}
	}
	return prompt
}

func TestFilesKeepTheirPlaceInAPrompt(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command)
	parts := []state.Part{{Text: "compare "}, {File: testPNG}, {Text: " with "}, {File: testPDF}, {Text: " please"}}
	if err := a.SendParts(id, parts); err != nil {
		t.Fatal(err)
	}
	v := await(t, a, id, func(v ThreadView) bool { return !v.Running && !v.Configuring })
	if v.Error != "" {
		t.Fatal(v.Error)
	}

	// What reaches the agent is in the order written.
	prompt := lastPrompt(t, command)
	var got []string
	for _, block := range prompt {
		got = append(got, fmt.Sprint(block["type"]))
	}
	if want := "text image text resource text"; strings.Join(got, " ") != want {
		t.Fatalf("blocks %v, want %s", got, want)
	}
	if prompt[0]["text"] != "compare " || prompt[2]["text"] != " with " || prompt[4]["text"] != " please" {
		t.Fatalf("text %v", prompt)
	}
	if prompt[1]["data"] != base64.StdEncoding.EncodeToString(testPNG.Data) || prompt[1]["mimeType"] != "image/png" {
		t.Fatalf("image %v", prompt[1])
	}
	resource := prompt[3]["resource"].(map[string]any)
	if resource["blob"] != base64.StdEncoding.EncodeToString(testPDF.Data) || resource["mimeType"] != "application/pdf" || resource["uri"] != "file:///tmp/b.pdf" {
		t.Fatalf("resource %v", resource)
	}

	// The transcript shows them the same way, and reads as the words around them.
	user := v.Entries[0]
	if want := "text:compare  | file:a.png | text: with  | file:b.pdf | text: please"; describeParts(user.Parts) != want {
		t.Fatalf("parts %q", describeParts(user.Parts))
	}
	if user.Text != "compare a.png with b.pdf please" {
		t.Fatalf("text %q", user.Text)
	}
	if v.Title != "compare with please" {
		t.Fatalf("title %q", v.Title)
	}
	// A file it won't draw again isn't held in memory.
	if user.Parts[3].File.Data != nil || user.Parts[1].File.Data == nil {
		t.Fatalf("kept %v and %v", user.Parts[3].File.Data, user.Parts[1].File.Data)
	}

	// A reload rebuilds the message from the agent's account, in order.
	if err := a.Reload(id); err != nil {
		t.Fatal(err)
	}
	v = await(t, a, id, func(v ThreadView) bool { return v.Connected && !v.Configuring })
	if want := "text:compare  | file:Image | text: with  | file:b.pdf | text: please"; describeParts(v.Entries[0].Parts) != want {
		t.Fatalf("replayed %q", describeParts(v.Entries[0].Parts))
	}
	if data, err := v.Entries[0].Parts[1].File.Contents(); err != nil || string(data) != string(testPNG.Data) {
		t.Fatalf("replayed image %q, %v", data, err)
	}
	if v.Entries[0].Details != "" {
		t.Fatalf("details hold the file: %q", v.Entries[0].Details)
	}
}

func TestPromptOfOnlyAFile(t *testing.T) {
	command := testutil.Agent(t)
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command)
	if err := a.SendParts(id, []state.Part{{File: testPNG}}); err != nil {
		t.Fatal(err)
	}
	v := await(t, a, id, func(v ThreadView) bool { return !v.Running && !v.Configuring })
	if v.Error != "" || v.Title != "a.png" || describeParts(v.Entries[0].Parts) != "file:a.png" {
		t.Fatalf("%+v", v)
	}
	if blocks := lastPrompt(t, command); len(blocks) != 1 || blocks[0]["type"] != "image" {
		t.Fatalf("blocks %v", blocks)
	}
}

func TestEmptyPromptIsRefused(t *testing.T) {
	a := openTest(t, t.TempDir())
	id := createTest(t, a, testutil.Agent(t))
	if err := a.SendParts(id, []state.Part{{Text: "  "}}); err == nil {
		t.Fatal("sent nothing")
	}
}

// An agent's echo of a prompt with files is the prompt, already shown, whether it
// starts with text or a file, and with or without message IDs.
func TestEchoOfFilesIsNotShownTwice(t *testing.T) {
	for _, mode := range []string{"echo-files", "echo-files-ids"} {
		for name, parts := range map[string][]state.Part{
			"text first": {{Text: "see "}, {File: testPNG}, {Text: " and "}, {File: testPDF}},
			"file first": {{File: testPNG}, {Text: " and "}, {File: testPDF}},
		} {
			t.Run(mode+" "+name, func(t *testing.T) {
				command := fmt.Sprintf("%q %s", testutil.Agent(t), mode)
				a := openTest(t, t.TempDir())
				id := createTest(t, a, command)
				if err := a.SendParts(id, parts); err != nil {
					t.Fatal(err)
				}
				v := await(t, a, id, func(v ThreadView) bool { return !v.Running && !v.Configuring })
				var users []state.Entry
				for _, e := range v.Entries {
					if e.Role == "user" {
						users = append(users, e)
					}
				}
				if len(users) != 1 || describeParts(users[0].Parts) != describeParts(parts) {
					t.Fatalf("user entries %+v", users)
				}
			})
		}
	}
}

// Whether a model can use a file is the agent's to say, and it stays in the thread.
func TestAgentRefusingAFileIsReported(t *testing.T) {
	command := fmt.Sprintf("%q refuse-files", testutil.Agent(t))
	a := openTest(t, t.TempDir())
	id := createTest(t, a, command)
	if err := a.SendParts(id, []state.Part{{Text: "watch this "}, {File: testPDF}}); err != nil {
		t.Fatal(err)
	}
	v := await(t, a, id, func(v ThreadView) bool { return !v.Running && !v.Configuring })
	if !strings.Contains(v.Error, "this model cannot take that file") {
		t.Fatalf("error %q", v.Error)
	}
	if v := sendDone(t, a, id, "still here"); v.Error != "" || lastText(v) != "Echo: still here" {
		t.Fatalf("after: %+v", v)
	}
}

func TestReplayOfAgentMessagesKeepsPlaceholders(t *testing.T) {
	// Only a user's messages are built from parts.
	th := &thread{Thread: &state.Thread{}, Conversation: &Conversation{}}
	appendChunk(th, "assistant", "", testPNG.Block())
	if len(th.Entries) != 1 || th.Entries[0].Parts != nil || !strings.Contains(th.Entries[0].Text, "Image") {
		t.Fatalf("%+v", th.Entries)
	}
}

func TestDivergentMessageWithoutIDsKeepsItsFiles(t *testing.T) {
	th := &thread{Thread: &state.Thread{}, Conversation: &Conversation{}}
	local := localEntry([]state.Part{{Text: "hello "}, {File: testPNG}})
	th.Entries = append(th.Entries, local)
	r := &runtime{job: &job{}, echo: promptEcho{target: "hello ", files: 1, local: local.ID}}
	other := media.Pasted("other.png", []byte("\x89PNG\r\n\x1a\n"))
	if !echoLocked(th, r, "", acp.TextContentBlock("something else ")) {
		t.Fatal("divergent text not taken")
	}
	if !echoLocked(th, r, "", other.Block()) {
		t.Fatal("divergent file not taken")
	}
	if !echoLocked(th, r, "", acp.TextContentBlock("after")) {
		t.Fatal("divergent text not taken")
	}
	if len(th.Entries) != 2 {
		t.Fatalf("entries %+v", th.Entries)
	}
	if got, want := describeParts(th.Entries[1].Parts), "text:something else  | file:Image | text:after"; got != want {
		t.Fatalf("divergent %q, want %q", got, want)
	}
	if got, want := describeParts(th.Entries[0].Parts), "text:hello  | file:a.png"; got != want {
		t.Fatalf("local %q, want %q", got, want)
	}
	if r.echo.files != 1 {
		t.Fatalf("it used up the prompt's file: %d left", r.echo.files)
	}
}

func TestAdjacentFilesStayApartAsText(t *testing.T) {
	if got := state.PartsText([]state.Part{{File: testPNG}, {File: testPDF}, {Text: " and"}, {File: testPNG}}); got != "a.png b.pdf and a.png" {
		t.Fatalf("got %q", got)
	}
}
