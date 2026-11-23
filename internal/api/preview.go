package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/manzanit0/athenaeum/internal/convert"
	"github.com/manzanit0/athenaeum/internal/preview"
	"github.com/manzanit0/athenaeum/internal/store"
)

// The ways a book can be shown in the interface.
const (
	// previewEPUB shows the documents of an EPUB one at a time.
	previewEPUB = "epub"
	// previewPDF hands the file to the browser's PDF viewer.
	previewPDF = "pdf"
	// previewText shows a plain text file.
	previewText = "text"
)

// contentPolicy is the Content-Security-Policy sent with the files of a
// book. Books are untrusted: their scripts never run and they cannot load
// anything from outside the server.
const contentPolicy = "default-src 'self' data: blob:; style-src 'self' 'unsafe-inline' data:; " +
	"script-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"

// nativePreviews maps the formats shown as they are to the way they are
// shown. Every other format is converted to EPUB first.
var nativePreviews = map[string]string{"epub": previewEPUB, "pdf": previewPDF, "txt": previewText}

// previewDocument tells the interface how to show a book.
type previewDocument struct {
	// Kind is one of epub, pdf or text.
	Kind string `json:"kind"`
	// Format is the format of the file being shown.
	Format string `json:"format"`
	// Converted reports whether the file was converted to EPUB first.
	Converted bool `json:"converted"`
	// Base is the address of the content: the file itself for pdf and
	// text, the folder the hrefs are relative to for epub.
	Base string `json:"base"`
	// Sections lists the documents of an EPUB in reading order.
	Sections []preview.Section `json:"sections"`
	// Contents lists the lines of the table of contents of an EPUB.
	Contents []preview.Entry `json:"contents"`
}

// previewRank orders the formats of a book by how well they preview:
// EPUB first, then the formats the built in converters turn into EPUB,
// then those calibre can, then the ones the browser shows itself.
// Without calibre those come before the rest. The formats that are
// only kept come last, since nothing shows them.
func (s *Server) previewRank(format string) int {
	_, native := nativePreviews[format]
	switch {
	case format == "epub":
		return 0
	case convert.Kept(format):
		return 1000
	case !native && convert.ReadsNatively(format):
		return 10
	case native && s.Tools.CanConvert():
		return 200 + nativeRank(format)
	case native:
		return 20 + nativeRank(format)
	}
	return 100 + sourceRank(format)
}

// nativeRank orders the formats the browser shows itself, preferring a
// PDF over plain text.
func nativeRank(format string) int {
	if format == "pdf" {
		return 0
	}
	return 1
}

// previewSource picks the file of book to show. An empty format selects
// the one that previews best.
func (s *Server) previewSource(book *store.Book, format string) (store.Format, error) {
	if format != "" {
		source, ok := book.Format(format)
		if !ok {
			return store.Format{}, fmt.Errorf("%w: the book has no %s file", store.ErrNotFound, format)
		}
		return source, nil
	}
	if len(book.Formats) == 0 {
		return store.Format{}, fmt.Errorf("%w: the book has no files to preview", errBadRequest)
	}
	return slices.MinFunc(book.Formats, func(a, b store.Format) int {
		return s.previewRank(a.Format) - s.previewRank(b.Format)
	}), nil
}

// epubOf returns the EPUB to read source, a file of book, from: the file
// itself or its rendition in the cache, which may not exist yet.
func (s *Server) epubOf(book *store.Book, source store.Format) (string, error) {
	file := s.Library.FormatPath(book, source)
	if source.Format == "epub" {
		return file, nil
	}
	return s.Previews.Path(book.UUID, file)
}

