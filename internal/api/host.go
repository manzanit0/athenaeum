package api

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/manzanit0/athenaeum/internal/home"
)

// Codes that tell the interface what kind of refusal an error is.
const (
	// codeNoLibrary marks a request that needs a library when none is open.
	codeNoLibrary = "no_library"
	// codeBusy marks a change of library refused because a job is running.
	codeBusy = "busy"
)

// States of a library in the list.
const (
	// stateOpen is the library being served.
	stateOpen = "open"
	// stateClosed is a library whose folder is there and is not open.
	stateClosed = "closed"
	// stateMissing is a library whose folder cannot be found.
	stateMissing = "missing"
)

// defaultName is what the library in the home folder is called. Its
// folder was not chosen by the reader, so the folder's name says little.
const defaultName = "Default library"

// errBusy is returned when changing library would cut a job short and
// the request did not ask for that.
var errBusy = errors.New("something is still running in the open library")

// refusal is an error worded for the reader: the reason a folder
// cannot be opened as a library.
type refusal string

// Error returns the reason.
func (r refusal) Error() string {
	return string(r)
}

// Host serves the web interface, the list of libraries and the API of
// the library that is open. One library is open at a time; the host
// moves from one to another without the process restarting, and runs
// with none when the one it should open cannot be.
type Host struct {
	// Home remembers the libraries and which one to open.
	Home *home.Home
	// Open builds the server of the library in a folder that exists and
	// returns, with it, what lets go of the library's files.
	Open func(dir string) (*Server, func(), error)
	// UI is the built web interface.
	UI fs.FS
	// switching lets one change of library happen at a time.
	switching sync.Mutex
	// mu guards current and problem. Requests to the open library hold
	// it for reading, so a change of library waits for them to finish.
	mu sync.RWMutex
	// current is the open library, or nil when there is none.
	current *openLibrary
	// problem says why no library is open, when one should be.
	problem *Problem
}

// openLibrary is a library being served.
type openLibrary struct {
	// path is the library's folder.
	path string
	// server holds the library's handlers and background jobs.
	server *Server
	// handler answers the library's API requests.
	handler http.Handler
	// release lets go of the library's files.
	release func()
}

// close ends the library's jobs and lets go of its files.
func (l *openLibrary) close() {
	l.server.Stop()
	l.release()
}

// Problem says why no library is open.
type Problem struct {
	// Path is the folder of the library that could not be opened.
	Path string `json:"path"`
	// Missing reports whether the folder could not be found, which is
	// what an unplugged drive looks like.
	Missing bool `json:"missing"`
	// Detail is the reason in words.
	Detail string `json:"detail"`
}

// libraryView is a known library as the API shows it.
type libraryView struct {
	// Path is the library's folder.
	Path string `json:"path"`
	// Name is the name of that folder, or defaultName for the home.
	Name string `json:"name"`
	// State is open, closed or missing.
	State string `json:"state"`
}

// librariesView is the answer to every request about the libraries.
type librariesView struct {
	// Home is the folder of the default library.
	Home string `json:"home"`
	// Current is the folder of the open library, or empty for none.
	Current string `json:"current"`
	// Problem says why no library is open, when one should be.
	Problem *Problem `json:"problem,omitempty"`
	// Libraries lists the known libraries, oldest first.
	Libraries []libraryView `json:"libraries"`
}

// openRequest asks for the library in a folder to be opened.
type openRequest struct {
	// Path is the folder, as typed.
	Path string `json:"path"`
	// Create allows a library to be started in a folder that holds
	// other files, or that does not exist yet.
	Create bool `json:"create"`
	// Force allows a job running in the open library to be cut short.
	Force bool `json:"force"`
}

// Start opens the library the home names. When it cannot be opened the
// host keeps running without a library and remembers why; a folder that
// has gone missing is not created again, so that an unplugged drive
// does not turn into an empty library.
func (h *Host) Start() {
	path := h.Home.Current()
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err != nil {
		h.problem = &Problem{Path: path, Missing: true, Detail: "the folder cannot be found"}
		return
	}
	opened, err := h.open(path)
	if err != nil {
		h.problem = &Problem{Path: path, Detail: err.Error()}
		return
	}
	h.current = opened
}

// Close ends the open library's jobs and lets go of its files.
func (h *Host) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil {
		h.current.close()
		h.current = nil
	}
}

// Handler returns the HTTP handler serving the web interface, the
// libraries and the open library's API.
func (h *Host) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", h.health)
	mux.HandleFunc("GET /api/libraries", h.list)
	mux.HandleFunc("POST /api/libraries", h.change)
	mux.HandleFunc("DELETE /api/libraries", h.forget)
	mux.HandleFunc("GET /api/libraries/check", h.check)
	mux.HandleFunc("GET /api/libraries/folders", h.folders)
	mux.HandleFunc("/api/", h.library)
	mux.Handle("/", serveUI(h.UI))
	return logRequests(mux)
}

