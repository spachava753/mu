package desktop

import (
	"bytes"
	"image"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/media"
	"github.com/spachava753/mu/internal/state"
)

// A file attached to a draft is sent after its text, as most agents and models take
// them. An agent's account of a message may still show them in another order.
type attachment struct {
	name string
	file *media.File // nil until it has been read
}

// attachments is what the view holds about files in drafts.
type attachments struct {
	files   map[string][]*attachment // by thread
	read    func(string) (*media.File, error)
	reads   chan attachRead
	picking chan filePick
	picked  string // the thread files are being chosen for
	shown   map[*media.File]*preview
	decoded chan decodedPreview
	frame   uint64
}

// attachRead is the result of reading a file for a draft.
type attachRead struct {
	thread string
	a      *attachment
	file   *media.File
	err    error
}

type filePick struct {
	paths []string
	err   error
}

// preview is an image's picture, decoded off the UI thread. Without a bitmap
// once decoded, it is an image Mu can't draw, which is only a loss of a thumbnail.
type preview struct {
	bitmap  *ui.Bitmap
	decoded bool
	frame   uint64 // the last frame that drew it
}

type decodedPreview struct {
	file   *media.File
	bitmap *ui.Bitmap
}

const (
	maxPreviewPixels = 64_000_000 // for one image: larger ones show as a file
	// previewBudget is how many pixels of pictures not on screen stay decoded.
	previewBudget = 64_000_000
)

func newAttachments() attachments {
	return attachments{
		files:   map[string][]*attachment{},
		read:    media.Read,
		reads:   make(chan attachRead, 16),
		shown:   map[*media.File]*preview{},
		decoded: make(chan decodedPreview, 16),
	}
}

// forget drops what the view holds for a thread's draft.
func (s *attachments) forget(thread string) {
	delete(s.files, thread)
}

// draftParts is a draft as a message: its text, if it has any, then its files.
func draftParts(text string, files []*attachment) []state.Part {
	var parts []state.Part
	if strings.TrimSpace(text) != "" || len(files) == 0 {
		parts = append(parts, state.Part{Text: text})
	}
	for _, a := range files {
		parts = append(parts, state.Part{File: a.file})
	}
	return parts
}

// addFiles adds files to a thread's draft at once, and reads them off the UI thread:
// what can go wrong is reported when it does, and until the reading is done the
// draft can't be sent.
func (v *View) addFiles(services ui.Services, thread string, paths []string) {
	s, read := &v.attach, v.attach.read
	delete(v.queued, thread) // A draft waiting to send goes back to being edited.
	for _, path := range paths {
		a := &attachment{name: filepath.Base(path)}
		s.files[thread] = append(s.files[thread], a)
		go func() {
			file, err := read(path)
			s.reads <- attachRead{thread, a, file, err}
			services.Invalidate()
		}()
	}
}

// attachPaths adds files from the picker or a drop, and returns to typing.
func (v *View) attachPaths(services ui.Services, thread string, paths []string) {
	if len(paths) > 0 {
		v.addFiles(services, thread, paths)
		v.picker.refocus = thread == v.selected
	}
}

// collectAttachments takes in what has finished happening in the background.
func (v *View) collectAttachments(services ui.Services) {
	s := &v.attach
	s.frame++
	for {
		select {
		case read := <-s.reads:
			if read.err != nil {
				if slices.Contains(s.files[read.thread], read.a) { // Not if it's been removed.
					v.err = "Could not attach " + read.a.name + ": " + read.err.Error()
					v.removeAttachment(read.thread, read.a)
				}
				continue
			}
			read.a.file = read.file
			continue
		case done := <-s.decoded:
			if p := s.shown[done.file]; p != nil {
				p.bitmap, p.decoded = done.bitmap, true
			}
			continue
		default:
		}
		break
	}
	if s.picking == nil {
		return
	}
	select {
	case pick := <-s.picking:
		s.picking = nil
		if pick.err != nil {
			v.err = "Could not open the file picker: " + pick.err.Error()
			return
		}
		v.attachPaths(services, s.picked, pick.paths)
	default:
	}
}

