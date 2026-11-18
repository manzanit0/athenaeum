package api_test

import (
	"bytes"
	"encoding/json"
	"image/color"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/manzanit0/athenaeum/internal/api"
	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/convert"
	"github.com/manzanit0/athenaeum/internal/device"
	"github.com/manzanit0/athenaeum/internal/library"
	"github.com/manzanit0/athenaeum/internal/megasync"
	"github.com/manzanit0/athenaeum/internal/megasync/megatest"
	"github.com/manzanit0/athenaeum/internal/preview"
	"github.com/manzanit0/athenaeum/internal/store"
	"github.com/manzanit0/athenaeum/internal/testbin"
	"github.com/manzanit0/athenaeum/internal/vault"
)

// sample is the book uploaded by the tests.
var sample = booktest.Book{
	Title:  "Parable of the Sower",
	Author: "Octavia E. Butler",
	Tags:   []string{"Science Fiction"},
	Cover:  booktest.PNG(color.RGBA{G: 120, A: 255}),
}

// env is a running server with fake external tools.
type env struct {
	// t is the test using the server.
	t *testing.T
	// url is the base address of the server.
	url string
	// kindle is the folder standing in for the Kindle storage.
	kindle string
	// remote is the folder standing in for the MEGA drive.
	remote string
	// tools locates the fake calibre tools the server uses.
	tools *convert.Tools
	// cache is the folder holding the preview renditions.
	cache string
	// delay is the file that says how long every change of a sync takes.
	delay string
	// database is the path of the database file.
	database string
}

// newEnv starts a server backed by temporary folders, a fake converter,
// a fake Kindle and a stand-in for MEGA that it is signed in to.
func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{t: t, kindle: filepath.Join(dir, "kindle"), remote: filepath.Join(dir, "mega")}
	e.tools = &convert.Tools{ConvertBin: testbin.Convert(), MetaBin: "no-such-ebook-meta"}
	e.cache = filepath.Join(dir, "cache", "preview")
	e.delay = filepath.Join(dir, "mega-delay")
	e.database = filepath.Join(dir, "test.db")
	for _, d := range []string{filepath.Join(e.kindle, "documents"), e.remote} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(e.database)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := vault.Open(st, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	accounts := &megasync.Accounts{Service: &megatest.Service{Root: e.remote, DelayFile: e.delay}, Secrets: secrets}
	lib, err := library.New(filepath.Join(dir, "library"), filepath.Join(dir, "tmp"), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &api.Server{
		Store:   st,
		Library: lib,
		Tools:   e.tools,
		Kindle: device.New(func() string {
			path, _ := st.Setting(api.SettingKindlePath, e.kindle)
			return path
		}),
		Syncer:   &megasync.Syncer{Accounts: accounts, Store: st, Library: lib},
		Previews: &preview.Cache{Dir: e.cache},
		Defaults: map[string]string{},
		UI:       fstest.MapFS{"index.html": {Data: []byte("<html>shell</html>")}},
		Version:  "test",
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Wait()
		st.Close()
	})
	e.url = ts.URL
	e.signIn(megatest.Email, megatest.Password, "", http.StatusOK)
	return e
}

// signIn signs in to MEGA and checks the status of the answer.
func (e *env) signIn(email, password, code string, want int) {
	e.t.Helper()
	e.do("POST", "/api/sync/session", map[string]string{"email": email, "password": password, "code": code}, want, nil)
}

// do sends a request and decodes the JSON response into out when out is
// not nil. It fails the test unless the status is want.
func (e *env) do(method, path string, body any, want int, out any) {
	e.t.Helper()
	var reader io.Reader
	contentType := "application/json"
	switch b := body.(type) {
	case nil:
	case *upload:
		reader, contentType = b.body, b.contentType
	case []byte:
		reader, contentType = bytes.NewReader(b), "application/octet-stream"
	default:
		data, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, e.url+path, reader)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, resp.StatusCode, want, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			e.t.Fatalf("%s %s: decoding %q: %v", method, path, data, err)
		}
	}
}

