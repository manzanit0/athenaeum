package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/manzanit0/athenaeum/internal/device"
	"github.com/manzanit0/athenaeum/internal/store"
)

// defaultDeviceFormat is the format books are converted to when none of
// their files can be read by the e-reader.
const defaultDeviceFormat = "azw3"

// deviceBook is a book on the e-reader, linked to the library when it was
// sent from or imported into it.
type deviceBook struct {
	device.File
	// BookID identifies the matching library book, if any.
	BookID *int64 `json:"book_id,omitempty"`
}

// sendRequest asks for a library book to be copied to the e-reader.
type sendRequest struct {
	// BookID identifies the library book.
	BookID int64 `json:"book_id"`
	// Format is the format to send; empty picks the best one.
	Format string `json:"format"`
}

// sendResult describes a book copied to the e-reader.
type sendResult struct {
	// Path is the file path relative to the e-reader root.
	Path string `json:"path"`
	// Format is the format that was sent.
	Format string `json:"format"`
	// Converted reports whether the book had to be converted first.
	Converted bool `json:"converted"`
	// Embedded reports whether the library metadata was written into the
	// file that was sent.
	Embedded bool `json:"embedded"`
	// Warnings lists what a conversion had to leave out.
	Warnings []string `json:"warnings"`
}

// deviceImportRequest asks for a book on the e-reader to be imported.
type deviceImportRequest struct {
	// Path is the file path relative to the e-reader root.
	Path string `json:"path"`
}

// deviceStatus reports whether an e-reader is connected.
func (s *Server) deviceStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Kindle.Status())
}

// listDeviceBooks returns the books on the e-reader.
func (s *Server) listDeviceBooks(w http.ResponseWriter, _ *http.Request) {
	files, err := s.Kindle.List()
	if err != nil {
		fail(w, err)
		return
	}
	links, err := s.Store.DeviceFiles()
	if err != nil {
		fail(w, err)
		return
	}
	books := make([]deviceBook, 0, len(files))
	for _, f := range files {
		book := deviceBook{File: f}
		if link, ok := links[f.Path]; ok {
			book.BookID = &link.BookID
			delete(links, f.Path)
		}
		books = append(books, book)
	}
	for stale := range links {
		if err := s.Store.DeleteDeviceFile(stale); err != nil {
			slog.Warn("forgetting device file", "path", stale, "error", err)
		}
	}
	writeJSON(w, http.StatusOK, books)
}

// sendToDevice starts a job copying a library book to the e-reader,
// converting it first when none of its formats is supported.
func (s *Server) sendToDevice(w http.ResponseWriter, r *http.Request) {
	var req sendRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	book, err := s.Store.Book(req.BookID)
	if err != nil {
		fail(w, err)
		return
	}
	format, err := s.deviceFormat(book, strings.ToLower(req.Format))
	if err == nil && !s.Kindle.Status().Connected {
		err = device.ErrNotConnected
	}
	if err != nil {
		fail(w, err)
		return
	}
	title := fmt.Sprintf("Send %q to Kindle as %s", book.Title, strings.ToUpper(format))
	job, err := s.startJob("send", title, &book.ID, func(ctx context.Context) (any, error) {
		return s.send(ctx, book.ID, format)
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// deviceFormat chooses the format of book to send. An empty requested
// format selects the preferred supported format the book has, or the
// default device format when it has none.
func (s *Server) deviceFormat(book *store.Book, requested string) (string, error) {
	format := requested
	if format == "" {
		format = defaultDeviceFormat
		for _, candidate := range device.Formats() {
			if _, ok := book.Format(candidate); ok {
				format = candidate
				break
			}
		}
	}
	if !device.Supports(format) {
		return "", fmt.Errorf("%w: a Kindle cannot read %s files", errBadRequest, format)
	}
	if _, ok := book.Format(format); ok {
		return format, nil
	}
	if _, err := s.conversionSource(book, "", format); err != nil {
		return "", err
	}
	return format, nil
}

// send copies the given format of a book to the e-reader, converting the
// book and embedding its metadata when needed and possible.
func (s *Server) send(ctx context.Context, id int64, format string) (*sendResult, error) {
	book, err := s.Store.Book(id)
	if err != nil {
		return nil, err
	}
	result := &sendResult{Format: format, Warnings: []string{}}
	if _, ok := book.Format(format); !ok {
		source, err := s.conversionSource(book, "", format)
		if err != nil {
			return nil, err
		}
		converted, err := s.convert(ctx, id, source.Format, format)
		if err != nil {
			return nil, err
		}
		book, result.Converted, result.Warnings = converted.Book, true, converted.Warnings
	}
	file, _ := book.Format(format)
	src := s.Library.FormatPath(book, file)
	if embedded, err := s.embeddedCopy(ctx, book, src); err != nil {
		slog.Warn("embedding metadata", "book", id, "error", err)
	} else if embedded != "" {
		defer os.Remove(embedded)
		src, result.Embedded = embedded, true
	}
	if result.Path, err = s.Kindle.Put(file.Filename, src); err != nil {
		return nil, err
	}
	link := store.DeviceFile{Path: result.Path, BookID: id, Format: format}
	return result, s.Store.SetDeviceFile(link)
}

// embeddedCopy returns the path of a scratch copy of the file at src with
// book's metadata written into it, or an empty string when metadata cannot
// be embedded.
func (s *Server) embeddedCopy(ctx context.Context, book *store.Book, src string) (string, error) {
	if !s.Tools.CanEmbed() {
		return "", nil
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(s.Library.TempDir(), "send-*"+path.Ext(src))
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = s.Tools.Embed(ctx, out.Name(), book.Metadata, s.Library.CoverPath(book))
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// deleteDeviceBook removes the book named by the path parameter from the
// e-reader.
func (s *Server) deleteDeviceBook(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if err := s.Kindle.Delete(rel); err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteDeviceFile(rel); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// importFromDevice copies a book from the e-reader into the library.
func (s *Server) importFromDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceImportRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	file, err := s.Kindle.Open(req.Path)
	if err != nil {
		fail(w, err)
		return
	}
	defer file.Close()
	book, err := s.Library.Import(r.Context(), path.Base(req.Path), file)
	if err != nil {
		fail(w, err)
		return
	}
	link := store.DeviceFile{Path: req.Path, BookID: book.ID, Format: book.Formats[0].Format}
	if err := s.Store.SetDeviceFile(link); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, book)
}
