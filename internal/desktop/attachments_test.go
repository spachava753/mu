package desktop_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
	"github.com/spachava753/mu/internal/media"
	"github.com/spachava753/mu/internal/state"
	"github.com/spachava753/mu/internal/testutil"
)

// gradient is a picture to tell a thumbnail by.
func gradient(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(255 * x / w), uint8(255 * y / h), 160, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type attachFixture struct {
	a       *app.App
	v       *desktop.View
	tt      *ui.Tester
	command string
	dir     string
	picks   chan []string
}

func newAttachFixture(t *testing.T, width, height int) *attachFixture {
	t.Helper()
	a := testApp(t)
	command := testutil.Agent(t)
	if _, err := a.Create(fmt.Sprintf("%q session-ui", command), ""); err != nil {
		t.Fatal(err)
	}
	f := &attachFixture{a: a, command: command, dir: t.TempDir(), picks: make(chan []string, 1)}
	f.v = desktop.New(a, "")
	f.v.ChooseFiles = func() ([]string, error) { return <-f.picks, nil }
	f.tt = ui.NewTester(f.v.Draw, width, height)
	f.tt.SetDark(true)
	waitView(t, a, f.tt, func(s app.Snapshot) bool { return s.Threads[0].Connected && !s.Threads[0].Configuring })
	click(t, f.tt, "Message")
	return f
}

func (f *attachFixture) file(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(f.dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// drop drops files on the composer's message field.
func (f *attachFixture) drop(t *testing.T, paths ...string) {
	t.Helper()
	field, ok := f.tt.Find("Message")
	if !ok {
		t.Fatal("no message field")
	}
	if !f.tt.DropFiles(field.X+field.W/2, field.Y+field.H/2, paths...) {
		t.Fatal("the composer did not take the files")
	}
	f.tt.Frame()
}

// settled runs frames until no read or decode is outstanding and the condition holds.
func (f *attachFixture) settle(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		f.tt.Frame()
		if condition() {
			f.tt.Frame()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %q", what, f.tt.Texts())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (f *attachFixture) draft() string { return f.v.DraftOfSelected() }

// sentBlocks is the types of the last prompt's blocks, in order, with its text.
func (f *attachFixture) sentBlocks(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(f.command), "fixture-sessions", "calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var blocks []string
	for line := range strings.Lines(string(data)) {
		var call struct {
			Method string `json:"method"`
			Params struct {
				Prompt []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"prompt"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(line), &call) == nil && call.Method == "session/prompt" {
			blocks = nil
			for _, b := range call.Params.Prompt {
				if b.Type == "text" {
					blocks = append(blocks, b.Text)
				} else {
					blocks = append(blocks, "<"+b.Type+">")
				}
			}
		}
	}
	return blocks
}

func parts(e state.Entry) string {
	var out []string
	for _, part := range e.Parts {
		if part.File != nil {
			out = append(out, "<"+part.File.Name+">")
		} else {
			out = append(out, part.Text)
		}
	}
	return strings.Join(out, "")
}

// Files go after the text, in the order attached, whenever they were attached.
func TestAttachmentsFollowTheText(t *testing.T) {
	for _, size := range [][2]int{{800, 600}, {1100, 780}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newAttachFixture(t, size[0], size[1])
			photo := f.file(t, "photo.png", gradient(t, 640, 480))
			paper := f.file(t, "paper.pdf", []byte("%PDF-1.7\n\x00\x01"))
			clip := f.file(t, "a rather long name for a holiday clip from last summer.mp4", []byte("\x00\x00\x00\x18ftypmp42\x00"))
			voice := f.file(t, "voice.mp3", []byte("\xff\xfb\x90\x00\x00\x00"))

			f.tt.Type("compare this")
			f.tt.Frame()
			f.drop(t, photo)
			f.tt.Type(" with the paper")
			f.tt.Frame()
			f.drop(t, paper)
			f.tt.Type(" and listen")
			f.tt.Frame()
			f.drop(t, voice, clip)
			f.settle(t, "the files", func() bool { return !f.tt.HasText("Reading…") })
			f.tt.Advance(320 * time.Millisecond)
			if got, want := f.draft(), "compare this with the paper and listen"; got != want {
				t.Fatalf("draft %q, want %q", got, want)
			}
			if !f.tt.Focused("Message") {
				t.Fatal("dropping left the draft without the focus")
			}
			for _, label := range []string{"Attached file: photo.png", "Attached file: paper.pdf", "Attached file: voice.mp3"} {
				if _, ok := f.tt.Find(label); !ok {
					t.Fatalf("no card %q: %q", label, f.tt.Texts())
				}
			}
			screenshot(t, f.tt, fmt.Sprintf("attachments-draft-%d", size[0]))
			f.tt.SetDark(false)
			f.tt.Frame()
			screenshot(t, f.tt, fmt.Sprintf("attachments-draft-light-%d", size[0]))
			f.tt.SetDark(true)
			f.tt.Frame()

			f.tt.Key(0, ui.KeyEnter)
			waitView(t, f.a, f.tt, func(s app.Snapshot) bool {
				return !s.Threads[0].Running && !s.Threads[0].Configuring && len(s.Threads[0].Entries) >= 2
			})
			if got, want := strings.Join(f.sentBlocks(t), ""), "compare this with the paper and listen<image><resource><audio><resource>"; got != want {
				t.Fatalf("sent %q, want %q", got, want)
			}
			entry := f.a.Snapshot().Threads[0].Entries[0]
			if !strings.HasPrefix(parts(entry), "compare this with the paper and listen<photo.png><paper.pdf><voice.mp3>") {
				t.Fatalf("transcript %q", parts(entry))
			}
			if f.draft() != "" {
				t.Fatalf("draft after sending: %q", f.draft())
			}
			if _, ok := f.tt.Find("Remove photo.png"); ok {
				t.Fatal("the draft kept its files after sending")
			}
			f.settle(t, "the sent picture", func() bool {
				_, ok := f.tt.Find("Attached image: photo.png")
				return ok
			})
			f.tt.Frame()
			screenshot(t, f.tt, fmt.Sprintf("attachments-sent-%d", size[0]))
			f.tt.SetDark(false)
			f.tt.Frame()
			screenshot(t, f.tt, fmt.Sprintf("attachments-sent-light-%d", size[0]))
		})
	}
}

func TestFilesAloneCanBeSent(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	f.drop(t, f.file(t, "paper.pdf", []byte("%PDF\x00")))
	f.settle(t, "the file", func() bool { return !f.tt.HasText("Reading…") })
	f.tt.Key(0, ui.KeyEnter)
	waitView(t, f.a, f.tt, func(s app.Snapshot) bool {
		return !s.Threads[0].Running && len(s.Threads[0].Entries) >= 2
	})
	if got := strings.Join(f.sentBlocks(t), ""); got != "<resource>" {
		t.Fatalf("sent %q", got)
	}
	if title := f.a.Snapshot().Threads[0].Title; title != "paper.pdf" {
		t.Fatalf("title %q", title)
	}
}

func TestRemovingAFileKeepsTheRestOfTheDraft(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	one, two := f.file(t, "one.pdf", []byte("%PDF-1\x00")), f.file(t, "two.pdf", []byte("%PDF-2\x00"))
	f.tt.Type("look")
	f.tt.Frame()
	f.drop(t, one, two)
	f.settle(t, "the files", func() bool { return !f.tt.HasText("Reading…") })
	click(t, f.tt, "Remove one.pdf")
	if got := f.draft(); got != "look" {
		t.Fatalf("draft %q", got)
	}
	if !f.tt.Focused("Message") {
		t.Fatal("removing left the draft without the focus")
	}
	f.tt.Key(0, ui.KeyEnter)
	waitView(t, f.a, f.tt, func(s app.Snapshot) bool {
		return !s.Threads[0].Running && len(s.Threads[0].Entries) >= 2
	})
	if got := strings.Join(f.sentBlocks(t), ""); got != "look<resource>" {
		t.Fatalf("sent %q", got)
	}
}

func TestSameNameTwiceStaysTwoFiles(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	a, b := filepath.Join(t.TempDir(), "same.pdf"), filepath.Join(t.TempDir(), "same.pdf")
	for _, path := range []string{a, b} {
		if err := os.WriteFile(path, []byte("%PDF\x00"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f.drop(t, a, b)
	f.settle(t, "the files", func() bool { return !f.tt.HasText("Reading…") })
	f.tt.Key(0, ui.KeyEnter)
	waitView(t, f.a, f.tt, func(s app.Snapshot) bool {
		return !s.Threads[0].Running && len(s.Threads[0].Entries) >= 2
	})
	if got := strings.Join(f.sentBlocks(t), ""); got != "<resource><resource>" {
		t.Fatalf("sent %q", got)
	}
}

// Files can be got ready for the next message while a turn runs. They wait for it.
func TestAttachingDuringATurn(t *testing.T) {
	for _, size := range [][2]int{{800, 600}, {1100, 780}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newAttachFixture(t, size[0], size[1])
			optionsBefore, ok := f.tt.Find("Session options")
			if !ok {
				t.Fatal("no session options")
			}
			f.tt.Type("wait")
			f.tt.Frame()
			f.tt.Key(0, ui.KeyEnter)
			waitView(t, f.a, f.tt, func(s app.Snapshot) bool { return s.Threads[0].Running })
			f.tt.Frame()
			// Starting the turn doesn't wrap the options and push the draft up.
			if options, ok := f.tt.Find("Session options"); !ok || options.H != optionsBefore.H {
				t.Fatalf("options went from %+v to %+v", optionsBefore, options)
			}

			paper, other := f.file(t, "paper.pdf", []byte("%PDF\x00")), f.file(t, "other.pdf", []byte("%PDF\x00"))
			click(t, f.tt, "Attach files")
			f.picks <- []string{paper}
			f.settle(t, "the chosen file", func() bool {
				_, ok := f.tt.Find("Attached file: paper.pdf")
				return ok && !f.tt.HasText("Reading…")
			})
			f.drop(t, other)
			f.settle(t, "the dropped file", func() bool {
				_, ok := f.tt.Find("Attached file: other.pdf")
				return ok && !f.tt.HasText("Reading…")
			})
			screenshot(t, f.tt, fmt.Sprintf("attachments-during-turn-%d", size[0]))
			click(t, f.tt, "Remove other.pdf")
			if _, ok := f.tt.Find("Attached file: other.pdf"); ok {
				t.Fatal("couldn't remove a file during the turn")
			}
			if !f.a.Snapshot().Threads[0].Running || len(f.a.Snapshot().Threads[0].Entries) != 2 {
				t.Fatalf("attaching touched the turn: %+v", f.a.Snapshot().Threads[0].Entries)
			}

			click(t, f.tt, "Stop")
			waitView(t, f.a, f.tt, func(s app.Snapshot) bool { return !s.Threads[0].Running })
			if _, ok := f.tt.Find("Attached file: paper.pdf"); !ok {
				t.Fatal("the file didn't wait for the next message")
			}
			click(t, f.tt, "Send")
			waitView(t, f.a, f.tt, func(s app.Snapshot) bool {
				return !s.Threads[0].Running && len(s.Threads[0].Entries) > 3
			})
			if got := strings.Join(f.sentBlocks(t), ""); got != "<resource>" {
				t.Fatalf("sent %q", got)
			}
		})
	}
}

// The next message can be written while a turn runs: only sending has to wait.
func TestComposingDuringATurn(t *testing.T) {
	for _, size := range [][2]int{{800, 600}, {1100, 780}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newAttachFixture(t, size[0], size[1])
			f.tt.Type("wait")
			f.tt.Frame()
			f.tt.Key(0, ui.KeyEnter)
			waitView(t, f.a, f.tt, func(s app.Snapshot) bool { return s.Threads[0].Running })
			// Sending leaves the focus in the draft, ready for the next one.
			if !f.tt.Focused("Message") {
				t.Fatal("the draft lost the focus")
			}
			f.tt.Type("next")
			f.tt.Frame()
			f.tt.Key(ui.Shift, ui.KeyEnter)
			f.tt.Type("prompt")
			f.tt.Frame()
			f.tt.SetClipboard(" pasted")
			f.tt.Key(ui.Cmd, ui.KeyV)
			f.tt.Frame()
			if got, want := f.draft(), "next\nprompt pasted"; got != want {
				t.Fatalf("draft %q, want %q", got, want)
			}
			paper := f.file(t, "paper.pdf", []byte("%PDF\x00"))
			f.drop(t, paper)
			f.settle(t, "the file", func() bool { return !f.tt.HasText("Reading…") })
			screenshot(t, f.tt, fmt.Sprintf("composing-during-turn-%d", size[0]))

			// Enter does nothing yet: it neither sends nor breaks the line.
			f.tt.Key(0, ui.KeyEnter)
			f.tt.Frame()
			if got, want := f.draft(), "next\nprompt pasted"; got != want {
				t.Fatalf("after Enter, draft %q, want %q", got, want)
			}
			if n := len(f.a.Snapshot().Threads[0].Entries); n != 2 {
				t.Fatalf("sent during the turn: %d entries", n)
			}
			if _, ok := f.tt.Find("Send"); ok {
				t.Fatal("offered Send during the turn")
			}

			click(t, f.tt, "Stop")
			waitView(t, f.a, f.tt, func(s app.Snapshot) bool { return !s.Threads[0].Running })
			if got, want := f.draft(), "next\nprompt pasted"; got != want {
				t.Fatalf("after stopping, draft %q, want %q", got, want)
			}
			if !f.tt.Focused("Message") {
				t.Fatal("stopping left the draft without the focus")
			}
			f.tt.Key(0, ui.KeyEnter)
			waitView(t, f.a, f.tt, func(s app.Snapshot) bool {
				return !s.Threads[0].Running && len(s.Threads[0].Entries) > 3
			})
			if got := strings.Join(f.sentBlocks(t), ""); got != "next\nprompt pasted<resource>" {
				t.Fatalf("sent %q", got)
			}
			if f.draft() != "" {
				t.Fatalf("draft after sending: %q", f.draft())
			}
		})
	}
}

// A turn that ends by itself, here once its permission is answered, leaves the draft.
func TestDraftWrittenDuringATurnSendsWhenItEnds(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	f.tt.Type("permission")
	f.tt.Frame()
	f.tt.Key(0, ui.KeyEnter)
	waitView(t, f.a, f.tt, func(s app.Snapshot) bool { return len(s.Threads[0].Permissions) == 1 })
	f.tt.Type("next")
	f.tt.Frame()
	f.tt.Key(0, ui.KeyEnter)
	f.tt.Frame()
	if !f.a.Snapshot().Threads[0].Running || f.draft() != "next" {
		t.Fatalf("running %v, draft %q", f.a.Snapshot().Threads[0].Running, f.draft())
	}
	click(t, f.tt, "Allow once")
	waitView(t, f.a, f.tt, func(s app.Snapshot) bool { return !s.Threads[0].Running })
	if got := f.draft(); got != "next" {
		t.Fatalf("draft %q", got)
	}
	if !f.tt.Focused("Message") {
		t.Fatal("answering left the draft without the focus")
	}
	f.tt.Key(0, ui.KeyEnter)
	waitView(t, f.a, f.tt, func(s app.Snapshot) bool {
		e := s.Threads[0].Entries
		return !s.Threads[0].Running && len(e) > 0 && e[len(e)-1].Text == "Echo: next"
	})
	if got := strings.Join(f.sentBlocks(t), ""); got != "next" {
		t.Fatalf("sent %q", got)
	}
}

// Slash commands can be completed during a turn. Enter still only completes.
func TestSlashCommandsDuringATurn(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	f.tt.Type("wait")
	f.tt.Frame()
	f.tt.Key(0, ui.KeyEnter)
	waitView(t, f.a, f.tt, func(s app.Snapshot) bool { return s.Threads[0].Running })
	f.tt.Type("/rev")
	f.tt.Frame()
	if !f.tt.HasText("/review") {
		t.Fatalf("no picker: %q", f.tt.Texts())
	}
	f.tt.Key(0, ui.KeyEnter)
	f.tt.Frame()
	if got := f.draft(); got != "/review " {
		t.Fatalf("draft %q", got)
	}
	if n := len(f.a.Snapshot().Threads[0].Entries); n != 2 {
		t.Fatalf("sent during the turn: %d entries", n)
	}
}

func TestAttachButtonUsesTheFilePicker(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	paper := f.file(t, "paper.pdf", []byte("%PDF-1.7\x00"))
	f.tt.Type("read")
	f.tt.Frame()
	click(t, f.tt, "Attach files")
	f.picks <- []string{paper}
	f.settle(t, "the chosen file", func() bool {
		_, ok := f.tt.Find("Attached file: paper.pdf")
		return ok && !f.tt.HasText("Reading…")
	})
	if got := f.draft(); got != "read" {
		t.Fatalf("draft %q", got)
	}
	if !f.tt.Focused("Message") {
		t.Fatal("the draft lost the focus")
	}
	// Cancelling changes nothing.
	click(t, f.tt, "Attach files")
	f.picks <- nil
	f.settle(t, "the picker closing", func() bool { return true })
	if _, ok := f.tt.Find("Attached file: paper.pdf"); !ok {
		t.Fatal("cancelling removed the file")
	}
}

func TestPastingAnImage(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	f.tt.Type("this")
	f.tt.Frame()
	f.tt.Key(0, ui.KeyLeft)
	f.tt.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, gradient(t, 32, 32)))))
	f.tt.Key(ui.Cmd, ui.KeyV)
	f.settle(t, "the paste", func() bool {
		_, ok := f.tt.Find("Attached file: Pasted image.png")
		return ok
	})
	// The paste leaves the text and the caret alone.
	f.tt.Type("X")
	f.tt.Frame()
	if got := f.draft(); got != "thiXs" {
		t.Fatalf("draft %q", got)
	}
	// Text beside a picture of it, as from a spreadsheet, pastes as text.
	f.tt.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.Text, []byte("cells")), transfer.Bytes(transfer.PNG, gradient(t, 32, 32)))))
	f.tt.Key(ui.Cmd, ui.KeyV)
	f.tt.Frame()
	f.tt.Frame()
	if got := f.draft(); got != "thiXcellss" {
		t.Fatalf("draft %q", got)
	}
	f.tt.Key(0, ui.KeyEnter)
	waitView(t, f.a, f.tt, func(s app.Snapshot) bool {
		return !s.Threads[0].Running && len(s.Threads[0].Entries) >= 2
	})
	if got := strings.Join(f.sentBlocks(t), ""); got != "thiXcellss<image>" {
		t.Fatalf("sent %q", got)
	}
}

