// Package api exposes every feature of the application over HTTP and
// serves the embedded web interface.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manzanit0/athenaeum/internal/convert"
	"github.com/manzanit0/athenaeum/internal/device"
	"github.com/manzanit0/athenaeum/internal/library"
	"github.com/manzanit0/athenaeum/internal/megasync"
	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/preview"
	"github.com/manzanit0/athenaeum/internal/store"
)

// Setting keys stored in the database.
const (
	// SettingMegaFolder is the MEGA folder the library syncs with.
	SettingMegaFolder = "mega_folder"
	// SettingKindlePath is the mount point of the Kindle.
	SettingKindlePath = "kindle_path"
)

// jobTimeout bounds how long a background job may run.
const jobTimeout = 2 * time.Hour

// Server holds the dependencies of the HTTP handlers.
type Server struct {
	// Store is the database index.
	Store *store.Store
	// Library manages the book files.
	Library *library.Library
	// Tools drives the calibre command line tools.
	Tools *convert.Tools
	// Kindle gives access to the connected e-reader.
	Kindle *device.Kindle
	// Syncer synchronises the library with MEGA.
	Syncer *megasync.Syncer
	// Previews keeps the EPUB renditions made to preview other formats.
	Previews *preview.Cache
	// Defaults holds the setting values used until the user changes them.
	Defaults map[string]string
	// UI is the built web interface.
	UI fs.FS
	// Version identifies the running build.
	Version string
	// jobs tracks the running background jobs.
	jobs sync.WaitGroup
	// progress holds how far the running jobs that report it have come,
	// by job ID.
	progress sync.Map
	// previewMu guards previewJobs.
	previewMu sync.Mutex
	// previewJobs maps the renditions being made to the jobs making them.
	previewJobs map[string]int64
}

// Handler returns the HTTP handler serving the API and the web interface.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/system", s.system)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	mux.HandleFunc("GET /api/books", s.listBooks)
	mux.HandleFunc("POST /api/books", s.importBooks)
	mux.HandleFunc("GET /api/books/facets", s.facets)
	mux.HandleFunc("POST /api/books/tags", s.tagBooks)
	mux.HandleFunc("POST /api/books/tags/rename", s.renameTag)
	mux.HandleFunc("GET /api/books/{id}", s.getBook)
	mux.HandleFunc("PATCH /api/books/{id}", s.patchBook)
	mux.HandleFunc("DELETE /api/books/{id}", s.deleteBook)
	mux.HandleFunc("GET /api/books/{id}/cover", s.getCover)
	mux.HandleFunc("PUT /api/books/{id}/cover", s.putCover)
	mux.HandleFunc("DELETE /api/books/{id}/cover", s.deleteCover)
	mux.HandleFunc("POST /api/books/{id}/formats", s.addFormat)
	mux.HandleFunc("GET /api/books/{id}/formats/{format}", s.downloadFormat)
	mux.HandleFunc("DELETE /api/books/{id}/formats/{format}", s.deleteFormat)
	mux.HandleFunc("POST /api/books/{id}/convert", s.convertBook)
	mux.HandleFunc("GET /api/books/{id}/preview", s.getPreview)
	mux.HandleFunc("GET /api/books/{id}/preview/{format}/{path...}", s.getPreviewFile)
	mux.HandleFunc("POST /api/library/rescan", s.rescan)

	mux.HandleFunc("GET /api/jobs", s.listJobs)
	mux.HandleFunc("GET /api/jobs/{id}", s.getJob)

	mux.HandleFunc("GET /api/device", s.deviceStatus)
	mux.HandleFunc("GET /api/device/books", s.listDeviceBooks)
	mux.HandleFunc("POST /api/device/books", s.sendToDevice)
	mux.HandleFunc("DELETE /api/device/books", s.deleteDeviceBook)
	mux.HandleFunc("POST /api/device/import", s.importFromDevice)

	mux.HandleFunc("GET /api/sync", s.syncStatus)
	mux.HandleFunc("POST /api/sync", s.startSync)
	mux.HandleFunc("POST /api/sync/session", s.signIn)
	mux.HandleFunc("DELETE /api/sync/session", s.signOut)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	mux.Handle("/", s.ui())
	return logRequests(mux)
}

