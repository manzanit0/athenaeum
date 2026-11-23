package api_test

import (
	"encoding/json"
	"image/color"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/store"
)

// logbook is a Word document with several chapters and its details in
// its properties.
var logbook = booktest.Book{
	Title:       "The Harbour Logbook",
	Author:      "Edda Voss",
	Language:    "en",
	Description: "A season of arrivals and departures.",
	Tags:        []string{"Harbours", "Logbooks"},
	Chapters:    []string{"Spring Tides", "Neap Tides", "Storm Surge"},
	Cover:       booktest.JPEG(color.RGBA{R: 40, G: 80, B: 120, A: 255}),
}

// TestWordDocument covers a Word document on a machine without
// calibre: its details and preview picture are read on import, and it
// is converted, read and sent to the Kindle by the built in converters.
func TestWordDocument(t *testing.T) {
	e := newEnv(t)
	e.tools.ConvertBin = "no-such-ebook-convert"
	var system struct {
		// NativeConversions lists what converts without calibre.
		NativeConversions map[string][]string `json:"native_conversions"`
	}
	e.do("GET", "/api/system", nil, http.StatusOK, &system)
	if strings.Join(system.NativeConversions["docx"], ",") != "azw3,epub" {
		t.Fatalf("native conversions of docx = %q", system.NativeConversions["docx"])
	}

	book := e.importFile("some file name.docx", booktest.DOCX(logbook))
	if book.Title != logbook.Title || !reflect.DeepEqual(book.Authors, []string{logbook.Author}) ||
		!reflect.DeepEqual(book.Tags, logbook.Tags) || book.Description != logbook.Description ||
		book.Language != "en" || book.Cover == "" {
		t.Fatalf("imported book = %+v, want the details and the picture of the document", book)
	}
	path := "/api/books/" + itoa(book.ID)

	var job store.Job
	e.do("POST", path+"/convert", map[string]string{"to": "epub"}, http.StatusAccepted, &job)
	job = e.wait(job)
	var result conversionResult
	if err := json.Unmarshal(job.Result, &result); err != nil || result.Engine != "native" || len(result.Warnings) != 0 {
		t.Fatalf("conversion result = %s, %v", job.Result, err)
	}
	if _, ok := result.Book.Format("epub"); !ok {
		t.Fatalf("formats after conversion = %+v", result.Book.Formats)
	}
	e.do("DELETE", path+"/formats/epub", nil, http.StatusOK, nil)

	var doc previewDoc
	e.do("GET", path+"/preview", nil, http.StatusAccepted, &job)
	e.wait(job)
	e.do("GET", path+"/preview", nil, http.StatusOK, &doc)
	if doc.Kind != "epub" || doc.Format != "docx" || !doc.Converted || len(doc.Sections) != 3 ||
		len(doc.Contents) != 3 || doc.Contents[2].Title != "Storm Surge" {
		t.Fatalf("preview = %+v, want the three chapters of the document", doc)
	}
	resp, body := e.fetch(doc.Base + doc.Sections[1].Href)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Neap Tides") {
		t.Errorf("second section: %s, %q", resp.Status, body)
	}

	e.do("POST", "/api/device/books", map[string]any{"book_id": book.ID}, http.StatusAccepted, &job)
	e.wait(job)
	var sent store.Book
	e.do("GET", path, nil, http.StatusOK, &sent)
	if _, ok := sent.Format("azw3"); !ok {
		t.Errorf("formats after sending = %+v, want an AZW3 made on the way", sent.Formats)
	}
	resp, body = e.fetch(path + "/formats/azw3")
	if resp.StatusCode != http.StatusOK || len(body) < 100 || body[60:68] != "BOOKMOBI" {
		t.Errorf("file made for the Kindle: %s, %d bytes", resp.Status, len(body))
	}
}

// TestPagesDocument covers a Pages document, which the library keeps
// without being able to open: it gets its cover from the picture Pages
// saves with it, and reading, converting and sending it are refused
// with what to do instead, calibre or not.
func TestPagesDocument(t *testing.T) {
	for _, calibre := range []bool{true, false} {
		e := newEnv(t)
		if !calibre {
			e.tools.ConvertBin = "no-such-ebook-convert"
		}
		var system struct {
			// KeptFormats lists the formats that are only kept.
			KeptFormats []string `json:"kept_formats"`
			// ImportFormats lists the formats accepted.
			ImportFormats []string `json:"import_formats"`
		}
		e.do("GET", "/api/system", nil, http.StatusOK, &system)
		if !reflect.DeepEqual(system.KeptFormats, []string{"pages"}) ||
			!strings.Contains(strings.Join(system.ImportFormats, ","), "pages") {
			t.Fatalf("system = %+v", system)
		}
		book := e.importFile("Shore Sketches - Edda Voss.pages", booktest.Pages(booktest.Book{Title: "ignored"}))
		if book.Title != "Shore Sketches" || !reflect.DeepEqual(book.Authors, []string{"Edda Voss"}) || book.Cover == "" {
			t.Fatalf("imported book = %+v, want its name from the file and its cover from the document", book)
		}
		path := "/api/books/" + itoa(book.ID)
		for _, refused := range []struct {
			// method and path name the request.
			method, path string
			// body is what is sent with it.
			body any
		}{
			{"POST", path + "/convert", map[string]string{"to": "epub"}},
			{"POST", path + "/convert", map[string]string{"to": "epub", "from": "pages"}},
			{"GET", path + "/preview", nil},
			{"POST", "/api/device/books", map[string]any{"book_id": book.ID}},
		} {
			var failure struct {
				// Error is the reason given.
				Error string `json:"error"`
			}
			e.do(refused.method, refused.path, refused.body, http.StatusUnprocessableEntity, &failure)
			if !strings.Contains(failure.Error, "open the book in the Pages app, export it as EPUB or Word") {
				t.Errorf("%s %s with calibre %v: %q, want what to do instead", refused.method, refused.path, calibre, failure.Error)
			}
		}
		resp, body := e.fetch(path + "/formats/pages")
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Index/Document.iwa") {
			t.Errorf("download: %s, %d bytes", resp.Status, len(body))
		}

		both := e.importFile("logbook.docx", booktest.DOCX(logbook))
		e.do("POST", "/api/books/"+itoa(both.ID)+"/formats", files(t, map[string][]byte{"logbook.pages": booktest.Pages(logbook)}),
			http.StatusCreated, nil)
		var job store.Job
		e.do("POST", "/api/books/"+itoa(both.ID)+"/convert", map[string]string{"to": "epub"}, http.StatusAccepted, &job)
		var result conversionResult
		if err := json.Unmarshal(e.wait(job).Result, &result); err != nil || len(result.Book.Formats) != 3 {
			t.Errorf("converting a book that also has a Word file = %s, %v", job.Result, err)
		}
	}
}
