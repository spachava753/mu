package desktop

import (
	"testing"

	"github.com/spachava753/mu/internal/media"
	"github.com/spachava753/mu/internal/state"
)

func TestDraftParts(t *testing.T) {
	file := media.Pasted("a.png", []byte("x"))
	files := []*attachment{{name: "a.png", file: file}}
	if got := draftParts("look ", files); len(got) != 2 || got[0] != (state.Part{Text: "look "}) || got[1].File != file {
		t.Fatalf("got %+v", got)
	}
	if got := draftParts("  ", files); len(got) != 1 || got[0].File != file {
		t.Fatalf("blank text went too: %+v", got)
	}
	if got := draftParts("  ", nil); len(got) != 1 || got[0].Text != "  " {
		t.Fatalf("got %+v", got)
	}
}

func TestPreviewsOfImagesThatCannotBeDrawnDoNotPileUp(t *testing.T) {
	v := &View{attach: newAttachments()}
	s := &v.attach
	s.frame = 10
	onScreen, gone, decoding := media.Pasted("a.png", []byte("x")), media.Pasted("b.png", []byte("x")), media.Pasted("c.png", []byte("x"))
	s.shown[onScreen] = &preview{decoded: true, frame: 10}
	s.shown[gone] = &preview{decoded: true, frame: 9}
	s.shown[decoding] = &preview{frame: 9}
	v.trimPreviews()
	if s.shown[gone] != nil {
		t.Fatal("kept an undrawable image no longer on screen, and its contents")
	}
	if s.shown[onScreen] == nil {
		t.Fatal("dropped one on screen, which would decode it again every frame")
	}
	if s.shown[decoding] == nil {
		t.Fatal("dropped one still decoding, which would start it again")
	}
}
