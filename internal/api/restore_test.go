package api_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/store"
)

// outlook is what restoring a book would involve, as the API says it.
type outlook struct {
	// Configured reports whether the key of the language model is known.
	Configured bool `json:"configured"`
	// Possible reports whether the book can be restored.
	Possible bool `json:"possible"`
	// Reason says why the book cannot be restored.
	Reason string `json:"reason"`
	// Pages is the number of pages.
	Pages int `json:"pages"`
	// Scans is the number of scanned pages.
	Scans int `json:"scans"`
	// CostLow is the least the restore is expected to cost.
	CostLow float64 `json:"cost_low"`
	// CostHigh is the most the restore is expected to cost.
	CostHigh float64 `json:"cost_high"`
	// Replaces reports whether the book has an EPUB already.
	Replaces bool `json:"replaces"`
}

// scannedBook imports a scanned PDF of 14 pages and returns the book.
func (e *env) scannedBook() store.Book {
	e.t.Helper()
	return e.importFile("packet.pdf", booktest.ScannedPDF(booktest.Book{
		Title: "The Packet Boat", Author: "Hugh Carrow", Chapters: []string{"THE FALMOUTH STATION", "Homeward"},
	}, 12))
}

func TestRestoreNeedsAKeyAndTurnsAScannedPDFIntoAnEPUB(t *testing.T) {
	e := newEnv(t)
	book := e.scannedBook()
	path := "/api/books/" + itoa(book.ID) + "/restore"

	var before outlook
	e.do("GET", path, nil, http.StatusOK, &before)
	if before.Configured || !before.Possible || before.Pages != 14 || before.Scans != 12 || before.Replaces {
		t.Fatalf("outlook = %+v, want a possible restore of 14 pages, 12 of them scans, without a key", before)
	}
	if before.CostLow <= 0 || before.CostHigh <= before.CostLow {
		t.Errorf("the cost is given as %v to %v", before.CostLow, before.CostHigh)
	}
	e.do("POST", path, map[string]bool{}, http.StatusConflict, nil)

	e.do("PUT", "/api/restore/key", map[string]string{"key": " "}, http.StatusBadRequest, nil)
	e.do("PUT", "/api/restore/key", map[string]string{"key": "sk-ant-test"}, http.StatusOK, nil)
	var job store.Job
	e.do("POST", path, map[string]bool{}, http.StatusAccepted, &job)
	job = e.wait(job)
	if job.Kind != "restore" || !strings.Contains(job.Title, "The Packet Boat") {
		t.Errorf("job = %+v", job)
	}
	var result struct {
		// Pages is the number of pages read.
		Pages int `json:"pages"`
		// Sections is the number of chapters.
		Sections int `json:"sections"`
		// Cost is what the restore cost.
		Cost float64 `json:"cost"`
		// Book is the book with its new file.
		Book store.Book `json:"book"`
	}
	e.do("GET", "/api/jobs/"+itoa(job.ID), nil, http.StatusOK, &struct {
		// Result is the outcome of the job.
		Result any `json:"result"`
	}{Result: &result})
	if result.Pages != 14 || result.Sections != 14 || result.Cost <= 0 {
		t.Errorf("result = %+v, want 14 pages and sections and a cost", result)
	}
	if _, ok := result.Book.Format("epub"); !ok {
		t.Errorf("the book has no EPUB after the restore: %+v", result.Book.Formats)
	}
	if _, ok := result.Book.Format("pdf"); !ok {
		t.Error("the PDF is gone after the restore")
	}

	var after outlook
	e.do("GET", path, nil, http.StatusOK, &after)
	if !after.Replaces {
		t.Error("the outlook does not say that the book has an EPUB now")
	}
	e.do("POST", path, map[string]bool{}, http.StatusBadRequest, nil)
	e.do("POST", path, map[string]bool{"replace": true}, http.StatusAccepted, &job)
	e.wait(job)

	e.do("DELETE", "/api/restore/key", nil, http.StatusOK, nil)
	e.do("POST", path, map[string]bool{"replace": true}, http.StatusConflict, nil)
}

func TestRestoreEndsWithTheReasonWhenTheModelDeclines(t *testing.T) {
	e := newEnv(t)
	e.decline = "The pages are too faint to read."
	book := e.scannedBook()
	e.do("PUT", "/api/restore/key", map[string]string{"key": "sk-ant-test"}, http.StatusOK, nil)
	var job store.Job
	e.do("POST", "/api/books/"+itoa(book.ID)+"/restore", map[string]bool{}, http.StatusAccepted, &job)
	deadline := time.Now().Add(20 * time.Second)
	for job.Status == store.JobRunning && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		e.do("GET", "/api/jobs/"+itoa(job.ID), nil, http.StatusOK, &job)
	}
	if job.Status != store.JobFailed || !strings.Contains(job.Error, "too faint to read") {
		t.Fatalf("job = %+v, want it to fail with the reason of the model", job)
	}
	var kept store.Book
	e.do("GET", "/api/books/"+itoa(book.ID), nil, http.StatusOK, &kept)
	if _, ok := kept.Format("epub"); ok {
		t.Error("the book got an EPUB although the restore was declined")
	}
}

func TestRestoreIsOnlyOfferedForBooksWithAPDF(t *testing.T) {
	e := newEnv(t)
	book := e.importSample()
	var got outlook
	e.do("GET", "/api/books/"+itoa(book.ID)+"/restore", nil, http.StatusOK, &got)
	if got.Possible || !strings.Contains(got.Reason, "PDF") || !got.Replaces {
		t.Fatalf("outlook = %+v, want it to say that the book needs a PDF", got)
	}
}