// removeAttachment takes a file out of a draft.
func (v *View) removeAttachment(thread string, a *attachment) {
	s := &v.attach
	s.files[thread] = slices.DeleteFunc(s.files[thread], func(other *attachment) bool { return other == a })
}

// chooseFiles shows the native file picker, off the UI thread.
func (v *View) chooseFiles(services ui.Services, thread string) {
	s := &v.attach
	result, pick := make(chan filePick, 1), v.ChooseFiles
	s.picking, s.picked = result, thread
	go func() {
		paths, err := pick()
		result <- filePick{paths, err}
		services.Invalidate()
	}()
}

// stillReading reports whether a draft has a file not read yet.
func (v *View) stillReading(thread string) bool {
	return slices.ContainsFunc(v.attach.files[thread], func(a *attachment) bool { return a.file == nil })
}

// pasteFiles takes a paste that has files or, with no text to go with it, an image.
func (v *View) pasteFiles(services ui.Services, thread, text string) bool {
	s := &v.attach
	formats := services.ClipboardFormats()
	if slices.Contains(formats, transfer.FileList) || slices.Contains(formats, transfer.URIList) {
		data := services.ReadClipboardData(transfer.FileList, transfer.URIList)
		if paths, err := data.Files(); err == nil && len(paths) > 0 {
			v.addFiles(services, thread, paths)
			return true
		}
	}
	if text == "" && slices.Contains(formats, transfer.PNG) {
		if png, err := services.ReadClipboardData(transfer.PNG).Read(transfer.PNG); err == nil && len(png) > 0 {
			// A file this large goes as a link to it, and this one has no location.
			if int64(len(png)) > media.EmbedLimit {
				v.err = "This image is too large to paste. Save it to a file and attach that."
				return true
			}
			const name = "Pasted image.png"
			s.files[thread] = append(s.files[thread], &attachment{name: name, file: media.Pasted(name, png)})
			return true
		}
	}
	return false
}

// attachmentStrip shows the files in a draft, in order, with a way to remove each.
// Several rows scroll, so the draft stays in the window.
func (v *View) attachmentStrip(c *ui.Context, thread string, removable bool) {
	files := v.attach.files[thread]
	if len(files) == 0 {
		return
	}
	ui.Scroll(c.Key("attachments")).Label("Attached files").Shrink(0).MaxHeight(attachmentStripHeight).Padding(10, 14, 6, 14).Children(func() {
		ui.Row(c).Wrap().Gap(6).Children(func() {
			for _, a := range files {
				ui.Box(c.Key(a)).Shrink(1).MinWidth(0).Children(func() {
					v.fileCard(c, a.file, a.name, func() {
						v.removeAttachment(thread, a)
						v.picker.refocus = thread == v.selected
					}, removable)
				})
			}
		})
	})
}

const (
	attachmentStripHeight = 124 // two rows of cards, and part of a third to show there are more
	fileCardHeight        = 44
	fileCardWidth         = 220
	sentImageWidth        = 320
	sentImageHeight       = 240
	sentImageMinSize      = 16
)

// fileCard is a file as a small card: a picture or an icon, its name, and what it
// is. With no file, it's still being read.
func (v *View) fileCard(c *ui.Context, file *media.File, name string, remove func(), removable bool) {
	t := c.Theme()
	about := ""
	label := "Attached file: " + name
	switch {
	case file == nil:
		about = "Reading…"
	case file.Linked():
		about = file.Describe() + " · as a link"
	default:
		about = file.Describe()
	}
	ui.Row(c).Label(label).Height(fileCardHeight).MaxWidth(fileCardWidth).Padding(5, 6, 5, 5).Gap(8).Radius(6).Border(1, hairline(t)).Background(t.Background).Children(func() {
		v.fileThumbnail(c, file)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, name).FontSize(12).FontWeight(500).SingleLine().Tooltip(name)
			ui.Text(c, about).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		if remove != nil && removable {
			smallIconButton(c, "Remove "+name, closeIcon).OnClick(remove)
		}
	})
}

