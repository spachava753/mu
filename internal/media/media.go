// Package media reads files attached to a prompt and converts between them and
// ACP content. Which kinds a model can use is for the agent to say: nothing here
// refuses a file because of its type.
package media

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spachava753/acp-sdk/acp"
)

// EmbedLimit is the largest file sent in the prompt itself. A larger one would
// be held in memory and pushed down the agent's pipe, so it goes as a link, which
// every ACP agent must accept.
const EmbedLimit = 100 << 20

// sniffLength is how much of a file http.DetectContentType looks at.
const sniffLength = 512

// File is an attached file, as it was when attached. It's immutable, so that
// snapshots can share it.
type File struct {
	Name     string
	Path     string // absolute, or empty for one that isn't on disk
	MimeType string
	Size     int64
	Text     bool   // valid UTF-8 text, sent as such
	Data     []byte // the contents, if embedded and still needed

	encoded string // the contents as an agent sent them, in base64
}

// Read reads a file to attach. It may block, so call it off the UI thread.
func Read(name string) (*File, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, err
	}
	// Before opening it: opening a named pipe would wait for a writer.
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("it isn't a regular file")
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	file := &File{Name: filepath.Base(abs), Path: abs, Size: info.Size()}
	if file.Linked() {
		head := make([]byte, sniffLength)
		n, err := io.ReadFull(f, head)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}
		file.MimeType, _ = detect(file.Name, head[:n], false)
		return file, nil
	}
	file.Data, err = io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	// It may have changed size since Stat.
	file.Size = int64(len(file.Data))
	if file.Linked() {
		file.Data = nil
	}
	file.MimeType, file.Text = detect(file.Name, file.Data, true)
	return file, nil
}

// Pasted is an image from the clipboard, which comes as a PNG on every platform.
func Pasted(name string, png []byte) *File {
	return &File{Name: name, MimeType: "image/png", Size: int64(len(png)), Data: png}
}

// byExtension covers common media types that mime.TypeByExtension knows only from
// tables that differ from machine to machine, or not at all.
var byExtension = map[string]string{
	".aac": "audio/aac", ".flac": "audio/flac", ".m4a": "audio/mp4", ".mp3": "audio/mpeg", ".oga": "audio/ogg", ".ogg": "audio/ogg", ".opus": "audio/opus", ".wav": "audio/wav", ".weba": "audio/webm",
	".avi": "video/x-msvideo", ".m4v": "video/mp4", ".mkv": "video/x-matroska", ".mov": "video/quicktime", ".mp4": "video/mp4", ".mpeg": "video/mpeg", ".mpg": "video/mpeg", ".webm": "video/webm",
	".avif": "image/avif", ".bmp": "image/bmp", ".heic": "image/heic", ".heif": "image/heif", ".tif": "image/tiff", ".tiff": "image/tiff",
	".md": "text/markdown", ".pdf": "application/pdf",
}

// detect names a file's type from its name and first bytes, and reports whether it
// is text. Without its whole contents, it can't be sure, and says it isn't.
func detect(name string, data []byte, whole bool) (mimeType string, text bool) {
	head := data[:min(len(data), sniffLength)]
	sniffed, _, _ := mime.ParseMediaType(http.DetectContentType(head))
	ext := strings.ToLower(filepath.Ext(name))
	named := byExtension[ext]
	if named == "" && ext != "" {
		named, _, _ = mime.ParseMediaType(mime.TypeByExtension(ext))
	}
	if whole && strings.HasPrefix(sniffed, "text/") && utf8.Valid(data) {
		// A name may claim a media type for text, as .ts does for an MPEG stream.
		if named == "" || mediaType(named) {
			return "text/plain", true
		}
		return named, true
	}
	switch {
	case strings.HasPrefix(sniffed, "image/"): // More reliable than a name for images.
		return sniffed, false
	case named != "":
		return named, false
	}
	return sniffed, false
}

func mediaType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "audio/") || strings.HasPrefix(mimeType, "video/")
}

// Linked reports whether the file is too large to embed and goes as a link.
func (f *File) Linked() bool { return f.Size > EmbedLimit }

// IsImage reports whether the file is sent as an image.
func (f *File) IsImage() bool { return !f.Text && strings.HasPrefix(f.MimeType, "image/") }

// Contents returns an image's bytes for showing it, if they are at hand. Decoding
// what an agent sent can take a moment, so call it off the UI thread.
func (f *File) Contents() ([]byte, error) {
	if f.encoded != "" {
		return base64.StdEncoding.DecodeString(f.encoded)
	}
	if f.Data == nil {
		return nil, errors.New("no contents")
	}
	return f.Data, nil
}