// health reports that the server is running, with or without a library.
func (h *Host) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// library hands a request to the open library, or says there is none.
func (h *Host) library(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.current == nil {
		writeCoded(w, http.StatusServiceUnavailable, codeNoLibrary, "no library is open")
		return
	}
	h.current.handler.ServeHTTP(w, r)
}

// list returns the known libraries and which one is open.
func (h *Host) list(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.view())
}

// check says what is at a typed path, without changing anything.
func (h *Host) check(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, home.Inspect(r.URL.Query().Get("path")))
}

// folders suggests the folders a typed path could go on to name.
func (h *Host) folders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, home.Suggest(r.URL.Query().Get("path")))
}

// change opens the library in the folder the request names and closes
// the one that was open.
func (h *Host) change(w http.ResponseWriter, r *http.Request) {
	var req openRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	var refused refusal
	switch err := h.Switch(req.Path, req.Create, req.Force); {
	case errors.Is(err, errBusy):
		writeCoded(w, http.StatusConflict, codeBusy, err.Error())
	case errors.As(err, &refused):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		fail(w, err)
	default:
		writeJSON(w, http.StatusOK, h.view())
	}
}

// forget removes a library from the list and leaves its folder alone.
func (h *Host) forget(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	h.switching.Lock()
	defer h.switching.Unlock()
	if h.openPath() == path {
		writeError(w, http.StatusConflict, home.ErrOpen.Error())
		return
	}
	if err := h.Home.Forget(path); err != nil {
		fail(w, err)
		return
	}
	h.forgetProblem(path)
	writeJSON(w, http.StatusOK, h.view())
}

// Switch opens the library in the folder at a typed path and closes the
// one that was open. The new library is opened first, so a folder that
// will not open leaves the previous library in place. With create, a
// library is started in a folder that holds other files or does not
// exist yet; with force, a job running in the open library is cut short.
func (h *Host) Switch(typed string, create, force bool) error {
	h.switching.Lock()
	defer h.switching.Unlock()
	check := home.Inspect(typed)
	if err := allowed(check, create); err != nil {
		return err
	}
	previous := h.snapshot()
	if previous != nil && previous.path == check.Path {
		return nil
	}
	if previous != nil && previous.server.Busy() && !force {
		return errBusy
	}
	if check.Kind == home.KindNew {
		if err := os.Mkdir(check.Path, 0o755); err != nil {
			return err
		}
	}
	opened, err := h.open(check.Path)
	if err != nil {
		return fmt.Errorf("the library could not be opened: %w", err)
	}
	h.mu.Lock()
	h.current, h.problem = opened, nil
	h.mu.Unlock()
	if previous != nil {
		previous.close()
	}
	return h.Home.Use(check.Path)
}

// allowed reports whether a library may be opened at what check found.
// A folder that holds a library, or nothing, always may; one that holds
// other files, or does not exist yet, only when the request asks for a
// library to be created.
func allowed(check home.Check, create bool) error {
	switch check.Kind {
	case home.KindLibrary, home.KindEmpty:
		return nil
	case home.KindOther:
		if create {
			return nil
		}
		return refusal("the folder holds no library")
	case home.KindNew:
		if create {
			return nil
		}
	}
	if _, err := os.Stat(check.Path); err != nil && !create {
		return refusal("the folder cannot be found")
	}
	return refusal(check.Detail)
}

// open builds the server of the library in path.
func (h *Host) open(path string) (*openLibrary, error) {
	server, release, err := h.Open(path)
	if err != nil {
		return nil, err
	}
	return &openLibrary{path: path, server: server, handler: server.routes(), release: release}, nil
}

// snapshot returns the open library, or nil.
func (h *Host) snapshot() *openLibrary {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.current
}

// openPath returns the folder of the open library, or an empty string.
func (h *Host) openPath() string {
	if current := h.snapshot(); current != nil {
		return current.path
	}
	return ""
}

// forgetProblem drops the problem when it is about the library in path.
func (h *Host) forgetProblem(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.problem != nil && h.problem.Path == path {
		h.problem = nil
	}
}

// view describes the libraries as the API shows them.
func (h *Host) view() librariesView {
	h.mu.RLock()
	current, problem := h.current, h.problem
	h.mu.RUnlock()
	view := librariesView{Home: h.Home.Dir(), Problem: problem, Libraries: []libraryView{}}
	if current != nil {
		view.Current = current.path
	}
	for _, path := range h.Home.Libraries() {
		view.Libraries = append(view.Libraries, libraryView{
			Path: path, Name: h.nameOf(path), State: stateOf(path, view.Current),
		})
	}
	return view
}

// nameOf returns what the library in path is called.
func (h *Host) nameOf(path string) string {
	if path == h.Home.Dir() {
		return defaultName
	}
	return filepath.Base(path)
}

// stateOf says whether the library in path is open, closed or missing.
func stateOf(path, current string) string {
	if path == current {
		return stateOpen
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return stateMissing
	}
	return stateClosed
}

// writeCoded writes a JSON error response that carries a code the
// interface can act on.
func writeCoded(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error": message, "code": code})
}