// fileThumbnail is a card's picture: the image itself, or an icon of its kind.
func (v *View) fileThumbnail(c *ui.Context, file *media.File) {
	t := c.Theme()
	const size = 32
	if bitmap := v.previewOf(c, file); bitmap != nil {
		ui.Image(c, bitmap).Size(size, size).Radius(4).Fit(ui.Cover).Shrink(0)
		return
	}
	ui.Box(c).Size(size, size).Radius(4).Shrink(0).Center().Background(t.Text.Alpha(0.06)).Children(func() {
		if file == nil {
			ui.Spinner(c).Size(13, 13).Label("Reading file")
			return
		}
		ui.Icon(c, fileIcon(file)).Size(16, 16).TextColor(t.TextMuted)
	})
}

// fileIcon is the icon of a file's kind.
func fileIcon(file *media.File) *ui.SVG {
	switch {
	case file.Text:
		return documentIcon
	case strings.HasPrefix(file.MimeType, "image/"):
		return imageIcon
	case strings.HasPrefix(file.MimeType, "audio/"):
		return audioIcon
	case strings.HasPrefix(file.MimeType, "video/"):
		return videoIcon
	}
	return documentIcon
}

// previewOf is an image's picture, or nil if it isn't ready or can't be drawn. It
// starts decoding the first time it's asked, and never waits.
func (v *View) previewOf(c *ui.Context, file *media.File) *ui.Bitmap {
	if file == nil || !file.IsImage() || !file.HasContents() {
		return nil
	}
	s := &v.attach
	p := s.shown[file]
	if p == nil {
		p = &preview{}
		s.shown[file] = p
		services := c.Services()
		go func() {
			done := decodedPreview{file: file, bitmap: decodePreview(file)}
			s.decoded <- done
			services.Invalidate()
		}()
	}
	p.frame = s.frame
	return p.bitmap
}

// decodePreview decodes an image to show, unless it's too large to keep in memory.
func decodePreview(file *media.File) *ui.Bitmap {
	data, err := file.Contents()
	if err != nil {
		return nil
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width*config.Height > maxPreviewPixels {
		return nil
	}
	bitmap, err := ui.DecodeBitmap(data)
	if err != nil {
		return nil
	}
	return bitmap
}

// trimPreviews lets go of pictures not on screen once they add up, as a long
// transcript's would, along with the files they hold. Any still wanted decode again.
// An image that can't be drawn costs no pixels, so it doesn't wait for that.
func (v *View) trimPreviews() {
	s := &v.attach
	total := 0
	for file, p := range s.shown {
		switch {
		case !p.decoded || p.frame == s.frame:
		case p.bitmap == nil:
			delete(s.shown, file)
		default:
			w, h := p.bitmap.Size()
			total += w * h
		}
	}
	if total <= previewBudget {
		return
	}
	for file, p := range s.shown {
		if p.decoded && p.frame != s.frame {
			delete(s.shown, file)
		}
	}
}

// messageParts shows a user's message with files in it, in their places.
func (v *View) messageParts(c *ui.Context, parts []state.Part) {
	t := c.Theme()
	for _, part := range parts {
		if part.File == nil {
			if text := strings.TrimSpace(part.Text); text != "" {
				ui.Text(c, text).FontSize(14).LineHeight(1.5).Selectable()
			}
			continue
		}
		if bitmap := v.previewOf(c, part.File); bitmap != nil {
			w, h := bitmap.Size()
			scale := min(1, float32(sentImageWidth)/float32(w), float32(sentImageHeight)/float32(h))
			// A thin picture stays big enough to see, and a small one doesn't turn
			// into a circle. It covers its box, which its border makes a little smaller.
			width, height := max(float32(w)*scale, sentImageMinSize), max(float32(h)*scale, sentImageMinSize)
			ui.Image(c, bitmap).Size(width, height).Fit(ui.Cover).Radius(min(6, min(width, height)/4)).Border(1, hairline(t)).Label("Attached image: " + part.File.Name).Tooltip(part.File.Name + "\n" + part.File.Describe())
			continue
		}
		v.fileCard(c, part.File, part.File.Name, nil, false)
	}
}