// upload is a multipart request body.
type upload struct {
	// body is the encoded form.
	body *bytes.Buffer
	// contentType is the multipart content type with its boundary.
	contentType string
}

// files builds a multipart upload of the named files.
func files(t *testing.T, named map[string][]byte) *upload {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, data := range named {
		part, err := w.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		part.Write(data)
	}
	w.Close()
	return &upload{body: &buf, contentType: w.FormDataContentType()}
}

// importSample uploads the sample EPUB and returns the created book.
func (e *env) importSample() store.Book {
	e.t.Helper()
	var result struct {
		// Books lists the created books.
		Books []store.Book `json:"books"`
	}
	e.do("POST", "/api/books", files(e.t, map[string][]byte{"sower.epub": booktest.EPUB(sample)}), http.StatusCreated, &result)
	if len(result.Books) != 1 {
		e.t.Fatalf("imported %d books, want 1", len(result.Books))
	}
	return result.Books[0]
}

// wait polls the job until it finishes and fails the test if it failed.
func (e *env) wait(job store.Job) store.Job {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		e.do("GET", "/api/jobs/"+itoa(job.ID), nil, http.StatusOK, &job)
		switch job.Status {
		case store.JobDone:
			return job
		case store.JobFailed:
			e.t.Fatalf("job %q failed: %s", job.Title, job.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("job %q did not finish", job.Title)
	return job
}

// itoa formats id as a decimal string.
func itoa(id int64) string {
	data, _ := json.Marshal(id)
	return string(data)
}

// TestLibraryJourney covers importing, searching, editing, converting and
// deleting a book through the API.
func TestLibraryJourney(t *testing.T) {
	e := newEnv(t)
	book := e.importSample()
	path := "/api/books/" + itoa(book.ID)
	if book.Title != sample.Title || book.Cover == "" || len(book.Formats) != 1 {
		t.Fatalf("imported book = %+v", book)
	}

	var rejected struct {
		// Errors lists the rejected files.
		Errors []map[string]string `json:"errors"`
	}
	e.do("POST", "/api/books", files(t, map[string][]byte{"tool.exe": []byte("MZ")}), http.StatusUnprocessableEntity, &rejected)
	if len(rejected.Errors) != 1 {
		t.Errorf("errors = %+v, want one rejected file", rejected.Errors)
	}

	var found []store.Book
	e.do("GET", "/api/books?q=butler+sower", nil, http.StatusOK, &found)
	if len(found) != 1 {
		t.Errorf("search found %d books, want 1", len(found))
	}
	e.do("GET", "/api/books?q=tolkien", nil, http.StatusOK, &found)
	if len(found) != 0 {
		t.Errorf("search found %d books, want 0", len(found))
	}

	patch := map[string]any{"title": "Parable of the Talents", "series": "Earthseed", "series_index": 2, "tags": []string{"Dystopia"}}
	e.do("PATCH", path, patch, http.StatusOK, &book)
	if book.Title != "Parable of the Talents" || book.Series != "Earthseed" || book.Authors[0] != sample.Author {
		t.Errorf("patched book = %+v", book)
	}
	e.do("PATCH", path, map[string]string{"title": ""}, http.StatusBadRequest, nil)

	e.do("PUT", path+"/cover", booktest.PNG(color.RGBA{B: 255, A: 255}), http.StatusOK, &book)
	e.do("PUT", path+"/cover", []byte("nope"), http.StatusBadRequest, nil)
	resp, err := http.Get(e.url + path + "/cover")
	if err != nil || resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("cover response = %v, %v", resp, err)
	}

	var job store.Job
	e.do("POST", path+"/convert", map[string]string{"to": "azw3"}, http.StatusAccepted, &job)
	e.wait(job)
	e.do("GET", path, nil, http.StatusOK, &book)
	if _, ok := book.Format("azw3"); !ok || len(book.Formats) != 2 {
		t.Fatalf("formats after conversion = %+v", book.Formats)
	}
	e.do("POST", path+"/convert", map[string]string{"to": "exe"}, http.StatusBadRequest, nil)

	resp, err = http.Get(e.url + path + "/formats/azw3")
	if err != nil || resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Errorf("download response = %v, %v", resp, err)
	}
	e.do("DELETE", path+"/formats/azw3", nil, http.StatusOK, &book)
	if len(book.Formats) != 1 {
		t.Errorf("formats after delete = %+v", book.Formats)
	}
	e.do("DELETE", path, nil, http.StatusNoContent, nil)
	e.do("GET", path, nil, http.StatusNotFound, nil)
}