// HasContents reports whether Contents has anything to return.
func (f *File) HasContents() bool { return f.encoded != "" || f.Data != nil }

// Shown is the file as a transcript keeps it: an image's contents are there to
// draw, and nothing else's are needed once it has been sent.
func (f *File) Shown() *File {
	if f.IsImage() || f.Data == nil {
		return f
	}
	shown := *f
	shown.Data = nil
	return &shown
}

// uri is the file's location, if it has one.
func (f *File) uri() string {
	if f.Path == "" {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: f.Path}).String()
}

// Block is the file as ACP content. Images and audio have their own block types;
// anything else, such as a PDF or a video, is an embedded resource.
func (f *File) Block() acp.ContentBlock {
	uri := f.uri()
	switch {
	case f.Linked():
		block := acp.ResourceLinkContentBlock(f.Name, uri)
		block.MimeType, block.Size = &f.MimeType, &f.Size
		return block
	case f.IsImage():
		block := acp.ImageContentBlock(base64.StdEncoding.EncodeToString(f.Data), f.MimeType)
		if uri != "" {
			block.URI = &uri
		}
		return block
	case strings.HasPrefix(f.MimeType, "audio/") && !f.Text:
		return acp.AudioContentBlock(base64.StdEncoding.EncodeToString(f.Data), f.MimeType)
	case f.Text:
		resource := acp.TextResourceContentsEmbeddedResourceResource(string(f.Data), uri)
		resource.MimeType = &f.MimeType
		return acp.ResourceContentBlock(resource)
	}
	resource := acp.BlobResourceContentsEmbeddedResourceResource(base64.StdEncoding.EncodeToString(f.Data), uri)
	resource.MimeType = &f.MimeType
	return acp.ResourceContentBlock(resource)
}

// FromBlock is the file an agent's account of a user message describes, or nil if
// the block is text. It keeps what showing it needs, without decoding anything.
func FromBlock(b acp.ContentBlock) *File {
	switch b.Type {
	case acp.ContentBlockTypeImage:
		return &File{Name: nameOf(deref(b.URI), "Image"), Path: localPath(deref(b.URI)), MimeType: deref(b.MimeType), Size: encodedSize(b.Data), encoded: b.Data}
	case acp.ContentBlockTypeAudio:
		return &File{Name: nameOf(deref(b.URI), "Audio"), Path: localPath(deref(b.URI)), MimeType: deref(b.MimeType), Size: encodedSize(b.Data)}
	case acp.ContentBlockTypeResourceLink:
		f := &File{Name: b.Name, Path: localPath(deref(b.URI)), MimeType: deref(b.MimeType), Size: deref(b.Size)}
		if f.Name == "" {
			f.Name = nameOf(deref(b.URI), "File")
		}
		return f
	case acp.ContentBlockTypeResource:
		f := &File{Name: nameOf(b.Resource.URI, "File"), Path: localPath(b.Resource.URI), MimeType: deref(b.Resource.MimeType)}
		if b.Resource.Text != nil {
			f.Text, f.Size = true, int64(len(*b.Resource.Text))
		} else {
			f.Size = encodedSize(deref(b.Resource.Blob))
		}
		return f
	}
	return nil
}

// encodedSize is the length of what base64 text decodes to.
func encodedSize(encoded string) int64 {
	return int64(base64.StdEncoding.DecodedLen(len(encoded)) - strings.Count(encoded[max(0, len(encoded)-2):], "="))
}

// nameOf is the last element of a URI, or fallback if it has none.
func nameOf(uri, fallback string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Path == "" {
		return fallback
	}
	if name := path.Base(parsed.Path); name != "." && name != "/" {
		return name
	}
	return fallback
}

func localPath(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return ""
	}
	return parsed.Path
}

func deref[T any](v *T) T {
	var zero T
	if v == nil {
		return zero
	}
	return *v
}

// Describe says what kind of file it is and how large, in a few words.
func (f *File) Describe() string {
	return kindLabel(f) + " · " + FormatSize(f.Size)
}

func kindLabel(f *File) string {
	sub := f.MimeType
	if _, rest, ok := strings.Cut(sub, "/"); ok {
		sub = rest
	}
	switch {
	case f.Text:
		return "Text"
	case f.MimeType == "application/pdf":
		return "PDF"
	case strings.HasPrefix(f.MimeType, "image/"):
		return "Image · " + strings.ToUpper(strings.TrimPrefix(sub, "x-"))
	case strings.HasPrefix(f.MimeType, "audio/"):
		return "Audio"
	case strings.HasPrefix(f.MimeType, "video/"):
		return "Video"
	}
	return "File"
}

// FormatSize is a byte count for people.
func FormatSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}
