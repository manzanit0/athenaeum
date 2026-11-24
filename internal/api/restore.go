package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/manzanit0/athenaeum/internal/restore"
	"github.com/manzanit0/athenaeum/internal/store"
)

// modelKeyName is the name the key of the language model is kept under.
const modelKeyName = "anthropic.key"

// What a restore is expected to cost, in dollars for every page. The
// lower figures hold when the service remembers what it has read within
// a session, the higher ones when it reads everything anew every turn.
const (
	// scanLow is the least a scanned page costs.
	scanLow = 0.02
	// scanHigh is the most a scanned page costs.
	scanHigh = 0.06
	// textLow is the least a page with a text layer costs.
	textLow = 0.015
	// textHigh is the most a page with a text layer costs.
	textHigh = 0.035
	// leastLimit is the least a restore is allowed to cost before it
	// is stopped.
	leastLimit = 1.0
)

// errNoModelKey says that the key of the language model is missing.
var errNoModelKey = errors.New("restoring needs an Anthropic API key")

// Secrets keeps values safe between runs of the application.
type Secrets interface {
	// Get returns the value kept under name and whether there is one.
	Get(name string) ([]byte, bool, error)
	// Put keeps value under name.
	Put(name string, value []byte) error
	// Delete forgets the value kept under name.
	Delete(name string) error
}

// modelKey returns the key of the language model: the one the user has
// given, or the one the application was started with.
func (s *Server) modelKey() (string, error) {
	if s.Secrets != nil {
		data, found, err := s.Secrets.Get(modelKeyName)
		if err != nil {
			return "", err
		}
		if found {
			return string(data), nil
		}
	}
	return s.ModelKey, nil
}

// restoreSetup says whether books can be restored.
type restoreSetup struct {
	// Configured reports whether the key of the language model is
	// known.
	Configured bool `json:"configured"`
	// Model is the language model that restores books.
	Model string `json:"model"`
}

// setup returns whether books can be restored.
func (s *Server) setup() (restoreSetup, error) {
	key, err := s.modelKey()
	return restoreSetup{Configured: key != "", Model: restore.DefaultModel}, err
}

// getRestoreSetup says whether books can be restored.
func (s *Server) getRestoreSetup(w http.ResponseWriter, _ *http.Request) {
	setup, err := s.setup()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, setup)
}

// keyRequest carries the key of the language model.
type keyRequest struct {
	// Key is the Anthropic API key.
	Key string `json:"key"`
}

// putModelKey keeps the key of the language model.
func (s *Server) putModelKey(w http.ResponseWriter, r *http.Request) {
	var req keyRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "" {
		fail(w, errors.Join(errBadRequest, errors.New("the key is empty")))
		return
	}
	if err := s.Secrets.Put(modelKeyName, []byte(key)); err != nil {
		fail(w, err)
		return
	}
	s.getRestoreSetup(w, r)
}

// deleteModelKey forgets the key of the language model.
func (s *Server) deleteModelKey(w http.ResponseWriter, r *http.Request) {
	if err := s.Secrets.Delete(modelKeyName); err != nil {
		fail(w, err)
		return
	}
	s.getRestoreSetup(w, r)
}

// restoreOutlook says what restoring a book would involve.
type restoreOutlook struct {
	restoreSetup
	restore.Survey
	// Possible reports whether the book can be restored.
	Possible bool `json:"possible"`
	// Reason says why the book cannot be restored.
	Reason string `json:"reason,omitempty"`
	// Sample is how many pages are read before the model says whether
	// it expects to restore the book well.
	Sample int `json:"sample"`
	// CostLow is the least the restore is expected to cost, in dollars.
	CostLow float64 `json:"cost_low"`
	// CostHigh is the most the restore is expected to cost.
	CostHigh float64 `json:"cost_high"`
	// Limit is the cost at which the restore is stopped.
	Limit float64 `json:"limit"`
	// Replaces reports whether the book has an EPUB the restored one
	// would replace.
	Replaces bool `json:"replaces"`
}

