package megasync_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/library"
	"github.com/manzanit0/athenaeum/internal/megasync"
	"github.com/manzanit0/athenaeum/internal/megasync/megatest"
	"github.com/manzanit0/athenaeum/internal/store"
	"github.com/manzanit0/athenaeum/internal/vault"
)

// site is one installation of the application sharing a MEGA drive.
type site struct {
	// lib is the local library.
	lib *library.Library
	// st is the local database.
	st *store.Store
	// syncer syncs the library with the shared drive.
	syncer *megasync.Syncer
}

// newSite returns an empty installation signed in to cloud.
func newSite(t *testing.T, cloud *megatest.Service) *site {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	lib, err := library.New(filepath.Join(dir, "library"), filepath.Join(dir, "tmp"), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := vault.Open(st, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	accounts := &megasync.Accounts{Service: cloud, Secrets: secrets}
	if _, err := accounts.SignIn(megatest.Email, megatest.Password, ""); err != nil {
		t.Fatal(err)
	}
	return &site{lib: lib, st: st, syncer: &megasync.Syncer{Accounts: accounts, Store: st, Library: lib}}
}

// run syncs the site and fails the test on error.
func (s *site) run(t *testing.T, opts megasync.Options) *megasync.Report {
	t.Helper()
	report, err := s.syncer.Run(context.Background(), "/Books/Library/", opts, nil)
	if err != nil {
		t.Fatalf("sync %+v: %v", opts, err)
	}
	return report
}

// add imports a book titled title into the site.
func (s *site) add(t *testing.T, title string) *store.Book {
	t.Helper()
	data := booktest.EPUB(booktest.Book{Title: title, Author: "Test Author"})
	book, err := s.lib.Import(context.Background(), title+".epub", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return book
}

// titles returns the titles of the books in the site.
func (s *site) titles(t *testing.T) []string {
	t.Helper()
	books, err := s.st.Books(store.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, b := range books {
		titles = append(titles, b.Title)
	}
	return titles
}

// TestAccounts checks signing in, with and without a two-factor code,
// what is kept of it, signing out and a session MEGA has ended.
func TestAccounts(t *testing.T) {
	cloud := &megatest.Service{Root: t.TempDir()}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	secrets, err := vault.Open(st, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	accounts := &megasync.Accounts{Service: cloud, Secrets: secrets}
	if status := accounts.Status(); status.SignedIn || status.Account != "" {
		t.Errorf("status before signing in = %+v", status)
	}
	if _, err := accounts.Open(); !errors.Is(err, megasync.ErrSignedOut) {
		t.Errorf("opening the drive signed out gave %v, want ErrSignedOut", err)
	}
	for _, attempt := range []struct {
		// email, password and code are what is typed in.
		email, password, code string
		// want is the error expected.
		want error
	}{
		{megatest.Email, "wrong", "", megasync.ErrSignInFailed},
		{"", megatest.Password, "", megasync.ErrSignInFailed},
		{megatest.GuardedEmail, megatest.Password, "", megasync.ErrCodeRequired},
		{megatest.GuardedEmail, megatest.Password, "000000", megasync.ErrSignInFailed},
	} {
		if _, err := accounts.SignIn(attempt.email, attempt.password, attempt.code); !errors.Is(err, attempt.want) {
			t.Errorf("signing in as %q gave %v, want %v", attempt.email, err, attempt.want)
		}
	}
	status, err := accounts.SignIn(" "+megatest.GuardedEmail, megatest.Password, megatest.Code)
	if err != nil || !status.SignedIn || status.Account != megatest.GuardedEmail {
		t.Fatalf("signing in = %+v, %v", status, err)
	}
	if again := accounts.Status(); again != status {
		t.Errorf("status = %+v, want %+v", again, status)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "test.db"))
	if err != nil || bytes.Contains(raw, []byte(megatest.Password)) || bytes.Contains(raw, []byte("master key")) {
		t.Errorf("the database holds the password or the session in the clear: %v", err)
	}
	if _, err := accounts.Open(); err != nil {
		t.Errorf("opening the drive: %v", err)
	}
	if confirmed, err := accounts.SignOut(); err != nil || !confirmed {
		t.Errorf("signing out = %t, %v", confirmed, err)
	}
	if status := accounts.Status(); status.SignedIn {
		t.Errorf("status after signing out = %+v", status)
	}

	if _, err := accounts.SignIn(megatest.Email, megatest.Password, ""); err != nil {
		t.Fatal(err)
	}
	cloud.End("session-2")
	if _, err := accounts.Open(); !errors.Is(err, megasync.ErrSessionEnded) {
		t.Errorf("a session ended by MEGA gave %v, want ErrSessionEnded", err)
	}
	if status := accounts.Status(); status.SignedIn {
		t.Errorf("status after MEGA ended the session = %+v", status)
	}
}

// TestTwoWaySync checks that books travel between two installations and
// that a repeated sync transfers nothing.
func TestTwoWaySync(t *testing.T) {
	cloud := &megatest.Service{Root: t.TempDir()}
	both := megasync.Options{Direction: megasync.Both}
	a, b := newSite(t, cloud), newSite(t, cloud)
	a.add(t, "Alpha")
	b.add(t, "Beta")

	preview := a.run(t, megasync.Options{Direction: megasync.Both, DryRun: true})
	if !preview.Push.DryRun || preview.Push.Files != 2 || len(preview.Push.Actions) == 0 {
		t.Errorf("preview = %+v, want the planned actions and how much they transfer", preview.Push)
	}
	if first := a.run(t, both); first.Push.Files != 2 || first.Pull.Files != 0 {
		t.Errorf("first sync of A pushed %d and pulled %d files, want 2 and 0", first.Push.Files, first.Pull.Files)
	}
	if again := a.run(t, both); again.Push.Files != 0 || again.Pull.Files != 0 {
		t.Errorf("repeated sync of A pushed %d and pulled %d files, want none", again.Push.Files, again.Pull.Files)
	}
	report := b.run(t, both)
	if report.Rescan == nil || report.Rescan.Added != 1 {
		t.Errorf("rescan on B = %+v, want one book added", report.Rescan)
	}
	if got := b.titles(t); len(got) != 2 || got[0] != "Alpha" || got[1] != "Beta" {
		t.Errorf("B has %q, want Alpha and Beta", got)
	}
	a.run(t, both)
	if got := a.titles(t); len(got) != 2 {
		t.Errorf("A has %q, want Alpha and Beta", got)
	}
}

// TestDeletionsPropagate checks that a book deleted locally is removed
// from MEGA by the next two-way sync instead of being downloaded again.
func TestDeletionsPropagate(t *testing.T) {
	remote := t.TempDir()
	cloud := &megatest.Service{Root: remote}
	both := megasync.Options{Direction: megasync.Both}
	a := newSite(t, cloud)
	book := a.add(t, "Gamma")
	a.run(t, both)
	uploaded := []string{
		filepath.Join(remote, "Books", "Library", filepath.FromSlash(book.Path), book.Formats[0].Filename),
		filepath.Join(remote, "Books", "Library", "_athenaeum", book.UUID+".json"),
	}
	for _, file := range uploaded {
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("the book was not uploaded: %v", err)
		}
	}
	if err := a.lib.Delete(book.ID); err != nil {
		t.Fatal(err)
	}
	report := a.run(t, both)
	if len(report.Removed) != len(uploaded) {
		t.Errorf("removed = %q, want the file of the book and its details", report.Removed)
	}
	for _, file := range uploaded {
		if _, err := os.Stat(file); !os.IsNotExist(err) {
			t.Errorf("%s is still on MEGA", file)
		}
	}
	if got := a.titles(t); len(got) != 0 {
		t.Errorf("A has %q, want no books", got)
	}
	if pending, _ := a.st.Tombstones(); len(pending) != 0 {
		t.Errorf("pending deletions = %q, want none", pending)
	}
}

// TestDeletionsSurviveDownloads checks that a book deleted locally stays
// deleted when a download brings its files back, until an upload has
// removed them from MEGA.
func TestDeletionsSurviveDownloads(t *testing.T) {
	cloud := &megatest.Service{Root: t.TempDir()}
	a := newSite(t, cloud)
	book := a.add(t, "Theta")
	a.add(t, "Iota")
	a.run(t, megasync.Options{Direction: megasync.Push})
	if err := a.lib.Delete(book.ID); err != nil {
		t.Fatal(err)
	}
	a.run(t, megasync.Options{Direction: megasync.Pull})
	if got := a.titles(t); len(got) != 1 || got[0] != "Iota" {
		t.Errorf("A has %q after downloading, want only Iota", got)
	}
	if pending, _ := a.st.Tombstones(); len(pending) == 0 {
		t.Error("the deletion was forgotten before it reached MEGA")
	}
	a.run(t, megasync.Options{Direction: megasync.Both})
	b := newSite(t, cloud)
	b.run(t, megasync.Options{Direction: megasync.Pull})
	if got := b.titles(t); len(got) != 1 || got[0] != "Iota" {
		t.Errorf("B has %q, want only Iota", got)
	}
}

// TestLooseFilesAreFiled checks that an ebook put on MEGA by hand becomes
// a book filed under its author, on MEGA as well after the next sync.
func TestLooseFilesAreFiled(t *testing.T) {
	remote := t.TempDir()
	cloud := &megatest.Service{Root: remote}
	both := megasync.Options{Direction: megasync.Both}
	a := newSite(t, cloud)
	a.add(t, "Kappa")
	a.run(t, both)
	loose := filepath.Join(remote, "Books", "Library", "inbox", "lambda.epub")
	if err := os.MkdirAll(filepath.Dir(loose), 0o755); err != nil {
		t.Fatal(err)
	}
	data := booktest.EPUB(booktest.Book{Title: "Lambda", Author: "Test Author"})
	if err := os.WriteFile(loose, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if report := a.run(t, both); report.Rescan == nil || report.Rescan.Added != 1 {
		t.Fatalf("rescan = %+v, want the loose file adopted", report.Rescan)
	}
	a.run(t, both)
	filed := filepath.Join(remote, "Books", "Library", "Test Author", "Lambda - Test Author.epub")
	if _, err := os.Stat(filed); err != nil {
		t.Errorf("the book was not filed on MEGA: %v", err)
	}
	if _, err := os.Stat(loose); !os.IsNotExist(err) {
		t.Error("the loose file is still on MEGA")
	}
	if again := a.run(t, both); again.Rescan.Added != 0 || len(a.titles(t)) != 2 {
		t.Errorf("a further sync changed the library: %+v, %q", again.Rescan, a.titles(t))
	}
}

// TestMirrorAndValidation checks mirroring with deletes and the rejected
// option combinations.
func TestMirrorAndValidation(t *testing.T) {
	cloud := &megatest.Service{Root: t.TempDir()}
	a, b := newSite(t, cloud), newSite(t, cloud)
	a.add(t, "Delta")
	a.run(t, megasync.Options{Direction: megasync.Push})
	b.add(t, "Epsilon")
	b.run(t, megasync.Options{Direction: megasync.Pull, Delete: true})
	if got := b.titles(t); len(got) != 1 || got[0] != "Delta" {
		t.Errorf("B has %q, want only Delta", got)
	}
	ctx := context.Background()
	if _, err := a.syncer.Run(ctx, "Books", megasync.Options{Direction: megasync.Both, Delete: true}, nil); !errors.Is(err, megasync.ErrInvalidOptions) {
		t.Errorf("two-way delete error = %v, want ErrInvalidOptions", err)
	}
	if _, err := a.syncer.Run(ctx, " / ", megasync.Options{Direction: megasync.Push}, nil); !errors.Is(err, megasync.ErrNotConfigured) {
		t.Errorf("empty folder error = %v, want ErrNotConfigured", err)
	}
}

// TestProgress checks what a sync reports while it runs and how much a
// preview says there is to transfer.
func TestProgress(t *testing.T) {
	cloud := &megatest.Service{Root: t.TempDir()}
	home, away := newSite(t, cloud), newSite(t, cloud)
	for _, title := range []string{"Kindred", "Piranesi", "Stoner"} {
		home.add(t, title)
	}
	preview := home.run(t, megasync.Options{Direction: megasync.Push, DryRun: true})
	if !preview.Push.DryRun || preview.Push.Files != 6 || preview.Push.Folders == 0 || preview.Push.Bytes == 0 {
		t.Fatalf("preview totals = %+v, want six files, their folders and their size", *preview.Push)
	}

	var seen []megasync.Progress
	report, err := home.syncer.Run(context.Background(), "Books/Library", megasync.Options{Direction: megasync.Push},
		func(p megasync.Progress) { seen = append(seen, p) })
	if err != nil {
		t.Fatal(err)
	}
	if report.Push.Files != 6 || report.Push.Bytes != preview.Push.Bytes || len(report.Push.Actions) != len(preview.Push.Actions) {
		t.Errorf("upload result = %+v, want what the preview planned", *report.Push)
	}
	if seen[0].Phase != megasync.PhasePreparing || seen[len(seen)-1].Phase != megasync.PhaseFinishing {
		t.Errorf("first and last phases = %s, %s", seen[0].Phase, seen[len(seen)-1].Phase)
	}
	var uploads []megasync.Progress
	for _, p := range seen {
		if p.Phase == megasync.PhaseUploading {
			uploads = append(uploads, p)
		}
	}
	total := len(preview.Push.Actions)
	if len(uploads) != total+2 {
		t.Fatalf("%d upload reports, want one per change plus a first and a last", len(uploads))
	}
	for i, p := range uploads {
		if p.Total != total || p.BytesTotal != preview.Push.Bytes || p.Done != max(i-1, 0) && i < len(uploads)-1 {
			t.Errorf("report %d = %+v", i, p)
		}
		if i > 0 && p.BytesDone < uploads[i-1].BytesDone {
			t.Errorf("report %d: bytes went back from %d to %d", i, uploads[i-1].BytesDone, p.BytesDone)
		}
		if named := i > 0 && i < len(uploads)-1; named != (p.Current != "") {
			t.Errorf("report %d names %q", i, p.Current)
		}
	}
	if last := uploads[len(uploads)-1]; last.Done != total || last.BytesDone != last.BytesTotal {
		t.Errorf("last upload report = %+v, want everything done", last)
	}

	seen = nil
	if _, err := away.syncer.Run(context.Background(), "Books/Library", megasync.Options{Direction: megasync.Pull},
		func(p megasync.Progress) { seen = append(seen, p) }); err != nil {
		t.Fatal(err)
	}
	downloaded := false
	for _, p := range seen {
		downloaded = downloaded || p.Phase == megasync.PhaseDownloading && p.Done == p.Total && p.BytesDone == preview.Push.Bytes
	}
	if !downloaded || len(away.titles(t)) != 3 {
		t.Errorf("download reports = %+v; books = %v", seen, away.titles(t))
	}

	seen = nil
	again := home.run(t, megasync.Options{Direction: megasync.Push})
	if len(again.Push.Actions) != 0 || again.Push.Files != 0 {
		t.Errorf("a second upload changed %+v, want nothing", *again.Push)
	}
}
