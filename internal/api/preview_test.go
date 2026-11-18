package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/preview"
	"github.com/manzanit0/athenaeum/internal/store"
)

// previewDoc is the description of a preview returned by the API.
type previewDoc struct {
	// Kind is one of epub, pdf or text.
	Kind string `json:"kind"`
	// Format is the format of the file being shown.
	Format string `json:"format"`
	// Converted reports whether the file was converted to EPUB first.
	Converted bool `json:"converted"`
	// Base is the address of the content.
	Base string `json:"base"`
	// Sections lists the documents in reading order.
	Sections []preview.Section `json:"sections"`
	// Contents lists the lines of the table of contents.
	Contents []preview.Entry `json:"contents"`
}

// almanac is a book with several chapters.
var almanac = booktest.Book{
	Title:    "The Tidewater Almanac",
	Author:   "Ines Marlow",
	Chapters: []string{"Spring Tides", "Neap Tides", "Storm Surge"},
}

// importFile uploads one file and returns the created book.
func (e *env) importFile(name string, data []byte) store.Book {
	e.t.Helper()
	var result struct {
		// Books lists the created books.
		Books []store.Book `json:"books"`
	}
	e.do("POST", "/api/books", files(e.t, map[string][]byte{name: data}), http.StatusCreated, &result)
	if len(result.Books) != 1 {
		e.t.Fatalf("imported %d books, want 1", len(result.Books))
	}
	return result.Books[0]
}

// fetch returns the response and body of a GET request for path.
func (e *env) fetch(path string) (*http.Response, string) {
	e.t.Helper()
	resp, err := http.Get(e.url + path)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return resp, string(body)
}

// TestPreviewEPUB covers reading the sections of an EPUB through the API.
func TestPreviewEPUB(t *testing.T) {
	e := newEnv(t)
	book := e.importFile("almanac.epub", booktest.EPUB(almanac))
	path := "/api/books/" + itoa(book.ID) + "/preview"

	var doc previewDoc
	e.do("GET", path, nil, http.StatusOK, &doc)
	if doc.Kind != "epub" || doc.Format != "epub" || doc.Converted || doc.Base != path+"/epub/" {
		t.Fatalf("preview = %+v", doc)
	}
	if len(doc.Sections) != 3 || doc.Sections[1].Title != "Neap Tides" || len(doc.Contents) != 3 {
		t.Fatalf("sections = %+v, contents = %+v", doc.Sections, doc.Contents)
	}

	resp, body := e.fetch(doc.Base + doc.Sections[1].Href)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/xhtml+xml" {
		t.Errorf("section response = %s, %q", resp.Status, resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'none'") {
		t.Errorf("policy = %q, want scripts disabled", resp.Header.Get("Content-Security-Policy"))
	}
	if !strings.Contains(body, "<h2>Neap Tides</h2>") {
		t.Errorf("section body = %q", body)
	}

	e.do("GET", doc.Base+"OEBPS/missing.xhtml", nil, http.StatusNotFound, nil)
	e.do("GET", path+"?format=mobi", nil, http.StatusNotFound, nil)
	e.do("GET", "/api/books/999/preview", nil, http.StatusNotFound, nil)
}

// TestPreviewNativeFormats covers the formats the browser shows itself.
func TestPreviewNativeFormats(t *testing.T) {
	e := newEnv(t)
	book := e.importFile("handbook.pdf", booktest.PDF(almanac))
	path := "/api/books/" + itoa(book.ID)

	var doc previewDoc
	e.do("GET", path+"/preview", nil, http.StatusOK, &doc)
	if doc.Kind != "pdf" || doc.Format != "pdf" || len(doc.Sections) != 0 {
		t.Fatalf("preview = %+v", doc)
	}
	resp, body := e.fetch(doc.Base)
	if resp.Header.Get("Content-Type") != "application/pdf" || resp.Header.Get("Content-Disposition") != "inline" {
		t.Errorf("pdf headers = %v", resp.Header)
	}
	if !strings.HasPrefix(body, "%PDF-") {
		t.Errorf("pdf body starts with %.10q", body)
	}

	e.do("POST", path+"/formats", files(t, map[string][]byte{"notes.txt": []byte("Low water at dawn.")}), http.StatusCreated, nil)
	e.do("GET", path+"/preview?format=txt", nil, http.StatusOK, &doc)
	if doc.Kind != "text" {
		t.Fatalf("preview = %+v", doc)
	}
	resp, body = e.fetch(doc.Base)
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") || body != "Low water at dawn." {
		t.Errorf("text response = %q, %q", resp.Header.Get("Content-Type"), body)
	}
}