// getPreview describes how to show a book. It answers 202 with a job
// when the book has to be converted first.
func (s *Server) getPreview(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err != nil {
		fail(w, err)
		return
	}
	source, err := s.previewSource(book, strings.ToLower(r.URL.Query().Get("format")))
	if err != nil {
		fail(w, err)
		return
	}
	doc := previewDocument{
		Kind:     nativePreviews[source.Format],
		Format:   source.Format,
		Base:     fmt.Sprintf("/api/books/%d/preview/%s/", book.ID, source.Format),
		Sections: []preview.Section{},
		Contents: []preview.Entry{},
	}
	if doc.Kind == previewPDF || doc.Kind == previewText {
		writeJSON(w, http.StatusOK, doc)
		return
	}
	epub, err := s.epubOf(book, source)
	if err != nil {
		fail(w, err)
		return
	}
	if _, err := os.Stat(epub); err != nil && doc.Kind == "" {
		s.preparePreview(w, book, source, epub)
		return
	}
	outline, err := preview.ReadOutline(epub)
	if err != nil {
		fail(w, fmt.Errorf("%w: %w", errUnreadable, err))
		return
	}
	doc.Kind, doc.Converted = previewEPUB, source.Format != "epub"
	doc.Sections, doc.Contents = outline.Sections, outline.Contents
	writeJSON(w, http.StatusOK, doc)
}

// preparePreview answers with the job converting source, a file of book,
// into the EPUB at target, starting it unless it is already running.
func (s *Server) preparePreview(w http.ResponseWriter, book *store.Book, source store.Format, target string) {
	if convert.Kept(source.Format) {
		fail(w, convert.KeptError(source.Format))
		return
	}
	if !s.Tools.CanRead(source.Format) {
		fail(w, fmt.Errorf("%w: previewing %s files needs ebook-convert",
			convert.ErrUnavailable, strings.ToUpper(source.Format)))
		return
	}
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	if id, running := s.previewJobs[target]; running {
		job, err := s.Store.Job(id)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
		return
	}
	file := s.Library.FormatPath(book, source)
	title := fmt.Sprintf("Prepare a preview of %q", book.Title)
	job, err := s.startJob("preview", title, &book.ID, func(ctx context.Context) (any, error) {
		defer s.forgetPreviewJob(target)
		return nil, s.render(ctx, book.UUID, source.Format, file, target)
	})
	if err != nil {
		fail(w, err)
		return
	}
	if s.previewJobs == nil {
		s.previewJobs = map[string]int64{}
	}
	s.previewJobs[target] = job.ID
	writeJSON(w, http.StatusAccepted, job)
}

// forgetPreviewJob records that the job making target has finished.
func (s *Server) forgetPreviewJob(target string) {
	s.previewMu.Lock()
	defer s.previewMu.Unlock()
	delete(s.previewJobs, target)
}

// render converts file, in the given format and belonging to the book
// uuid, into the EPUB at target, replacing older renditions of it.
func (s *Server) render(ctx context.Context, uuid, format, file, target string) error {
	tmp, err := s.Previews.Temp()
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if _, err := s.Tools.Convert(ctx, convert.Request{In: file, Out: tmp}); err != nil {
		return err
	}
	if _, err := preview.ReadOutline(tmp); err != nil {
		return fmt.Errorf("the converted book cannot be opened: %w", err)
	}
	if err := s.Previews.Drop(uuid, format); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// getPreviewFile serves the content of a preview: the PDF or text file
// itself, or one file from inside the EPUB.
func (s *Server) getPreviewFile(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err != nil {
		fail(w, err)
		return
	}
	source, err := s.previewSource(book, strings.ToLower(r.PathValue("format")))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-cache")
	if kind := nativePreviews[source.Format]; kind == previewPDF || kind == previewText {
		w.Header().Set("Content-Disposition", "inline")
		http.ServeFile(w, r, s.Library.FormatPath(book, source))
		return
	}
	epub, err := s.epubOf(book, source)
	if err != nil {
		fail(w, err)
		return
	}
	file, err := preview.OpenFile(epub, r.PathValue("path"))
	if err != nil {
		fail(w, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Security-Policy", contentPolicy)
	w.Header().Set("Content-Type", file.MediaType)
	w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
	if r.Method != http.MethodHead {
		io.Copy(w, file)
	}
}