// TestDeviceJourney covers sending a book to the Kindle, listing it,
// importing it back and deleting it.
func TestDeviceJourney(t *testing.T) {
	e := newEnv(t)
	book := e.importSample()
	var status device.Status
	e.do("GET", "/api/device", nil, http.StatusOK, &status)
	if !status.Connected {
		t.Fatalf("device status = %+v", status)
	}

	var job store.Job
	e.do("POST", "/api/device/books", map[string]any{"book_id": book.ID}, http.StatusAccepted, &job)
	job = e.wait(job)
	var sent struct {
		// Path is where the book was copied to.
		Path string `json:"path"`
		// Converted reports whether a conversion was needed.
		Converted bool `json:"converted"`
	}
	if err := json.Unmarshal(job.Result, &sent); err != nil || !sent.Converted || !strings.HasSuffix(sent.Path, ".azw3") {
		t.Fatalf("send result = %s, %v", job.Result, err)
	}

	var onDevice []struct {
		// Path is the file path on the Kindle.
		Path string `json:"path"`
		// BookID links the file to a library book.
		BookID *int64 `json:"book_id"`
	}
	e.do("GET", "/api/device/books", nil, http.StatusOK, &onDevice)
	if len(onDevice) != 1 || onDevice[0].BookID == nil || *onDevice[0].BookID != book.ID {
		t.Fatalf("device books = %+v", onDevice)
	}

	loose := filepath.Join(e.kindle, "documents", "Solaris - Stanislaw Lem.mobi")
	if err := os.WriteFile(loose, booktest.MOBI(booktest.Book{Title: "Solaris", Author: "Stanislaw Lem"}), 0o644); err != nil {
		t.Fatal(err)
	}
	var imported store.Book
	e.do("POST", "/api/device/import", map[string]string{"path": "documents/Solaris - Stanislaw Lem.mobi"}, http.StatusCreated, &imported)
	if imported.Title != "Solaris" {
		t.Errorf("imported %q, want Solaris", imported.Title)
	}

	e.do("DELETE", "/api/device/books?path="+url.QueryEscape(sent.Path), nil, http.StatusNoContent, nil)
	e.do("DELETE", "/api/device/books?path=documents/../x.mobi", nil, http.StatusBadRequest, nil)
	e.do("GET", "/api/device/books", nil, http.StatusOK, &onDevice)
	if len(onDevice) != 1 || onDevice[0].Path != "documents/Solaris - Stanislaw Lem.mobi" {
		t.Errorf("device books after delete = %+v", onDevice)
	}

	e.do("PUT", "/api/settings", map[string]string{"kindle_path": filepath.Join(e.kindle, "missing")}, http.StatusOK, nil)
	e.do("GET", "/api/device/books", nil, http.StatusConflict, nil)
}

