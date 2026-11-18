package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestUpgrade checks that a database created before deletions recorded
// their book opens, keeps its deletions and records new ones in full.
func TestUpgrade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE tombstones (path TEXT PRIMARY KEY, created_at TEXT NOT NULL);
		INSERT INTO tombstones (path, created_at) VALUES ('Author/Old.epub', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	st, err := Open(file)
	if err != nil {
		t.Fatalf("opening an older database: %v", err)
	}
	defer st.Close()
	book := Tombstone{Path: "Author/New.epub", Book: "uuid-1", Title: "New", Authors: []string{"Author"}}
	if err := st.AddTombstone(book); err != nil {
		t.Fatal(err)
	}
	if err := st.AddTombstone(Tombstone{Path: book.Path}); err != nil {
		t.Fatal(err)
	}
	gone, err := st.Deletions()
	if err != nil || len(gone) != 2 {
		t.Fatalf("deletions = %+v, %v, want the old one and the new one", gone, err)
	}
	if gone[0].Path != book.Path || gone[0].Book != book.Book || gone[0].Title != book.Title || len(gone[0].Authors) != 1 {
		t.Errorf("new deletion = %+v, want %+v", gone[0], book)
	}
	if gone[1].Path != "Author/Old.epub" || gone[1].Book != "" {
		t.Errorf("old deletion = %+v, want its path and no book", gone[1])
	}
}
