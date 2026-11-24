package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/store"
)

// sendResult is what a finished send job reports.
type sentBook struct {
	// Path is where the book was copied to.
	Path string `json:"path"`
	// Format is the format that was sent.
	Format string `json:"format"`
	// Converted reports whether a conversion was needed.
	Converted bool `json:"converted"`
}

// send sends the book to the Kindle and returns what was sent.
func (e *env) send(id int64) sentBook {
	e.t.Helper()
	var job store.Job
	e.do("POST", "/api/device/books", map[string]any{"book_id": id}, http.StatusAccepted, &job)
	job = e.wait(job)
	var sent sentBook
	if err := json.Unmarshal(job.Result, &sent); err != nil {
		e.t.Fatalf("send result = %s, %v", job.Result, err)
	}
	return sent
}

// addFile adds a file to an existing book.
func (e *env) addFile(id int64, name string, data []byte) {
	e.t.Helper()
	e.do("POST", "/api/books/"+itoa(id)+"/formats", files(e.t, map[string][]byte{name: data}), http.StatusCreated, nil)
}

// TestSendPrefersAZW3ThenEPUBThenPDF checks which file of a book is sent:
// its AZW3 as it is, else its EPUB converted to AZW3 even when it has a
// PDF, else the PDF.
func TestSendPrefersAZW3ThenEPUBThenPDF(t *testing.T) {
	book := booktest.Book{Title: "Solaris", Author: "Stanislaw Lem"}

	t.Run("an AZW3 is sent as it is", func(t *testing.T) {
		e := newEnv(t)
		b := e.importFile("solaris.epub", booktest.EPUB(book))
		e.addFile(b.ID, "solaris.pdf", booktest.PDF(book))
		var job store.Job
		e.do("POST", "/api/books/"+itoa(b.ID)+"/convert", map[string]string{"to": "azw3"}, http.StatusAccepted, &job)
		e.wait(job)
		sent := e.send(b.ID)
		if sent.Format != "azw3" || sent.Converted {
			t.Errorf("sent %+v, want the existing AZW3 without converting", sent)
		}
	})

	t.Run("an EPUB is converted even when there is a PDF", func(t *testing.T) {
		e := newEnv(t)
		b := e.importFile("solaris.epub", booktest.EPUB(book))
		e.addFile(b.ID, "solaris.pdf", booktest.PDF(book))
		sent := e.send(b.ID)
		if sent.Format != "azw3" || !sent.Converted || !strings.HasSuffix(sent.Path, ".azw3") {
			t.Errorf("sent %+v, want the EPUB converted to AZW3", sent)
		}
	})

	t.Run("a PDF alone is sent as it is", func(t *testing.T) {
		e := newEnv(t)
		b := e.importFile("solaris.pdf", booktest.PDF(book))
		sent := e.send(b.ID)
		if sent.Format != "pdf" || sent.Converted || !strings.HasSuffix(sent.Path, ".pdf") {
			t.Errorf("sent %+v, want the PDF as it is", sent)
		}
	})
}

// TestSendWritesThumbnail checks that a book sent as AZW3 comes with the
// cover picture the Kindle looks for, and that removing the book takes it
// away: without it the Kindle shows "No image available".
func TestSendWritesThumbnail(t *testing.T) {
	e := newEnv(t)
	thumbnails := filepath.Join(e.kindle, "system", "thumbnails")
	if err := os.MkdirAll(filepath.Dir(thumbnails), 0o755); err != nil {
		t.Fatal(err)
	}
	b := e.importSample()
	sent := e.send(b.ID)

	asin, kind := meta.KindleID(filepath.Join(e.kindle, filepath.FromSlash(sent.Path)))
	if asin == "" || kind != "PDOC" {
		t.Fatalf("the sent book declares identifier %q and type %q", asin, kind)
	}
	thumbnail := filepath.Join(thumbnails, "thumbnail_"+asin+"_PDOC_portrait.jpg")
	data, err := os.ReadFile(thumbnail)
	if err != nil || meta.ImageExt(data) != "jpg" {
		t.Fatalf("thumbnail %s: %d bytes, %v; want a JPEG", thumbnail, len(data), err)
	}
	if left, _ := filepath.Glob(filepath.Join(thumbnails, "*.partial")); len(left) != 0 {
		t.Errorf("leftover files: %v", left)
	}

	e.do("DELETE", "/api/device/books?path="+url.QueryEscape(sent.Path), nil, http.StatusNoContent, nil)
	if _, err := os.Stat(thumbnail); !os.IsNotExist(err) {
		t.Errorf("the thumbnail stayed after the book was removed: %v", err)
	}
}

// TestSendKeepsOtherFiles checks that sending a book never overwrites a
// file of the same name on the Kindle that is not its own, so both stay
// listed and linked to the right book.
func TestSendKeepsOtherFiles(t *testing.T) {
	e := newEnv(t)
	b := e.importSample()
	first := e.send(b.ID)

	// Another book that would be given the same file name.
	loose := filepath.Join(e.kindle, "documents", filepath.Base(first.Path))
	e.do("DELETE", "/api/device/books?path="+url.QueryEscape(first.Path), nil, http.StatusNoContent, nil)
	if err := os.WriteFile(loose, []byte("not a book of the library"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := e.send(b.ID)
	if second.Path == first.Path {
		t.Fatalf("the book was sent over the file that was already there: %s", second.Path)
	}
	if data, err := os.ReadFile(loose); err != nil || string(data) != "not a book of the library" {
		t.Errorf("the file that was already there changed: %q, %v", data, err)
	}

	// Sending the same book again replaces its own copy.
	if again := e.send(b.ID); again.Path != second.Path {
		t.Errorf("sent again to %s, want %s", again.Path, second.Path)
	}
}

// TestDeviceBooksStayLinked checks that every book sent in one go is
// listed as in the library afterwards.
func TestDeviceBooksStayLinked(t *testing.T) {
	e := newEnv(t)
	var jobs []store.Job
	var ids []int64
	for _, title := range []string{"One", "Two", "Three", "Four", "Five", "Six"} {
		b := e.importFile(title+".epub", booktest.EPUB(booktest.Book{Title: title, Author: "Same Author"}))
		ids = append(ids, b.ID)
		var job store.Job
		e.do("POST", "/api/device/books", map[string]any{"book_id": b.ID}, http.StatusAccepted, &job)
		jobs = append(jobs, job)
		// Listing while books are still being sent must not forget them.
		e.do("GET", "/api/device/books", nil, http.StatusOK, nil)
	}
	for _, job := range jobs {
		e.wait(job)
	}
	var onDevice []struct {
		// Path is the file path on the Kindle.
		Path string `json:"path"`
		// BookID links the file to a library book.
		BookID *int64 `json:"book_id"`
	}
	e.do("GET", "/api/device/books", nil, http.StatusOK, &onDevice)
	linked := map[int64]bool{}
	for _, f := range onDevice {
		if f.BookID == nil {
			t.Errorf("%s is not linked to its book", f.Path)
			continue
		}
		linked[*f.BookID] = true
	}
	for _, id := range ids {
		if !linked[id] {
			t.Errorf("book %d is not linked to a file on the Kindle: %+v", id, onDevice)
		}
	}
}