// Too large to embed, and with no file to link to, it's refused rather than lost.
func TestPastingTooLargeAnImage(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	f.tt.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, make([]byte, media.EmbedLimit+1)))))
	f.tt.Key(ui.Cmd, ui.KeyV)
	f.tt.Frame()
	f.tt.Frame()
	if _, ok := f.tt.Find("Attached file: Pasted image.png"); ok {
		t.Fatal("attached it")
	}
	if !f.tt.HasText("This image is too large to paste. Save it to a file and attach that.") {
		t.Fatalf("no error: %q", f.tt.Texts())
	}
}

func TestPastingFiles(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	paper := f.file(t, "paper.pdf", []byte("%PDF-1.7\x00"))
	data, err := transfer.FileData(paper)
	if err != nil {
		t.Fatal(err)
	}
	f.tt.SetClipboardData(data)
	f.tt.Key(ui.Cmd, ui.KeyV)
	f.settle(t, "the paste", func() bool {
		_, ok := f.tt.Find("Attached file: paper.pdf")
		return ok && !f.tt.HasText("Reading…")
	})
	if got := f.draft(); got != "" {
		t.Fatalf("draft %q", got)
	}
}

func TestUnreadableFileIsReported(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	f.tt.Type("look")
	f.tt.Frame()
	f.drop(t, filepath.Join(f.dir, "gone.png"), f.dir)
	f.settle(t, "the errors", func() bool { return !f.tt.HasText("Reading…") })
	if got := f.draft(); got != "look" {
		t.Fatalf("draft %q", got)
	}
	if _, ok := f.tt.Find("Attached file: gone.png"); ok {
		t.Fatal("kept a card for a file it couldn't read")
	}
	found := false
	for _, text := range f.tt.Texts() {
		found = found || strings.HasPrefix(text, "Could not attach")
	}
	if !found {
		t.Fatalf("no error: %q", f.tt.Texts())
	}
}

