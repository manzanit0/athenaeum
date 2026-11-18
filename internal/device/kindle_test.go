package device_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/device"
)

// newKindle returns a Kindle backed by a temporary folder laid out like
// the device storage.
func newKindle(t *testing.T) (*device.Kindle, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "documents"), 0o755); err != nil {
		t.Fatal(err)
	}
	return device.New(func() string { return dir }), dir
}

// TestNotConnected checks the behaviour without a mounted Kindle.
func TestNotConnected(t *testing.T) {
	kindle := device.New(func() string { return filepath.Join(t.TempDir(), "missing") })
	if kindle.Status().Connected {
		t.Error("a missing mount point was reported as connected")
	}
	if _, err := kindle.List(); !errors.Is(err, device.ErrNotConnected) {
		t.Errorf("List error = %v, want ErrNotConnected", err)
	}
}

// TestPutListDelete checks the life cycle of a book on the Kindle.
func TestPutListDelete(t *testing.T) {
	kindle, dir := newKindle(t)
	src := filepath.Join(t.TempDir(), "source.mobi")
	book := booktest.Book{Title: "Solaris", Author: "Stanislaw Lem"}
	if err := os.WriteFile(src, booktest.MOBI(book), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := kindle.Put("Solaris - Stanislaw Lem.mobi", src)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "documents/Solaris - Stanislaw Lem.mobi" {
		t.Errorf("path = %q", rel)
	}
	sidecar := filepath.Join(dir, "documents", "Solaris - Stanislaw Lem.sdr")
	if err := os.MkdirAll(sidecar, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sidecar, "position.azw3f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "documents", "notes.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := kindle.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != rel || files[0].Title != "Solaris" || files[0].Authors[0] != "Stanislaw Lem" {
		t.Fatalf("files = %+v", files)
	}
	if !kindle.Status().Connected || kindle.Status().TotalBytes == 0 {
		t.Errorf("status = %+v", kindle.Status())
	}
	if err := kindle.Delete(rel); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Error("the sidecar folder was not removed")
	}
	if files, _ = kindle.List(); len(files) != 0 {
		t.Errorf("files after delete = %+v", files)
	}
}

// TestRejectsPathsOutsideDocuments checks that only books in the
// documents folder can be opened or deleted.
func TestRejectsPathsOutsideDocuments(t *testing.T) {
	kindle, dir := newKindle(t)
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"secret.txt", "documents/../secret.txt", "/etc/passwd", "documents/notes.html", "system/x.mobi"} {
		if err := kindle.Delete(rel); !errors.Is(err, device.ErrInvalidPath) {
			t.Errorf("Delete(%q) error = %v, want ErrInvalidPath", rel, err)
		}
		if _, err := kindle.Open(rel); !errors.Is(err, device.ErrInvalidPath) {
			t.Errorf("Open(%q) error = %v, want ErrInvalidPath", rel, err)
		}
	}
}
