package restore_test

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/restore"
	"github.com/manzanit0/athenaeum/internal/restore/restoretest"
)

// scanned writes a PDF of the given number of scanned pages followed by
// two pages with text, and returns its path.
func scanned(t *testing.T, scans int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "book.pdf")
	data := booktest.ScannedPDF(booktest.Book{
		Title: "The Packet Boat", Author: "Hugh Carrow", Chapters: []string{"THE FALMOUTH STATION", "Homeward"},
	}, scans)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// files returns the files of the EPUB at path by name.
func files(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(data)
	}
	return out
}

func TestInspectCountsPagesByKind(t *testing.T) {
	survey, err := restore.Inspect(scanned(t, 12))
	if err != nil {
		t.Fatal(err)
	}
	if survey.Pages != 14 || survey.Scans != 12 || survey.Text != 2 {
		t.Fatalf("survey = %+v, want 14 pages, 12 scans and 2 with text", survey)
	}
}

func TestRestoreReadsEveryPageAndWritesTheBook(t *testing.T) {
	model := &restoretest.Model{Tokens: 100}
	dst := filepath.Join(t.TempDir(), "out.epub")
	var last restore.Progress
	report, err := restore.Restore(context.Background(), restore.Options{
		Source: scanned(t, 23), Dest: dst, Model: model,
		Metadata: meta.Metadata{Title: "The Packet Boat", Authors: []string{"Hugh Carrow"}, Language: "en"},
		Progress: func(p restore.Progress) { last = p },
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Pages != 25 || report.Sections != 25 || report.Headings != 25 {
		t.Errorf("report = %+v, want 25 pages, sections and headings", report)
	}
	if report.NotesLinked != 23 || len(report.Doubts) != 23 {
		t.Errorf("linked %d notes and kept %d doubts, want 23 of each", report.NotesLinked, len(report.Doubts))
	}
	if report.HeadingsChanged != 24 {
		t.Errorf("changed %d headings, want 24: all but the one already in sentence case", report.HeadingsChanged)
	}
	if report.InputTokens == 0 || report.OutputTokens == 0 {
		t.Errorf("no tokens were counted: %+v", report)
	}
	if last.Stage != restore.StageWriting || last.Read != 25 {
		t.Errorf("last progress = %+v, want the writing stage with 25 pages read", last)
	}
	book := files(t, dst)
	first := book["OEBPS/section0001.xhtml"]
	for _, want := range []string{
		`<h1 id="h1">Chapter 1</h1>`,
		`The words read from the scan of page 1.<a class="noteref" epub:type="noteref" id="r1" href="section0001.xhtml#n1">1</a>`,
		`<p id="n1"><a href="section0001.xhtml#r1">1</a> A note on page 1.</p>`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("the first section lacks %s:\n%s", want, first)
		}
	}
	text := book["OEBPS/section0024.xhtml"]
	if !strings.Contains(text, `<h1 id="h24">The falmouth station</h1>`) || !strings.Contains(text, "weighted with a stone from the beach") {
		t.Errorf("the page with text was not carried over:\n%s", text)
	}
	if nav := book["OEBPS/nav.xhtml"]; !strings.Contains(nav, "Homeward") || !strings.Contains(nav, "section0025.xhtml#h25") {
		t.Errorf("the contents do not list the last chapter:\n%s", nav)
	}
}

func TestRestoreEndsAfterTheSampleWhenTheModelDeclines(t *testing.T) {
	model := &restoretest.Model{Decline: "The pages are too faint to read."}
	dst := filepath.Join(t.TempDir(), "out.epub")
	_, err := restore.Restore(context.Background(), restore.Options{Source: scanned(t, 40), Dest: dst, Model: model})
	var declined *restore.Declined
	if !errors.As(err, &declined) {
		t.Fatalf("err = %v, want the restore to be declined", err)
	}
	if !strings.Contains(err.Error(), "too faint to read") {
		t.Errorf("the error does not give the reason: %v", err)
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Error("a book was written although the restore was declined")
	}
	// Ten pages read, saved and judged, and the word that ends the session.
	if calls := model.Calls(); calls > 15 {
		t.Errorf("the model was asked %d times, which is more than the sample takes", calls)
	}
}

func TestRestoreFailsWhenPagesStayUnsaved(t *testing.T) {
	model := &restoretest.Model{Skip: []int{17}}
	_, err := restore.Restore(context.Background(), restore.Options{
		Source: scanned(t, 20), Dest: filepath.Join(t.TempDir(), "out.epub"), Model: model,
	})
	if err == nil || !strings.Contains(err.Error(), "pages 17 could not be transcribed") {
		t.Fatalf("err = %v, want it to name page 17", err)
	}
}

func TestRestoreStopsWhenItCostsMoreThanAllowed(t *testing.T) {
	model := &restoretest.Model{Name: restore.DefaultModel, Tokens: 1000}
	_, err := restore.Restore(context.Background(), restore.Options{
		Source: scanned(t, 20), Dest: filepath.Join(t.TempDir(), "out.epub"), Model: model, Limit: 0.0001,
	})
	if err == nil || !strings.Contains(err.Error(), "more than the $0.00 it was allowed") {
		t.Fatalf("err = %v, want the restore to stop over its cost", err)
	}
}

func TestRestoreSavesOnePageAtATimeAfterAnAnswerIsCutOff(t *testing.T) {
	model := &restoretest.Model{MostSaves: 5}
	report, err := restore.Restore(context.Background(), restore.Options{
		Source: scanned(t, 20), Dest: filepath.Join(t.TempDir(), "out.epub"), Model: model,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Sections != 22 {
		t.Errorf("the book has %d sections, want all 22 pages", report.Sections)
	}
}

func TestRestoreSaysThatAnAnswerWasCutOff(t *testing.T) {
	model := &restoretest.Model{MostSaves: -1}
	_, err := restore.Restore(context.Background(), restore.Options{
		Source: scanned(t, 20), Dest: filepath.Join(t.TempDir(), "out.epub"), Model: model,
	})
	if err == nil || !strings.Contains(err.Error(), "the answer of the model was cut off for being too long") {
		t.Fatalf("err = %v, want it to say that the answer was cut off", err)
	}
}