// TestSyncJourney covers configuring the MEGA folder and syncing.
func TestSyncJourney(t *testing.T) {
	e := newEnv(t)
	book := e.importSample()
	e.do("POST", "/api/sync", map[string]string{"direction": "push"}, http.StatusBadRequest, nil)

	var settings map[string]string
	e.do("PUT", "/api/settings", map[string]string{"mega_folder": "mega://Books/"}, http.StatusOK, &settings)
	if settings["mega_folder"] != "Books" {
		t.Errorf("settings = %+v, want the folder normalised to Books", settings)
	}
	e.do("POST", "/api/sync", map[string]any{"direction": "both", "delete": true}, http.StatusBadRequest, nil)

	var job store.Job
	e.do("POST", "/api/sync", map[string]string{"direction": "both"}, http.StatusAccepted, &job)
	job = e.wait(job)
	var report megasync.Report
	if err := json.Unmarshal(job.Result, &report); err != nil || report.Push == nil || report.Push.Files != 3 {
		t.Fatalf("sync report = %s, %v; want three uploaded files", job.Result, err)
	}
	if _, err := os.Stat(filepath.Join(e.remote, "Books", "_athenaeum", book.UUID+".json")); err != nil {
		t.Errorf("the book was not uploaded: %v", err)
	}

	var info struct {
		// SignedIn reports whether a MEGA account is signed in.
		SignedIn bool `json:"signed_in"`
		// Account is the logged in account.
		Account string `json:"account"`
		// Folder is the configured MEGA folder.
		Folder string `json:"folder"`
		// LastRun is the latest sync job.
		LastRun *store.Job `json:"last_run"`
	}
	e.do("GET", "/api/sync", nil, http.StatusOK, &info)
	if !info.SignedIn || info.Account != megatest.Email || info.Folder != "Books" || info.LastRun == nil || info.LastRun.ID != job.ID {
		t.Errorf("sync info = %+v", info)
	}

	var out struct {
		// Confirmed reports whether MEGA confirmed the end of the session.
		Confirmed bool `json:"confirmed"`
	}
	e.do("DELETE", "/api/sync/session", nil, http.StatusOK, &out)
	e.do("GET", "/api/sync", nil, http.StatusOK, &info)
	if !out.Confirmed || info.SignedIn || info.Account != "" {
		t.Errorf("after signing out: confirmed %t, sync info %+v", out.Confirmed, info)
	}
	e.do("POST", "/api/sync", map[string]string{"direction": "push"}, http.StatusConflict, nil)
	e.signIn(megatest.Email, "not the password", "", http.StatusUnauthorized)
	var failure struct {
		// CodeRequired reports that a two-factor code is needed.
		CodeRequired bool `json:"code_required"`
	}
	e.do("POST", "/api/sync/session", map[string]string{"email": megatest.GuardedEmail, "password": megatest.Password},
		http.StatusUnauthorized, &failure)
	if !failure.CodeRequired {
		t.Error("the guarded account did not ask for a two-factor code")
	}
	e.signIn(megatest.GuardedEmail, megatest.Password, megatest.Code, http.StatusOK)
	e.do("GET", "/api/sync", nil, http.StatusOK, &info)
	if !info.SignedIn || info.Account != megatest.GuardedEmail {
		t.Errorf("after signing in again, sync info = %+v", info)
	}
	raw, err := os.ReadFile(e.database)
	if err != nil || bytes.Contains(raw, []byte(megatest.Password)) || bytes.Contains(raw, []byte("master key")) {
		t.Errorf("the database holds the password or the session in the clear: %v", err)
	}
	e.do("POST", "/api/sync", map[string]string{"direction": "push"}, http.StatusAccepted, &job)
	e.wait(job)
}

// TestStaticAndUnknownRoutes checks the web interface fallback and the
// API not found response.
func TestStaticAndUnknownRoutes(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/", "/books/12", "/device"} {
		resp, err := http.Get(e.url + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "shell") {
			t.Errorf("GET %s = %d %q, want the application shell", path, resp.StatusCode, body)
		}
	}
	e.do("GET", "/api/nope", nil, http.StatusNotFound, nil)
	e.do("GET", "/api/health", nil, http.StatusOK, nil)
	var system map[string]any
	e.do("GET", "/api/system", nil, http.StatusOK, &system)
	if system["can_convert"] != true || system["can_embed"] != false {
		t.Errorf("system = %+v", system)
	}
}
