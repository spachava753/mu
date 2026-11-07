package media

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spachava753/acp-sdk/acp"
)

var pngHeader = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEveryKindGoesAsItsOwnBlock(t *testing.T) {
	for _, test := range []struct {
		name, mimeType string
		data           []byte
		block          acp.ContentBlockType
	}{
		{"shot.png", "image/png", pngHeader, acp.ContentBlockTypeImage},
		{"named-wrongly.jpg", "image/png", pngHeader, acp.ContentBlockTypeImage},
		{"voice.mp3", "audio/mpeg", []byte("\xff\xfb\x90\x00\x00binary\x00"), acp.ContentBlockTypeAudio},
		{"voice.m4a", "audio/mp4", []byte("\x00\x00\x00\x18ftypM4A \x00"), acp.ContentBlockTypeAudio},
		{"clip.mp4", "video/mp4", []byte("\x00\x00\x00\x18ftypmp42\x00"), acp.ContentBlockTypeResource},
		{"clip.mov", "video/quicktime", []byte("\x00\x00\x00\x14ftypqt  \x00"), acp.ContentBlockTypeResource},
		{"paper.pdf", "application/pdf", []byte("%PDF-1.7\n\x00\x01"), acp.ContentBlockTypeResource},
		{"blob.bin", "application/octet-stream", []byte{0, 1, 2, 3}, acp.ContentBlockTypeResource},
		{"notes.md", "text/markdown", []byte("# Notes\n"), acp.ContentBlockTypeResource},
		{"not-really.mp4", "text/plain", []byte("just some words\n"), acp.ContentBlockTypeResource},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := write(t, test.name, test.data)
			f, err := Read(path)
			if err != nil {
				t.Fatal(err)
			}
			if f.Name != test.name || f.Path != path || f.Size != int64(len(test.data)) {
				t.Fatalf("read %+v", f)
			}
			if f.MimeType != test.mimeType {
				t.Fatalf("type %q, want %q", f.MimeType, test.mimeType)
			}
			block := f.Block()
			if block.Type != test.block {
				t.Fatalf("block %s, want %s", block.Type, test.block)
			}
			// What reaches the agent decodes to the same file.
			wire, err := json.Marshal(block)
			if err != nil {
				t.Fatal(err)
			}
			var received acp.ContentBlock
			if err := json.Unmarshal(wire, &received); err != nil {
				t.Fatal(err)
			}
			got := ""
			switch received.Type {
			case acp.ContentBlockTypeImage, acp.ContentBlockTypeAudio:
				got = received.Data
			case acp.ContentBlockTypeResource:
				if received.Resource.Text != nil {
					if *received.Resource.Text != string(test.data) {
						t.Fatalf("text %q", *received.Resource.Text)
					}
					return
				}
				got = *received.Resource.Blob
				if received.Resource.URI == "" || *received.Resource.MimeType != test.mimeType {
					t.Fatalf("resource %+v", received.Resource)
				}
			}
			if got != base64.StdEncoding.EncodeToString(test.data) {
				t.Fatalf("data %q", got)
			}
		})
	}
}

func TestLargeFileGoesAsALink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long clip.mp4")
	if err := os.WriteFile(path, []byte("\x00\x00\x00\x18ftypmp42"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, EmbedLimit+1); err != nil {
		t.Fatal(err)
	}
	f, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Data != nil || !f.Linked() || f.MimeType != "video/mp4" {
		t.Fatalf("read %+v", f)
	}
	block := f.Block()
	if block.Type != acp.ContentBlockTypeResourceLink || block.Name != "long clip.mp4" || !strings.HasSuffix(*block.URI, "/long%20clip.mp4") || *block.Size != EmbedLimit+1 || *block.MimeType != "video/mp4" {
		t.Fatalf("block %+v", block)
	}
}

func TestReadFailures(t *testing.T) {
	if _, err := Read(t.TempDir()); err == nil {
		t.Fatal("read a folder")
	}
	if _, err := Read(filepath.Join(t.TempDir(), "gone.png")); err == nil {
		t.Fatal("read a missing file")
	}
}

func TestEmptyFileIsText(t *testing.T) {
	f, err := Read(write(t, "empty.txt", nil))
	if err != nil {
		t.Fatal(err)
	}
	if block := f.Block(); block.Type != acp.ContentBlockTypeResource || block.Resource.Text == nil {
		t.Fatalf("block %+v", block)
	}
}

func TestPastedImageHasNoLocation(t *testing.T) {
	block := Pasted("Pasted image.png", pngHeader).Block()
	if block.Type != acp.ContentBlockTypeImage || block.URI != nil {
		t.Fatalf("block %+v", block)
	}
}

func TestFromBlock(t *testing.T) {
	if f := FromBlock(acp.TextContentBlock("hi")); f != nil {
		t.Fatalf("text became %+v", f)
	}
	uri := "file:///tmp/a%20photo.png"
	mimeType := "image/png"
	encoded := base64.StdEncoding.EncodeToString(pngHeader)
	f := FromBlock(acp.ContentBlock{Type: acp.ContentBlockTypeImage, Data: encoded, MimeType: &mimeType, URI: &uri})
	if f.Name != "a photo.png" || f.Path != "/tmp/a photo.png" || f.Size != int64(len(pngHeader)) || !f.IsImage() {
		t.Fatalf("image %+v", f)
	}
	if data, err := f.Contents(); err != nil || string(data) != string(pngHeader) {
		t.Fatalf("contents %q, %v", data, err)
	}
	if f := FromBlock(acp.ImageContentBlock(encoded, mimeType)); f.Name != "Image" {
		t.Fatalf("unnamed image %+v", f)
	}
	if f := FromBlock(acp.ResourceContentBlock(acp.BlobResourceContentsEmbeddedResourceResource("AAAA", "file:///tmp/paper.pdf"))); f.Name != "paper.pdf" || f.Size != 3 {
		t.Fatalf("blob %+v", f)
	}
	if f := FromBlock(acp.ResourceContentBlock(acp.TextResourceContentsEmbeddedResourceResource("hello", "file:///tmp/a.txt"))); !f.Text || f.Size != 5 {
		t.Fatalf("text resource %+v", f)
	}
	if f := FromBlock(acp.ResourceLinkContentBlock("big.mov", "file:///tmp/big.mov")); f.Name != "big.mov" || f.Path != "/tmp/big.mov" {
		t.Fatalf("link %+v", f)
	}
}

func TestShownDropsContentsOnlyWhereNotDrawn(t *testing.T) {
	image, doc := Pasted("a.png", pngHeader), &File{Name: "a.pdf", MimeType: "application/pdf", Data: []byte("x"), Size: 1}
	if image.Shown() != image {
		t.Fatal("an image lost what draws it")
	}
	if shown := doc.Shown(); shown.Data != nil || doc.Data == nil || shown.Size != 1 {
		t.Fatalf("shown %+v, original %+v", shown, doc)
	}
}
