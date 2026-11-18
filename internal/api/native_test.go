package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/store"
)

// conversionResult is the outcome of a conversion job.
type conversionResult struct {
	// Book is the book with its new file.
	Book store.Book `json:"book"`
	// Engine is the engine that did the conversion.
	Engine string `json:"engine"`
	// Warnings lists what had to be left out.
	Warnings []string `json:"warnings"`
}

// TestNativeConversion covers converting and sending a book on a machine
// without calibre.
func TestNativeConversion(t *testing.T) {
	e := newEnv(t)
	e.tools.ConvertBin = "no-such-ebook-convert"
	var system struct {
		// CanConvert reports whether calibre is installed.
		CanConvert bool `json:"can_convert"`
		// NativeConversions lists what converts without calibre.
		NativeConversions map[string][]string `json:"native_conversions"`
	}
	e.do("GET", "/api/system", nil, http.StatusOK, &system)
	if system.CanConvert || strings.Join(system.NativeConversions["epub"], ",") != "azw3" ||
		strings.Join(system.NativeConversions["azw3"], ",") != "epub" ||
		strings.Join(system.NativeConversions["pdf"], ",") != "azw3,epub" {
		t.Fatalf("system = %+v", system)
	}

	book := e.importFile("almanac.epub", booktest.EPUB(almanac))
	path := "/api/books/" + itoa(book.ID)
	e.do("POST", path+"/convert", map[string]string{"to": "mobi"}, http.StatusServiceUnavailable, nil)
	e.do("POST", path+"/convert", map[string]string{"to": "pdf", "from": "epub"}, http.StatusServiceUnavailable, nil)

	var job store.Job
	e.do("POST", path+"/convert", map[string]string{"to": "azw3"}, http.StatusAccepted, &job)
	job = e.wait(job)
	var result conversionResult
	if err := json.Unmarshal(job.Result, &result); err != nil || result.Engine != "native" || result.Warnings == nil {
		t.Fatalf("conversion result = %s, %v", job.Result, err)
	}
	file, ok := result.Book.Format("azw3")
	if !ok {
		t.Fatalf("formats after conversion = %+v", result.Book.Formats)
	}
	resp, body := e.fetch(path + "/formats/azw3")
	if resp.StatusCode != http.StatusOK || len(body) < 100 || body[60:68] != "BOOKMOBI" {
		t.Fatalf("converted file: %s, %d bytes", resp.Status, len(body))
	}
	copied := filepath.Join(t.TempDir(), file.Filename)
	if err := os.WriteFile(copied, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, _ := meta.Read(copied, file.Filename); m.Title != almanac.Title || m.Authors[0] != almanac.Author {
		t.Errorf("metadata of the converted file = %+v", m)
	}

	other := e.importFile("sower.epub", booktest.EPUB(sample))
	e.do("POST", "/api/device/books", map[string]any{"book_id": other.ID}, http.StatusAccepted, &job)
	e.wait(job)
	sent, err := os.ReadDir(filepath.Join(e.kindle, "documents"))
	if err != nil || len(sent) != 1 || !strings.HasSuffix(sent[0].Name(), ".azw3") {
		t.Errorf("files on the Kindle = %v, %v; want one AZW3", sent, err)
	}
}

// waitFailed polls the job until it fails and returns its error.
func (e *env) waitFailed(job store.Job) string {
	e.t.Helper()
	for range 500 {
		e.do("GET", "/api/jobs/"+itoa(job.ID), nil, http.StatusOK, &job)
		if job.Status != store.JobRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != store.JobFailed {
		e.t.Fatalf("job %q ended as %s, want it to fail", job.Title, job.Status)
	}
	return job.Error
}

// TestNativePDFConversion covers turning a PDF into an EPUB and an AZW3
// without calibre, and the refusal of a PDF without a text layer.
func TestNativePDFConversion(t *testing.T) {
	e := newEnv(t)
	e.tools.ConvertBin = "no-such-ebook-convert"
	book := e.importFile("tables.pdf", booktest.TextPDF(almanac))
	path := "/api/books/" + itoa(book.ID)

	var job store.Job
	var result conversionResult
	e.do("POST", path+"/convert", map[string]string{"to": "azw3"}, http.StatusAccepted, &job)
	job = e.wait(job)
	if err := json.Unmarshal(job.Result, &result); err != nil || result.Engine != "native" || len(result.Book.Formats) != 2 {
		t.Fatalf("PDF to AZW3 result = %s, %v", job.Result, err)
	}
	if _, body := e.fetch(path + "/formats/azw3"); len(body) < 100 || body[60:68] != "BOOKMOBI" {
		t.Errorf("PDF to AZW3 wrote %d bytes that are not a Kindle book", len(body))
	}

	e.do("POST", path+"/convert", map[string]string{"to": "epub", "from": "pdf"}, http.StatusAccepted, &job)
	e.wait(job)
	var doc previewDoc
	e.do("GET", path+"/preview", nil, http.StatusOK, &doc)
	if doc.Kind != "epub" || doc.Format != "epub" || len(doc.Sections) == 0 {
		t.Fatalf("preview of the converted book = %+v", doc)
	}
	var titles []string
	for _, entry := range doc.Contents {
		titles = append(titles, entry.Title)
	}
	if got := strings.Join(titles, "|"); !strings.Contains(got, "Spring Tides|Neap Tides|Storm Surge") {
		t.Errorf("contents of the converted book = %s", got)
	}
	text := ""
	for _, section := range doc.Sections {
		_, body := e.fetch(doc.Base + section.Href)
		text += body
	}
	if strings.Count(text, "It was a quiet morning in the library, and the tide tables lay open on the long desk") != 3 {
		t.Errorf("the paragraphs were not joined back together: %s", text)
	}

	scan := e.importFile("scan.pdf", booktest.PDF(almanac))
	e.do("POST", "/api/books/"+itoa(scan.ID)+"/convert", map[string]string{"to": "epub"}, http.StatusAccepted, &job)
	if reason := e.waitFailed(job); !strings.Contains(reason, "look like scans") {
		t.Errorf("a PDF without text failed with %q, want it to say the pages look like scans", reason)
	}
}