// While a file is being read the draft can't go, whichever way it's sent.
func TestSendWaitsForFilesToBeRead(t *testing.T) {
	f := newAttachFixture(t, 1100, 780)
	release := make(chan struct{})
	f.v.SetFileReader(func(path string) (*media.File, error) {
		<-release
		return media.Read(path)
	})
	paper := f.file(t, "paper.pdf", []byte("%PDF\x00"))
	f.tt.Type("look")
	f.tt.Frame()
	f.drop(t, paper)
	if !f.tt.HasText("Reading…") {
		t.Fatalf("no sign of reading: %q", f.tt.Texts())
	}
	screenshot(t, f.tt, "attachments-reading")
	f.tt.Key(0, ui.KeyEnter)
	f.tt.Frame()
	if err := f.tt.Click("Send"); err != nil {
		t.Fatal(err)
	}
	f.tt.Frame()
	if n := len(f.a.Snapshot().Threads[0].Entries); n != 0 {
		t.Fatalf("sent while reading: %d entries", n)
	}
	close(release)
	f.settle(t, "the read", func() bool { return !f.tt.HasText("Reading…") })
	click(t, f.tt, "Message")
	f.tt.Key(0, ui.KeyEnter)
	waitView(t, f.a, f.tt, func(s app.Snapshot) bool {
		return !s.Threads[0].Running && len(s.Threads[0].Entries) >= 2
	})
	if got := strings.Join(f.sentBlocks(t), ""); got != "look<resource>" {
		t.Fatalf("sent %q", got)
	}
}

