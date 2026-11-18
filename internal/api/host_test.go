package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/manzanit0/athenaeum/internal/home"
	"github.com/manzanit0/athenaeum/internal/library"
	"github.com/manzanit0/athenaeum/internal/store"
)

// hostEnv is a host under test with the address it is served on.
type hostEnv struct {
	// t is the running test.
	t *testing.T
	// host is the host under test.
	host *Host
	// url is the address of the test server.
	url string
}

// openForTest builds a library server with just what the tests use.
func openForTest(dir string) (*Server, func(), error) {
	st, err := store.Open(filepath.Join(dir, home.Catalogue))
	if err != nil {
		return nil, nil, err
	}
	lib, err := library.New(filepath.Join(dir, "library"), filepath.Join(dir, "tmp"), st, nil)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	return &Server{Store: st, Library: lib, Defaults: map[string]string{}, Version: "test"}, func() { st.Close() }, nil
}

// newHostEnv starts a host on the home folder dir.
func newHostEnv(t *testing.T, dir string) *hostEnv {
	t.Helper()
	known, err := home.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	host := &Host{Home: known, Open: openForTest, UI: fstest.MapFS{"index.html": {Data: []byte("shell")}}}
	host.Start()
	ts := httptest.NewServer(host.Handler())
	t.Cleanup(func() {
		ts.Close()
		host.Close()
	})
	return &hostEnv{t: t, host: host, url: ts.URL}
}

// do sends a request, checks the status and decodes the answer into out.
func (e *hostEnv) do(method, path string, body any, status int, out any) {
	e.t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			e.t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, e.url+path, &payload)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		e.t.Fatalf("%s %s answered %d, want %d", method, path, resp.StatusCode, status)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			e.t.Fatal(err)
		}
	}
}

// libraries returns the list of libraries as the API shows it.
func (e *hostEnv) libraries() librariesView {
	e.t.Helper()
	var view librariesView
	e.do("GET", "/api/libraries", nil, http.StatusOK, &view)
	return view
}

// TestHostOpensTheHomeFirst checks that a fresh home is a library.
func TestHostOpensTheHomeFirst(t *testing.T) {
	e := newHostEnv(t, t.TempDir())
	view := e.libraries()
	if view.Current != view.Home || len(view.Libraries) != 1 || view.Libraries[0].State != stateOpen {
		t.Fatalf("a fresh home shows %+v", view)
	}
	if view.Libraries[0].Name != defaultName {
		t.Fatalf("the home is called %q", view.Libraries[0].Name)
	}
	e.do("GET", "/api/books", nil, http.StatusOK, nil)
	e.do("GET", "/", nil, http.StatusOK, nil)
}

// TestHostSwitchesAndForgets checks the life of a second library: made,
// opened, left, removed from the list, and still on disk afterwards.
func TestHostSwitchesAndForgets(t *testing.T) {
	e := newHostEnv(t, t.TempDir())
	first := e.libraries().Home
	second := filepath.Join(t.TempDir(), "novels")
	e.do("POST", "/api/libraries", openRequest{Path: second}, http.StatusBadRequest, nil)
	var view librariesView
	e.do("POST", "/api/libraries", openRequest{Path: second, Create: true}, http.StatusOK, &view)
	if view.Current != second || len(view.Libraries) != 2 || view.Libraries[1].Name != "novels" {
		t.Fatalf("after opening a new library: %+v", view)
	}
	if _, err := os.Stat(filepath.Join(second, home.Catalogue)); err != nil {
		t.Fatalf("the new library has no catalogue: %v", err)
	}
	e.do("DELETE", "/api/libraries?path="+second, nil, http.StatusConflict, nil)
	e.do("POST", "/api/libraries", openRequest{Path: first}, http.StatusOK, &view)
	if view.Current != first || view.Libraries[1].State != stateClosed {
		t.Fatalf("after going back: %+v", view)
	}
	e.do("DELETE", "/api/libraries?path="+second, nil, http.StatusOK, &view)
	if len(view.Libraries) != 1 {
		t.Fatalf("after forgetting: %+v", view)
	}
	if _, err := os.Stat(filepath.Join(second, home.Catalogue)); err != nil {
		t.Fatalf("forgetting touched the folder: %v", err)
	}
}