// Wait blocks until the running background jobs have finished.
func (s *Server) Wait() {
	s.jobs.Wait()
}

// statusRecorder remembers the status code written to a response.
type statusRecorder struct {
	http.ResponseWriter
	// status is the response status code.
	status int
}

// WriteHeader records status and forwards it to the wrapped writer.
func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// logRequests logs every API request with its status and duration.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path,
			"status", rec.status, "duration", time.Since(start).Round(time.Millisecond))
	})
}

// ui serves the embedded web interface, answering unknown paths with the
// application shell so client side routes survive a reload.
func (s *Server) ui() http.Handler {
	files := http.FileServerFS(s.UI)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if info, err := fs.Stat(s.UI, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		shell, err := fs.ReadFile(s.UI, "index.html")
		if err != nil {
			http.Error(w, "The web interface is not built. Run `task web`.", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(shell)
	})
}

// writeJSON writes v as a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding response", "error", err)
	}
}

// writeError writes a JSON error response with the given status.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// errorStatuses maps known errors to HTTP status codes.
var errorStatuses = []struct {
	// err is the error to match.
	err error
	// status is the HTTP status code to answer with.
	status int
}{
	{store.ErrNotFound, http.StatusNotFound},
	{library.ErrUnsupportedFormat, http.StatusUnsupportedMediaType},
	{library.ErrInvalidCover, http.StatusBadRequest},
	{library.ErrTitleRequired, http.StatusBadRequest},
	{library.ErrInvalidDate, http.StatusBadRequest},
	{errBadRequest, http.StatusBadRequest},
	{errUnreadable, http.StatusUnprocessableEntity},
	{preview.ErrNotFound, http.StatusNotFound},
	{convert.ErrUnavailable, http.StatusServiceUnavailable},
	{device.ErrNotConnected, http.StatusConflict},
	{device.ErrInvalidPath, http.StatusBadRequest},
	{megasync.ErrNotConfigured, http.StatusBadRequest},
	{megasync.ErrInvalidOptions, http.StatusBadRequest},
	{megasync.ErrSignedOut, http.StatusConflict},
	{megasync.ErrSessionEnded, http.StatusConflict},
	{megasync.ErrSignInFailed, http.StatusUnauthorized},
	{megasync.ErrBusy, http.StatusConflict},
}

// errBadRequest marks errors caused by a malformed request.
var errBadRequest = errors.New("bad request")

// errUnreadable marks books whose files cannot be opened for reading.
var errUnreadable = errors.New("the book cannot be opened")

// fail writes err as a JSON error response with a matching status.
func fail(w http.ResponseWriter, err error) {
	for _, known := range errorStatuses {
		if errors.Is(err, known.err) {
			writeError(w, known.status, err.Error())
			return
		}
	}
	slog.Error("request failed", "error", err)
	writeError(w, http.StatusInternalServerError, err.Error())
}

// decode reads the JSON request body into v.
func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return errors.Join(errBadRequest, err)
	}
	return nil
}

// pathID returns the numeric id path parameter of r.
func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, store.ErrNotFound
	}
	return id, nil
}

// setting returns the effective value of the setting key.
func (s *Server) setting(key string) string {
	value, err := s.Store.Setting(key, s.Defaults[key])
	if err != nil {
		return s.Defaults[key]
	}
	return value
}

// health reports that the server is running.
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// systemInfo describes the capabilities of the running server.
type systemInfo struct {
	// Version identifies the running build.
	Version string `json:"version"`
	// ImportFormats lists the formats accepted into the library.
	ImportFormats []string `json:"import_formats"`
	// OutputFormats lists the formats books can be converted to.
	OutputFormats []string `json:"output_formats"`
	// DeviceFormats lists the formats the e-reader reads.
	DeviceFormats []string `json:"device_formats"`
	// CanConvert reports whether calibre is installed, which converts
	// between all the import and output formats.
	CanConvert bool `json:"can_convert"`
	// NativeConversions maps the formats the built in converters read
	// to the formats they turn them into without calibre.
	NativeConversions map[string][]string `json:"native_conversions"`
	// CanEmbed reports whether metadata can be written into files.
	CanEmbed bool `json:"can_embed"`
}