func TestManyAttachmentsLeaveTheComposerInTheWindow(t *testing.T) {
	f := newAttachFixture(t, 800, 600)
	var paths []string
	for i := range 14 {
		paths = append(paths, f.file(t, fmt.Sprintf("file-%d.pdf", i), []byte("%PDF\x00")))
	}
	f.drop(t, paths...)
	f.settle(t, "the files", func() bool { return !f.tt.HasText("Reading…") })
	f.tt.Advance(320 * time.Millisecond)
	for _, label := range []string{"Message", "Send", "Attach files"} {
		r, ok := f.tt.Find(label)
		if !ok || r.Y < 0 || r.Y+r.H > 600 {
			t.Fatalf("%s is at %+v, %v", label, r, ok)
		}
	}
	screenshot(t, f.tt, "attachments-many-800")
}

func TestDraftsKeepTheirFilesAcrossThreads(t *testing.T) {
	a := testApp(t)
	command := fmt.Sprintf("%q session-ui", testutil.Agent(t))
	first, err := a.Create(command, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Create(command, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Rename(first, "First"); err != nil {
		t.Fatal(err)
	}
	if err := a.Rename(second, "Second"); err != nil {
		t.Fatal(err)
	}
	v := desktop.New(a, "")
	tt := ui.NewTester(v.Draw, 1100, 780)
	waitView(t, a, tt, func(s app.Snapshot) bool {
		for _, thread := range s.Threads {
			if thread.Connected && !thread.Configuring {
				return true
			}
		}
		return false
	})
	path := filepath.Join(t.TempDir(), "paper.pdf")
	if err := os.WriteFile(path, []byte("%PDF\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	field, _ := tt.Find("Message")
	if !tt.DropFiles(field.X+10, field.Y+10, path) {
		t.Fatal("not taken")
	}
	tt.Frame()
	deadline := time.Now().Add(8 * time.Second)
	for tt.HasText("Reading…") {
		if time.Now().After(deadline) {
			t.Fatal("not read")
		}
		tt.Frame()
		time.Sleep(time.Millisecond)
	}
	_, inFirst := tt.Find("Attached file: paper.pdf")
	if !inFirst {
		t.Fatal("no card")
	}
	// Whichever thread was in view has the file, and the other doesn't.
	click(t, tt, "Thread: First")
	_, onFirst := tt.Find("Attached file: paper.pdf")
	click(t, tt, "Thread: Second")
	_, onSecond := tt.Find("Attached file: paper.pdf")
	if onFirst == onSecond {
		t.Fatalf("on First %v, on Second %v", onFirst, onSecond)
	}
}
