package megasync_test

import (
	"os"
	"path"
	"path/filepath"
	"slices"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/megasync"
	"github.com/manzanit0/athenaeum/internal/megasync/megatest"
)

// brief renders a summary as one line per book and per other file.
func brief(s *megasync.Summary) []string {
	out := []string{}
	if s == nil {
		return append(out, "no summary")
	}
	for _, c := range s.Books {
		line := c.Kind + " " + c.Title
		if c.Details {
			line += " details"
		}
		if c.Cover != "" {
			line += " cover:" + c.Cover
		}
		for _, f := range c.Files {
			line += " " + f.Op + path.Ext(f.Path)
		}
		out = append(out, line)
	}
	for _, f := range s.Other {
		out = append(out, "other "+f.Op+" "+f.Path)
	}
	return out
}

// expect fails the test unless the summary reads as want.
func expect(t *testing.T, what string, s *megasync.Summary, want ...string) {
	t.Helper()
	if got := brief(s); !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// TestChangesByBook checks that a sync says, book by book, what it adds,
// updates and removes on each side, before it runs and after.
func TestChangesByBook(t *testing.T) {
	remote := t.TempDir()
	cloud := &megatest.Service{Root: remote}
	both := megasync.Options{Direction: megasync.Both}
	a, b := newSite(t, cloud), newSite(t, cloud)
	alpha, beta := a.add(t, "Alpha"), a.add(t, "Beta")

	preview := a.run(t, megasync.Options{Direction: megasync.Both, DryRun: true})
	expect(t, "first preview, MEGA", preview.Mega, "add Alpha details add.epub", "add Beta details add.epub")
	expect(t, "first preview, library", preview.Library)
	first := a.run(t, both)
	expect(t, "first sync, MEGA", first.Mega, "add Alpha details add.epub", "add Beta details add.epub")
	if c := first.Mega.Books[0]; c.BookID != alpha.ID || len(c.Authors) != 1 || c.Bytes == 0 {
		t.Errorf("first change = %+v, want the book, its author and its size", c)
	}

	renamed := alpha.Metadata
	renamed.Title = "Alpha Prime"
	if _, err := a.lib.UpdateMetadata(alpha.ID, renamed); err != nil {
		t.Fatal(err)
	}
	if err := a.lib.Delete(beta.ID); err != nil {
		t.Fatal(err)
	}
	a.add(t, "Gamma")
	want := []string{"update Alpha Prime details rename.epub", "remove Beta details remove.epub", "add Gamma details add.epub"}
	preview = a.run(t, megasync.Options{Direction: megasync.Both, DryRun: true})
	expect(t, "second preview, MEGA", preview.Mega, want...)
	expect(t, "second preview, library", preview.Library)
	if len(preview.Pull.Actions) != 0 {
		t.Errorf("the preview plans to download %+v, which the upload makes moot", preview.Pull.Actions)
	}
	moved := preview.Mega.Books[0].Files[0]
	if moved.From != path.Join(alpha.Path, alpha.Formats[0].Filename) || moved.Path == moved.From {
		t.Errorf("rename = %+v, want it from the old name of the file", moved)
	}
	second := a.run(t, both)
	expect(t, "second sync, MEGA", second.Mega, want...)
	expect(t, "second sync, library", second.Library)

	arrivals := []string{"add Alpha Prime details add.epub", "add Gamma details add.epub"}
	preview = b.run(t, megasync.Options{Direction: megasync.Pull, DryRun: true})
	expect(t, "download preview", preview.Library, arrivals...)
	if c := preview.Library.Books[0]; len(c.Authors) != 1 || c.Authors[0] != "Test Author" {
		t.Errorf("previewed download = %+v, want the author MEGA has for the book", c)
	}
	pulled := b.run(t, megasync.Options{Direction: megasync.Pull})
	expect(t, "download", pulled.Library, arrivals...)
	if pulled.Library.Books[0].BookID == 0 {
		t.Error("the downloaded book is not linked to the library")
	}

	loose := filepath.Join(remote, "Books", "Library", "inbox", "lambda.epub")
	if err := os.MkdirAll(filepath.Dir(loose), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loose, booktest.EPUB(booktest.Book{Title: "Lambda", Author: "Test Author"}), 0o644); err != nil {
		t.Fatal(err)
	}
	preview = b.run(t, megasync.Options{Direction: megasync.Pull, DryRun: true})
	expect(t, "preview of a loose file", preview.Library, "add lambda add.epub")
	expect(t, "download of a loose file", b.run(t, megasync.Options{Direction: megasync.Pull}).Library, "add Lambda add.epub")

	mirrored := b.run(t, megasync.Options{Direction: megasync.Push, Delete: true, DryRun: true})
	expect(t, "mirror preview", mirrored.Mega, "add Lambda details rename.epub")
}