// TestPreviewConverted covers books that are converted to EPUB before
// they can be shown.
func TestPreviewConverted(t *testing.T) {
	e := newEnv(t)
	rendition := filepath.Join(t.TempDir(), "rendition.epub")
	if err := os.WriteFile(rendition, booktest.EPUB(almanac), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CONVERT_EPUB", rendition)
	book := e.importFile("almanac.mobi", booktest.MOBI(almanac))
	path := "/api/books/" + itoa(book.ID)

	e.do("GET", path+"/preview/mobi/OEBPS/chapter1.xhtml", nil, http.StatusNotFound, nil)

	var job store.Job
	converter := e.tools.ConvertBin
	e.tools.ConvertBin = "no-such-ebook-convert"
	e.do("GET", path+"/preview", nil, http.StatusAccepted, &job)
	if reason := e.waitFailed(job); !strings.Contains(reason, "only calibre can read") {
		t.Errorf("preview of an old MOBI file without calibre failed with %q", reason)
	}
	e.tools.ConvertBin = converter

	e.do("GET", path+"/preview", nil, http.StatusAccepted, &job)
	if job.Kind != "preview" {
		t.Fatalf("job = %+v", job)
	}
	e.wait(job)

	var doc previewDoc
	e.do("GET", path+"/preview", nil, http.StatusOK, &doc)
	if doc.Kind != "epub" || doc.Format != "mobi" || !doc.Converted || len(doc.Sections) != 3 {
		t.Fatalf("preview = %+v", doc)
	}
	if resp, body := e.fetch(doc.Base + doc.Sections[2].Href); resp.StatusCode != http.StatusOK || !strings.Contains(body, "Storm Surge") {
		t.Errorf("section response = %s, %q", resp.Status, body)
	}

	if kept, _ := os.ReadDir(e.cache); len(kept) != 1 {
		t.Errorf("cache holds %d files, want 1", len(kept))
	}
	e.do("DELETE", path, nil, http.StatusNoContent, nil)
	if kept, _ := os.ReadDir(e.cache); len(kept) != 0 {
		t.Errorf("cache holds %d files after the book was deleted, want 0", len(kept))
	}
}

// TestPreviewKindleBook covers reading an AZW3 book without calibre.
func TestPreviewKindleBook(t *testing.T) {
	e := newEnv(t)
	e.tools.ConvertBin = "no-such-ebook-convert"
	book := e.importFile("almanac.epub", booktest.EPUB(almanac))
	path := "/api/books/" + itoa(book.ID)
	var job store.Job
	e.do("POST", path+"/convert", map[string]string{"to": "azw3"}, http.StatusAccepted, &job)
	e.wait(job)
	e.do("DELETE", path+"/formats/epub", nil, http.StatusOK, nil)

	e.do("GET", path+"/preview", nil, http.StatusAccepted, &job)
	e.wait(job)
	var doc previewDoc
	e.do("GET", path+"/preview", nil, http.StatusOK, &doc)
	if doc.Kind != "epub" || doc.Format != "azw3" || !doc.Converted || len(doc.Sections) != 3 {
		t.Fatalf("preview = %+v", doc)
	}
	if doc.Sections[1].Title != "Neap Tides" || len(doc.Contents) != 3 {
		t.Errorf("sections = %+v, contents = %+v", doc.Sections, doc.Contents)
	}
	if resp, body := e.fetch(doc.Base + doc.Sections[2].Href); resp.StatusCode != http.StatusOK || !strings.Contains(body, "<h2>Storm Surge</h2>") {
		t.Errorf("section response = %s, %q", resp.Status, body)
	}

	e.do("POST", path+"/convert", map[string]string{"to": "epub"}, http.StatusAccepted, &job)
	job = e.wait(job)
	var result conversionResult
	if err := json.Unmarshal(job.Result, &result); err != nil || result.Engine != "native" {
		t.Fatalf("AZW3 to EPUB result = %s, %v", job.Result, err)
	}
	e.do("GET", path+"/preview", nil, http.StatusOK, &doc)
	if doc.Format != "epub" || doc.Converted || len(doc.Sections) != 3 {
		t.Errorf("preview after converting back = %+v", doc)
	}
}
