package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/megasync"
	"github.com/manzanit0/athenaeum/internal/store"
)

// progressJob is a job as the API shows it while it runs.
type progressJob struct {
	store.Job
	// Progress says how far the job has come.
	Progress *megasync.Progress `json:"progress"`
}

// TestSyncProgress covers watching a sync while it runs and reading how
// much a preview would transfer.
func TestSyncProgress(t *testing.T) {
	e := newEnv(t)
	slow := e.delay
	for _, title := range []string{"Kindred", "Piranesi", "Stoner"} {
		e.importFile(title+".epub", booktest.EPUB(booktest.Book{Title: title, Author: "Test Author"}))
	}
	e.do("PUT", "/api/settings", map[string]string{"mega_folder": "Books/Library"}, http.StatusOK, nil)

	var job store.Job
	e.do("POST", "/api/sync", megasync.Options{Direction: megasync.Push, DryRun: true}, http.StatusAccepted, &job)
	job = e.wait(job)
	var preview megasync.Report
	if err := json.Unmarshal(job.Result, &preview); err != nil || preview.Push == nil || preview.Push.Files != 6 || preview.Push.Bytes == 0 {
		t.Fatalf("preview = %s, %v; want six files and their size", job.Result, err)
	}

	if err := os.WriteFile(slow, []byte("60ms"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.do("POST", "/api/sync", megasync.Options{Direction: megasync.Push}, http.StatusAccepted, &job)
	var seen *megasync.Progress
	var status struct {
		// LastRun is the most recent sync job.
		LastRun *progressJob `json:"last_run"`
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var running progressJob
		e.do("GET", "/api/jobs/"+itoa(job.ID), nil, http.StatusOK, &running)
		if running.Status != store.JobRunning {
			break
		}
		if p := running.Progress; p != nil && p.Phase == megasync.PhaseUploading && p.Done > 0 && p.Current != "" {
			seen = p
			e.do("GET", "/api/sync", nil, http.StatusOK, &status)
			break
		}
	}
	if seen == nil || seen.Total != len(preview.Push.Actions) || seen.BytesTotal != preview.Push.Bytes || seen.Done >= seen.Total {
		t.Fatalf("progress seen while uploading = %+v", seen)
	}
	if status.LastRun == nil || status.LastRun.ID != job.ID || status.LastRun.Progress == nil {
		t.Errorf("sync status while uploading = %+v", status.LastRun)
	}
	os.Remove(slow)
	e.wait(job)
	var finished progressJob
	e.do("GET", "/api/jobs/"+itoa(job.ID), nil, http.StatusOK, &finished)
	if finished.Progress != nil {
		t.Errorf("a finished job still reports progress %+v", finished.Progress)
	}
}
