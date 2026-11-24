package megasync_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/manzanit0/athenaeum/internal/megasync"
	"github.com/manzanit0/athenaeum/internal/megasync/megatest"
	"github.com/manzanit0/athenaeum/internal/store"
)

// quiet fails the test unless the sync report r moved nothing.
func quiet(t *testing.T, who string, r *megasync.Report) {
	t.Helper()
	if len(r.Removed) != 0 || len(r.Push.Actions) != 0 || len(r.Pull.Actions) != 0 {
		t.Errorf("a further sync of %s removed %q, uploaded %+v and changed %+v, want nothing",
			who, r.Removed, r.Push.Actions, r.Pull.Actions)
	}
}

// files returns the library relative paths of the files of the site.
func (s *site) files(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(s.lib.Root(), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(s.lib.Root(), p)
		out = append(out, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// trashed returns the files called rel in the trash of the site.
func (s *site) trashed(rel string) []string {
	found, _ := filepath.Glob(filepath.Join(filepath.Dir(s.lib.Root()), "trash", "*", filepath.FromSlash(rel)))
	return found
}

// only returns the one book of the site.
func (s *site) only(t *testing.T) *store.Book {
	t.Helper()
	books, err := s.st.Books(store.ListOptions{})
	if err != nil || len(books) != 1 {
		t.Fatalf("books = %v, %v, want one book", books, err)
	}
	return books[0]
}

// describe sets the description of the one book of the site.
func (s *site) describe(t *testing.T, text string) {
	t.Helper()
	book := s.only(t)
	m := book.Metadata
	m.Description = text
	if _, err := s.lib.UpdateMetadata(book.ID, m); err != nil {
		t.Fatal(err)
	}
}

// TestEditsReachOtherMachines checks that a book retitled on one machine,
// which renames its file, is retitled on the other by a sync both ways
// instead of being put back as it was, and that both then stay put.
func TestEditsReachOtherMachines(t *testing.T) {
	cloud := &megatest.Service{Root: t.TempDir()}
	both := megasync.Options{Direction: megasync.Both}
	a, b := newSite(t, cloud), newSite(t, cloud)
	book := a.add(t, "Alpha")
	a.run(t, both)
	b.run(t, both)

	edited := book.Metadata
	edited.Title = "Alpha, the second edition"
	if _, err := a.lib.UpdateMetadata(book.ID, edited); err != nil {
		t.Fatal(err)
	}
	a.run(t, both)
	report := b.run(t, both)
	if len(report.Push.Actions) != 0 || len(report.Removed) != 0 {
		t.Errorf("B uploaded %+v and removed %q, want it to only follow A", report.Push.Actions, report.Removed)
	}
	if len(report.Conflicts) != 0 {
		t.Errorf("conflicts = %+v, want none: only A changed the book", report.Conflicts)
	}
	for _, s := range []*site{a, b} {
		if got := s.titles(t); len(got) != 1 || got[0] != edited.Title {
			t.Errorf("titles = %q, want only %q", got, edited.Title)
		}
		if got := s.files(t); len(got) != 2 {
			t.Errorf("files = %q, want the renamed book and its details", got)
		}
	}
	quiet(t, "A", a.run(t, both))
	quiet(t, "B", b.run(t, both))
}

// TestDeletionsReachOtherMachines checks that a book deleted on one
// machine is removed from the other by a sync both ways, which keeps the
// files it removes in the trash, instead of being uploaded again.
func TestDeletionsReachOtherMachines(t *testing.T) {
	remote := t.TempDir()
	cloud := &megatest.Service{Root: remote}
	both := megasync.Options{Direction: megasync.Both}
	a, b := newSite(t, cloud), newSite(t, cloud)
	book := a.add(t, "Alpha")
	a.add(t, "Beta")
	a.run(t, both)
	b.run(t, both)

	if err := a.lib.Delete(book.ID); err != nil {
		t.Fatal(err)
	}
	a.run(t, both)
	preview := b.run(t, megasync.Options{Direction: megasync.Both, DryRun: true})
	if got := preview.Library; got == nil || len(got.Books) != 1 || got.Books[0].Kind != megasync.KindRemove || got.Books[0].Title != "Alpha" {
		t.Errorf("preview on B = %+v, want Alpha removed from the library", got)
	}
	if got := b.titles(t); len(got) != 2 {
		t.Errorf("B has %q after a preview, want both books still", got)
	}
	report := b.run(t, both)
	if report.Pull.Deleted != 2 || len(report.Push.Actions) != 0 {
		t.Errorf("B deleted %d files and uploaded %+v, want the book and its details deleted", report.Pull.Deleted, report.Push.Actions)
	}
	for _, s := range []*site{a, b} {
		if got := s.titles(t); len(got) != 1 || got[0] != "Beta" {
			t.Errorf("titles = %q, want only Beta", got)
		}
	}
	if _, err := os.Stat(filepath.Join(remote, "Books", "Library", "_athenaeum", book.UUID+".json")); !os.IsNotExist(err) {
		t.Error("the deleted book is back on MEGA")
	}
	if kept := b.trashed(book.Path + "/" + book.Formats[0].Filename); len(kept) != 1 {
		t.Errorf("trash holds %q, want the file removed from B", kept)
	}
	quiet(t, "A", a.run(t, both))
	quiet(t, "B", b.run(t, both))
}

// TestConflictingEdits checks that when two machines change the same
// book between syncs, the later change stays on both, the earlier one is
// kept in the trash and the run says which side it kept.
func TestConflictingEdits(t *testing.T) {
	cloud := &megatest.Service{Root: t.TempDir()}
	both := megasync.Options{Direction: megasync.Both}
	a, b := newSite(t, cloud), newSite(t, cloud)
	book := a.add(t, "Alpha")
	a.run(t, both)
	b.run(t, both)
	details := "_athenaeum/" + book.UUID + ".json"

	b.describe(t, "What B says, which is the longer of the two.")
	a.describe(t, "What A says.")
	a.run(t, both)
	report := b.run(t, both)
	if len(report.Conflicts) != 1 || report.Conflicts[0] != (megasync.Conflict{Path: details, Kept: megasync.SideMega}) {
		t.Errorf("conflicts = %+v, want the details of the book kept as MEGA has them", report.Conflicts)
	}
	if got := b.only(t).Description; got != "What A says." {
		t.Errorf("B describes the book as %q, want the later change, made on A", got)
	}
	if lost := b.trashed(details); len(lost) != 1 {
		t.Errorf("trash holds %q, want the details B had", lost)
	}

	a.describe(t, "A again.")
	a.run(t, both)
	b.describe(t, "B has the last word.")
	report = b.run(t, both)
	if len(report.Conflicts) != 1 || report.Conflicts[0].Kept != megasync.SideLibrary {
		t.Errorf("conflicts = %+v, want the details kept as B has them", report.Conflicts)
	}
	a.run(t, both)
	if got := a.only(t).Description; got != "B has the last word." {
		t.Errorf("A describes the book as %q, want the later change, made on B", got)
	}
	quiet(t, "A", a.run(t, both))
	quiet(t, "B", b.run(t, both))
}

// TestSyncStateStartsOver checks that machines with nothing recorded,
// as after an upgrade, take matching files as synced without moving
// them, and that a sync refuses to empty the library when the MEGA
// folder has lost everything.
func TestSyncStateStartsOver(t *testing.T) {
	remote := t.TempDir()
	cloud := &megatest.Service{Root: remote}
	both := megasync.Options{Direction: megasync.Both}
	a, b := newSite(t, cloud), newSite(t, cloud)
	a.add(t, "Alpha")
	a.run(t, megasync.Options{Direction: megasync.Push})
	b.run(t, megasync.Options{Direction: megasync.Pull})
	for _, s := range []*site{a, b} {
		if err := s.st.ClearSyncState(); err != nil {
			t.Fatal(err)
		}
	}
	quiet(t, "A", a.run(t, both))
	quiet(t, "B", b.run(t, both))
	if state, _ := b.st.SyncState(); len(state) != 2 {
		t.Errorf("B recorded %d files, want the book and its details", len(state))
	}

	if err := os.RemoveAll(filepath.Join(remote, "Books", "Library")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.syncer.Run(context.Background(), "Books/Library", both, nil); !errors.Is(err, megasync.ErrRemoteEmpty) {
		t.Errorf("sync with an emptied MEGA folder: %v, want ErrRemoteEmpty", err)
	}
	if got := b.titles(t); len(got) != 1 {
		t.Errorf("B has %q, want its book kept", got)
	}
	b.run(t, megasync.Options{Direction: megasync.Push})
	quiet(t, "B", b.run(t, both))
}
