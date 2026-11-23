package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/manzanit0/athenaeum/internal/convert"
	"github.com/manzanit0/athenaeum/internal/store"
)

// maxCoverBytes bounds the size of an uploaded cover image.
const maxCoverBytes = 20 << 20

// sourcePreference orders the formats by how well they convert.
var sourcePreference = []string{"epub", "azw3", "mobi", "docx", "fb2", "htmlz", "odt", "rtf", "txt", "pdf"}

// importFailure reports a file that could not be imported.
type importFailure struct {
	// File is the name of the uploaded file.
	File string `json:"file"`
	// Error explains why the import failed.
	Error string `json:"error"`
}

// importResult is the outcome of an upload of one or more files.
type importResult struct {
	// Books lists the books that were created.
	Books []*store.Book `json:"books"`
	// Errors lists the files that were rejected.
	Errors []importFailure `json:"errors"`
}

// listBooks returns the books matching the parameters of the request.
func (s *Server) listBooks(w http.ResponseWriter, r *http.Request) {
	books, err := s.Store.Books(listOptions(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, books)
}

// importBooks creates one book per file of a multipart upload.
func (s *Server) importBooks(w http.ResponseWriter, r *http.Request) {
	result := importResult{Books: []*store.Book{}, Errors: []importFailure{}}
	err := eachUpload(r, func(name string, file io.Reader) {
		book, err := s.Library.Import(r.Context(), name, file)
		if err != nil {
			result.Errors = append(result.Errors, importFailure{File: name, Error: err.Error()})
			return
		}
		result.Books = append(result.Books, book)
	})
	if err != nil {
		fail(w, err)
		return
	}
	status := http.StatusCreated
	if len(result.Books) == 0 {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, result)
}

// eachUpload calls fn for every file in the multipart body of r.
func eachUpload(r *http.Request, fn func(name string, file io.Reader)) error {
	reader, err := r.MultipartReader()
	if err != nil {
		return errors.Join(errBadRequest, err)
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.Join(errBadRequest, err)
		}
		if part.FileName() != "" {
			fn(filepath.Base(part.FileName()), part)
		}
		part.Close()
	}
}

// book loads the book named by the id path parameter of r.
func (s *Server) book(r *http.Request) (*store.Book, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return s.Store.Book(id)
}

// getBook returns one book.
func (s *Server) getBook(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, book)
}

// patchBook updates the metadata fields present in the request body.
func (s *Server) patchBook(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err != nil {
		fail(w, err)
		return
	}
	m := book.Metadata
	if err := decode(r, &m); err != nil {
		fail(w, err)
		return
	}
	updated, err := s.Library.UpdateMetadata(book.ID, m)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// deleteBook removes a book and its files.
func (s *Server) deleteBook(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err == nil {
		err = s.Library.Delete(book.ID)
	}
	if err == nil {
		err = s.Previews.Drop(book.UUID, "")
	}
	if err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getCover serves the cover image of a book.
func (s *Server) getCover(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err == nil && book.Cover == "" {
		err = store.ErrNotFound
	}
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, s.Library.CoverPath(book))
}

// putCover replaces the cover of a book with the image in the request
// body.
func (s *Server) putCover(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCoverBytes))
	if err != nil {
		fail(w, errors.Join(errBadRequest, err))
		return
	}
	book, err := s.Library.SetCover(id, data)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, book)
}

// deleteCover removes the cover of a book.
func (s *Server) deleteCover(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	book, err := s.Library.RemoveCover(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, book)
}

// addFormat stores the uploaded files as additional formats of a book.
func (s *Server) addFormat(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err != nil {
		fail(w, err)
		return
	}
	var failure error
	err = eachUpload(r, func(name string, file io.Reader) {
		if updated, err := s.Library.AddFormat(book.ID, name, file); err != nil {
			failure = err
		} else {
			book = updated
		}
	})
	if err = errors.Join(err, failure); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, book)
}

// downloadFormat serves one file of a book as a download.
func (s *Server) downloadFormat(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err != nil {
		fail(w, err)
		return
	}
	format, ok := book.Format(r.PathValue("format"))
	if !ok {
		fail(w, store.ErrNotFound)
		return
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": format.Filename})
	w.Header().Set("Content-Disposition", disposition)
	http.ServeFile(w, r, s.Library.FormatPath(book, format))
}

// deleteFormat removes one file of a book.
func (s *Server) deleteFormat(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	book, err := s.Library.DeleteFormat(id, r.PathValue("format"))
	if err == nil {
		err = s.Previews.Drop(book.UUID, strings.ToLower(r.PathValue("format")))
	}
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, book)
}