// system reports the capabilities of the running server.
func (s *Server) system(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, systemInfo{
		Version:       s.Version,
		ImportFormats: meta.ImportFormats(),
		OutputFormats: convert.OutputFormats(),
		DeviceFormats: device.Formats(),
		CanConvert:    s.Tools.CanConvert(),

		NativeConversions: convert.NativeTargets(),
		CanEmbed:          s.Tools.CanEmbed(),
	})
}

// settings holds the user editable configuration.
type settings struct {
	// MegaFolder is the MEGA folder the library syncs with.
	MegaFolder string `json:"mega_folder"`
	// KindlePath is the mount point of the Kindle; empty means detect it.
	KindlePath string `json:"kindle_path"`
}

// currentSettings returns the effective settings.
func (s *Server) currentSettings() settings {
	return settings{
		MegaFolder: s.setting(SettingMegaFolder),
		KindlePath: s.setting(SettingKindlePath),
	}
}

// getSettings returns the effective settings.
func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.currentSettings())
}

// putSettings updates the settings present in the request body.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	next := s.currentSettings()
	if err := decode(r, &next); err != nil {
		fail(w, err)
		return
	}
	values := map[string]string{
		SettingMegaFolder: megasync.Normalize(next.MegaFolder),
		SettingKindlePath: strings.TrimSpace(next.KindlePath),
	}
	for key, value := range values {
		if err := s.Store.SetSetting(key, value); err != nil {
			fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.currentSettings())
}

// startJob records a job and runs work in the background, storing its
// outcome when it returns.
func (s *Server) startJob(kind, title string, bookID *int64, work func(context.Context) (any, error)) (*store.Job, error) {
	return s.startTrackedJob(kind, title, bookID, func(ctx context.Context, _ func(any)) (any, error) {
		return work(ctx)
	})
}

// startTrackedJob is startJob for work that says how far it has come:
// what it passes to report is shown with the job while it runs.
func (s *Server) startTrackedJob(kind, title string, bookID *int64,
	work func(ctx context.Context, report func(any)) (any, error)) (*store.Job, error) {
	job := &store.Job{Kind: kind, Title: title, BookID: bookID}
	if err := s.Store.CreateJob(job); err != nil {
		return nil, err
	}
	s.jobs.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
		defer cancel()
		defer s.progress.Delete(job.ID)
		result, err := work(ctx, func(progress any) { s.progress.Store(job.ID, progress) })
		if err != nil {
			slog.Warn("job failed", "id", job.ID, "kind", kind, "error", err)
		}
		if err := s.Store.FinishJob(job.ID, result, err); err != nil {
			slog.Error("recording job outcome", "id", job.ID, "error", err)
		}
	})
	return job, nil
}

// jobView is a job as the API shows it: with its progress while it
// runs.
type jobView struct {
	*store.Job
	// Progress says how far a running job has come, when it reports it.
	Progress any `json:"progress,omitempty"`
}

// view returns job as the API shows it.
func (s *Server) view(job *store.Job) jobView {
	v := jobView{Job: job}
	if job.Status == store.JobRunning {
		v.Progress, _ = s.progress.Load(job.ID)
	}
	return v
}

// listJobs returns the most recent background jobs.
func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 || limit > 500 {
		limit = 50
	}
	jobs, err := s.Store.Jobs(r.URL.Query().Get("kind"), limit)
	if err != nil {
		fail(w, err)
		return
	}
	views := make([]jobView, len(jobs))
	for i, job := range jobs {
		views[i] = s.view(job)
	}
	writeJSON(w, http.StatusOK, views)
}

// getJob returns one background job.
func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	job, err := s.Store.Job(id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(job))
}