// TestHostAsksBeforeStartingBesideOtherFiles checks that a folder of
// other files only becomes a library when asked, and keeps its files.
func TestHostAsksBeforeStartingBesideOtherFiles(t *testing.T) {
	e := newHostEnv(t, t.TempDir())
	folder := t.TempDir()
	novel := filepath.Join(folder, "novel.epub")
	if err := os.WriteFile(novel, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/api/libraries", openRequest{Path: folder}, http.StatusBadRequest, nil)
	if e.libraries().Current == folder {
		t.Fatal("the folder was opened without asking")
	}
	e.do("POST", "/api/libraries", openRequest{Path: folder, Create: true}, http.StatusOK, nil)
	if _, err := os.Stat(novel); err != nil {
		t.Fatalf("the file that was there is gone: %v", err)
	}
}

// TestHostKeepsTheLibraryWhenAnotherFails checks that a folder that
// will not open leaves the open library in place.
func TestHostKeepsTheLibraryWhenAnotherFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("the superuser can write anywhere")
	}
	e := newHostEnv(t, t.TempDir())
	locked := t.TempDir()
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/api/libraries", openRequest{Path: locked}, http.StatusInternalServerError, nil)
	view := e.libraries()
	if view.Current != view.Home || len(view.Libraries) != 1 {
		t.Fatalf("after a failed change: %+v", view)
	}
	e.do("GET", "/api/books", nil, http.StatusOK, nil)
}

// TestHostRunsWithoutAMissingLibrary checks that a library whose folder
// is gone is neither created again nor replaced, and that the host says
// so until another library is opened.
func TestHostRunsWithoutAMissingLibrary(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(t.TempDir(), "unplugged")
	known, err := home.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := known.Use(gone); err != nil {
		t.Fatal(err)
	}
	e := newHostEnv(t, dir)
	view := e.libraries()
	if view.Current != "" || view.Problem == nil || !view.Problem.Missing || view.Problem.Path != gone {
		t.Fatalf("with the library gone: %+v", view)
	}
	if view.Libraries[1].State != stateMissing {
		t.Fatalf("the missing library is shown as %q", view.Libraries[1].State)
	}
	var refusal map[string]string
	e.do("GET", "/api/books", nil, http.StatusServiceUnavailable, &refusal)
	if refusal["code"] != codeNoLibrary {
		t.Fatalf("the refusal carries the code %q", refusal["code"])
	}
	e.do("GET", "/api/health", nil, http.StatusOK, nil)
	e.do("POST", "/api/libraries", openRequest{Path: gone}, http.StatusBadRequest, nil)
	if _, err := os.Stat(gone); err == nil {
		t.Fatal("the missing folder was created again")
	}
	e.do("POST", "/api/libraries", openRequest{Path: view.Home}, http.StatusOK, nil)
	if after := e.libraries(); after.Current != after.Home || after.Problem != nil {
		t.Fatalf("after opening the default library: %+v", after)
	}
}

// TestHostAsksBeforeCuttingAJobShort checks that a running job blocks a
// change of library until the request insists, and is then ended.
func TestHostAsksBeforeCuttingAJobShort(t *testing.T) {
	e := newHostEnv(t, t.TempDir())
	ended := make(chan struct{})
	_, err := e.host.snapshot().server.startJob("test", "A long job", nil, func(ctx context.Context) (any, error) {
		<-ctx.Done()
		close(ended)
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	second := t.TempDir()
	var refusal map[string]string
	e.do("POST", "/api/libraries", openRequest{Path: second}, http.StatusConflict, &refusal)
	if refusal["code"] != codeBusy {
		t.Fatalf("the refusal carries the code %q", refusal["code"])
	}
	e.do("POST", "/api/libraries", openRequest{Path: second, Force: true}, http.StatusOK, nil)
	select {
	case <-ended:
	default:
		t.Fatal("the job was left running")
	}
}