// rescan makes the database match the library folder.
func (s *Server) rescan(w http.ResponseWriter, r *http.Request) {
	result, err := s.Library.Rescan(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// convertRequest asks for a book to be converted.
type convertRequest struct {
	// From is the source format; empty picks the best one available.
	From string `json:"from"`
	// To is the target format.
	To string `json:"to"`
}

// convertBook starts a job converting a book to another format.
func (s *Server) convertBook(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err != nil {
		fail(w, err)
		return
	}
	var req convertRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	req.To = strings.ToLower(req.To)
	source, err := s.conversionSource(book, req.From, req.To)
	if err != nil {
		fail(w, err)
		return
	}
	title := fmt.Sprintf("Convert %q from %s to %s", book.Title,
		strings.ToUpper(source.Format), strings.ToUpper(req.To))
	job, err := s.startJob("convert", title, &book.ID, func(ctx context.Context) (any, error) {
		return s.convert(ctx, book.ID, source.Format, req.To)
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// errNeedsCalibre explains that a conversion is beyond the built in
// converters and calibre is not installed.
func errNeedsCalibre(from, to string) error {
	return fmt.Errorf("%w: converting %s to %s needs calibre's ebook-convert",
		convert.ErrUnavailable, strings.ToUpper(from), strings.ToUpper(to))
}

// conversionSource picks the file of book to convert to the format to.
// An empty from selects the most suitable format that can be converted.
func (s *Server) conversionSource(book *store.Book, from, to string) (store.Format, error) {
	if !slices.Contains(convert.OutputFormats(), to) {
		return store.Format{}, fmt.Errorf("%w: cannot convert to %q", errBadRequest, to)
	}
	if from != "" {
		source, ok := book.Format(strings.ToLower(from))
		switch {
		case !ok:
			return store.Format{}, fmt.Errorf("%w: the book has no %s file", errBadRequest, from)
		case source.Format == to:
			return store.Format{}, fmt.Errorf("%w: source and target formats are the same", errBadRequest)
		case convert.Kept(source.Format):
			return store.Format{}, convert.KeptError(source.Format)
		case !s.Tools.Supports(source.Format, to):
			return store.Format{}, errNeedsCalibre(source.Format, to)
		}
		return source, nil
	}
	others := slices.DeleteFunc(slices.Clone(book.Formats), func(f store.Format) bool {
		return f.Format == to
	})
	candidates := slices.DeleteFunc(slices.Clone(others), func(f store.Format) bool {
		return convert.Kept(f.Format)
	})
	if len(candidates) == 0 && len(others) > 0 {
		return store.Format{}, convert.KeptError(others[0].Format)
	}
	if len(candidates) == 0 {
		return store.Format{}, fmt.Errorf("%w: the book has no other format to convert from", errBadRequest)
	}
	slices.SortStableFunc(candidates, func(a, b store.Format) int {
		return sourceRank(a.Format) - sourceRank(b.Format)
	})
	for _, candidate := range candidates {
		if s.Tools.Supports(candidate.Format, to) {
			return candidate, nil
		}
	}
	return store.Format{}, errNeedsCalibre(candidates[0].Format, to)
}

// conversion is the outcome of converting a book.
type conversion struct {
	// Book is the book with its new file.
	Book *store.Book `json:"book"`
	// Engine is the engine that did the conversion.
	Engine string `json:"engine"`
	// Warnings lists what had to be left out of the new file.
	Warnings []string `json:"warnings"`
}

// sourceRank returns the position of format in sourcePreference, ranking
// unknown formats last.
func sourceRank(format string) int {
	if i := slices.Index(sourcePreference, format); i >= 0 {
		return i
	}
	return len(sourcePreference)
}

// convert converts the book with the given ID from one format to another
// and adds the result to the library.
func (s *Server) convert(ctx context.Context, id int64, from, to string) (*conversion, error) {
	book, err := s.Store.Book(id)
	if err != nil {
		return nil, err
	}
	source, ok := book.Format(from)
	if !ok {
		return nil, fmt.Errorf("the book no longer has a %s file", from)
	}
	out, err := os.CreateTemp(s.Library.TempDir(), "convert-*."+to)
	if err != nil {
		return nil, err
	}
	out.Close()
	defer os.Remove(out.Name())
	report, err := s.Tools.Convert(ctx, convert.Request{
		In: s.Library.FormatPath(book, source), Out: out.Name(),
		Metadata: book.Metadata, Cover: s.Library.CoverPath(book), ID: book.UUID,
	})
	if err != nil {
		return nil, err
	}
	for _, warning := range report.Warnings {
		slog.Info("conversion left something out", "book", id, "to", to, "warning", warning)
	}
	book, err = s.Library.AddFormatFile(id, to, out.Name())
	if err != nil {
		return nil, err
	}
	return &conversion{Book: book, Engine: report.Engine, Warnings: report.Warnings}, nil
}