// outlook works out what restoring book would involve.
func (s *Server) outlook(book *store.Book) (restoreOutlook, error) {
	setup, err := s.setup()
	if err != nil {
		return restoreOutlook{}, err
	}
	out := restoreOutlook{restoreSetup: setup}
	_, out.Replaces = book.Format("epub")
	source, ok := book.Format("pdf")
	if !ok {
		out.Reason = "Only books with a PDF can be restored for now."
		return out, nil
	}
	survey, err := restore.Inspect(s.Library.FormatPath(book, source))
	switch {
	case errors.Is(err, restore.ErrEncrypted):
		out.Reason = "The PDF is encrypted."
		return out, nil
	case err != nil:
		out.Reason = "The PDF could not be read."
		return out, nil
	}
	out.Survey = survey
	if survey.Scans+survey.Text == 0 {
		out.Reason = "The PDF has no page that can be read."
		return out, nil
	}
	out.Possible = true
	out.Sample = min(restore.SamplePages, survey.Pages)
	out.CostLow = float64(survey.Scans)*scanLow + float64(survey.Text)*textLow
	out.CostHigh = float64(survey.Scans)*scanHigh + float64(survey.Text)*textHigh
	out.Limit = max(leastLimit, 2*out.CostHigh)
	return out, nil
}

// getRestoreOutlook says what restoring a book would involve.
func (s *Server) getRestoreOutlook(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err != nil {
		fail(w, err)
		return
	}
	outlook, err := s.outlook(book)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, outlook)
}

// restoreRequest asks for a book to be restored.
type restoreRequest struct {
	// Replace allows the restored book to take the place of the EPUB
	// the book already has.
	Replace bool `json:"replace"`
}

// restoreBook starts a job restoring the PDF of a book into an EPUB.
func (s *Server) restoreBook(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err != nil {
		fail(w, err)
		return
	}
	var req restoreRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	outlook, err := s.outlook(book)
	switch {
	case err != nil:
		fail(w, err)
		return
	case !outlook.Possible:
		fail(w, fmt.Errorf("%w: %s", errBadRequest, outlook.Reason))
		return
	case !outlook.Configured:
		fail(w, errNoModelKey)
		return
	case outlook.Replaces && !req.Replace:
		fail(w, fmt.Errorf("%w: the book already has an EPUB, which the restored one would replace", errBadRequest))
		return
	}
	title := fmt.Sprintf("Restore %q", book.Title)
	job, err := s.startTrackedJob("restore", title, &book.ID, func(ctx context.Context, report func(any)) (any, error) {
		return s.restore(ctx, book.ID, outlook.Limit, report)
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// restoration is the outcome of restoring a book.
type restoration struct {
	// Book is the book with its new file.
	Book *store.Book `json:"book"`
	*restore.Report
}

// model returns the language model that restores books.
func (s *Server) model(ctx context.Context) (restore.Model, error) {
	key, err := s.modelKey()
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, errNoModelKey
	}
	if s.Model != nil {
		return s.Model(ctx, key)
	}
	return restore.Anthropic(ctx, key, restore.DefaultModel)
}

// restore restores the PDF of the book with the given ID into an EPUB
// and adds it to the library. It is stopped once it has cost limit
// dollars.
func (s *Server) restore(ctx context.Context, id int64, limit float64, report func(any)) (*restoration, error) {
	book, err := s.Store.Book(id)
	if err != nil {
		return nil, err
	}
	source, ok := book.Format("pdf")
	if !ok {
		return nil, errors.New("the book no longer has a PDF file")
	}
	model, err := s.model(ctx)
	if err != nil {
		return nil, err
	}
	out, err := os.CreateTemp(s.Library.TempDir(), "restore-*.epub")
	if err != nil {
		return nil, err
	}
	out.Close()
	defer os.Remove(out.Name())
	result, err := restore.Restore(ctx, restore.Options{
		Source: s.Library.FormatPath(book, source), Dest: out.Name(), Model: model,
		Metadata: book.Metadata, Cover: s.Library.CoverPath(book), ID: book.UUID, Limit: limit,
		Progress: func(p restore.Progress) { report(p) },
	})
	if err != nil {
		return nil, err
	}
	for _, warning := range result.Warnings {
		slog.Info("restore left something as it was", "book", id, "warning", warning)
	}
	book, err = s.Library.AddFormatFile(id, "epub", out.Name())
	if err != nil {
		return nil, err
	}
	return &restoration{Book: book, Report: result}, nil
}
