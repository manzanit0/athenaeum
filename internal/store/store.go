// Package store persists the library index, settings and job history in
// SQLite.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// schema creates the tables used by the store.
const schema = `
CREATE TABLE IF NOT EXISTS books (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	uuid TEXT NOT NULL UNIQUE,
	title TEXT NOT NULL,
	authors TEXT NOT NULL DEFAULT '[]',
	series TEXT NOT NULL DEFAULT '',
	series_index REAL NOT NULL DEFAULT 0,
	publisher TEXT NOT NULL DEFAULT '',
	language TEXT NOT NULL DEFAULT '',
	isbn TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	tags TEXT NOT NULL DEFAULT '[]',
	published TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL,
	cover TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS formats (
	book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	format TEXT NOT NULL,
	filename TEXT NOT NULL,
	size INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (book_id, format)
);
CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS jobs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	kind TEXT NOT NULL,
	title TEXT NOT NULL,
	status TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	result TEXT NOT NULL DEFAULT '',
	book_id INTEGER,
	created_at TEXT NOT NULL,
	finished_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS tombstones (
	path TEXT PRIMARY KEY,
	created_at TEXT NOT NULL,
	book TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	authors TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS sync_state (
	path TEXT PRIMARY KEY,
	local_size INTEGER NOT NULL,
	local_mtime INTEGER NOT NULL,
	remote_size INTEGER NOT NULL,
	remote_version TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS secrets (
	name TEXT PRIMARY KEY,
	value BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS device_files (
	path TEXT PRIMARY KEY,
	book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
	format TEXT NOT NULL
);
`

// Store is the SQLite backed index of the library.
type Store struct {
	// db is the underlying database handle.
	db *sql.DB
}

// Open opens the database at path, creating it and its schema as needed.
func Open(path string) (*Store, error) {
	pragmas := url.Values{"_pragma": {
		"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)",
	}}
	db, err := sql.Open("sqlite", "file:"+path+"?"+pragmas.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	if err := upgrade(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("upgrading schema: %w", err)
	}
	return &Store{db: db}, nil
}

// column is a column added to a table after its first version.
type column struct {
	// table is the table the column belongs to.
	table string
	// name is the name of the column.
	name string
	// definition is the type and default of the column.
	definition string
}

// added lists the columns that databases created by earlier versions
// lack.
var added = []column{
	{"tombstones", "book", "TEXT NOT NULL DEFAULT ''"},
	{"tombstones", "title", "TEXT NOT NULL DEFAULT ''"},
	{"tombstones", "authors", "TEXT NOT NULL DEFAULT '[]'"},
}

// upgrade adds to a database created by an earlier version the columns
// it lacks.
func upgrade(db *sql.DB) error {
	for _, c := range added {
		var found int
		err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.name).Scan(&found)
		if err != nil {
			return err
		}
		if found > 0 {
			continue
		}
		if _, err := db.Exec("ALTER TABLE " + c.table + " ADD COLUMN " + c.name + " " + c.definition); err != nil {
			return err
		}
	}
	return nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// now returns the current time formatted for storage.
func now() string {
	return formatTime(time.Now())
}

// formatTime formats t for storage.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseTime parses a stored timestamp, returning the zero time when s is
// empty or malformed.
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// Setting returns the stored value for key, or fallback when it is unset.
func (s *Store) Setting(key, fallback string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	return value, err
}

// SetSetting stores value under key.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Tombstone is a library file deleted or moved away since the last
// upload.
type Tombstone struct {
	// Path is where the file was, relative to the library folder.
	Path string
	// Book is the UUID of the book the file belonged to, when known.
	Book string
	// Title is the title of that book.
	Title string
	// Authors are the authors of that book.
	Authors []string
}

// AddTombstone records that a library file was deleted locally so the
// deletion can be propagated on the next sync. A path recorded before
// keeps what is known about its book unless t says more.
func (s *Store) AddTombstone(t Tombstone) error {
	_, err := s.db.Exec(`INSERT INTO tombstones (path, created_at, book, title, authors) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET book = excluded.book, title = excluded.title, authors = excluded.authors
		WHERE excluded.book != ''`,
		t.Path, now(), t.Book, t.Title, encodeList(t.Authors))
	return err
}

// Tombstones returns the library paths deleted since the last sync.
func (s *Store) Tombstones() ([]string, error) {
	gone, err := s.Deletions()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, t := range gone {
		paths = append(paths, t.Path)
	}
	return paths, nil
}

// Deletions returns the library files deleted since the last sync with
// what is known about the books they belonged to.
func (s *Store) Deletions() ([]Tombstone, error) {
	rows, err := s.db.Query(`SELECT path, book, title, authors FROM tombstones ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var gone []Tombstone
	for rows.Next() {
		var t Tombstone
		var authors string
		if err := rows.Scan(&t.Path, &t.Book, &t.Title, &authors); err != nil {
			return nil, err
		}
		t.Authors = decodeList(authors)
		gone = append(gone, t)
	}
	return gone, rows.Err()
}

// ClearTombstones forgets all recorded deletions.
func (s *Store) ClearTombstones() error {
	_, err := s.db.Exec(`DELETE FROM tombstones`)
	return err
}

// DeviceFile links a file on the e-reader to the library book it came from.
type DeviceFile struct {
	// Path is the file path relative to the device root.
	Path string `json:"path"`
	// BookID identifies the library book.
	BookID int64 `json:"book_id"`
	// Format is the format that was sent.
	Format string `json:"format"`
}

// SetDeviceFile records that the device file at path holds the given book.
func (s *Store) SetDeviceFile(f DeviceFile) error {
	_, err := s.db.Exec(`INSERT INTO device_files (path, book_id, format) VALUES (?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET book_id = excluded.book_id, format = excluded.format`,
		f.Path, f.BookID, f.Format)
	return err
}

// DeleteDeviceFile forgets the device file at path.
func (s *Store) DeleteDeviceFile(path string) error {
	_, err := s.db.Exec(`DELETE FROM device_files WHERE path = ?`, path)
	return err
}

// DeviceFiles returns the known device files keyed by path.
func (s *Store) DeviceFiles() (map[string]DeviceFile, error) {
	rows, err := s.db.Query(`SELECT path, book_id, format FROM device_files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := map[string]DeviceFile{}
	for rows.Next() {
		var f DeviceFile
		if err := rows.Scan(&f.Path, &f.BookID, &f.Format); err != nil {
			return nil, err
		}
		files[f.Path] = f
	}
	return files, rows.Err()
}

// Secret returns the value kept under name, or ErrNotFound. Values are
// stored as they are given: what must stay secret is encrypted by the
// caller.
func (s *Store) Secret(name string) ([]byte, error) {
	var value []byte
	err := s.db.QueryRow(`SELECT value FROM secrets WHERE name = ?`, name).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return value, err
}

// SetSecret keeps value under name, replacing what was there.
func (s *Store) SetSecret(name string, value []byte) error {
	_, err := s.db.Exec(`INSERT INTO secrets (name, value) VALUES (?, ?)
		ON CONFLICT(name) DO UPDATE SET value = excluded.value`, name, value)
	return err
}

// DeleteSecret forgets the value kept under name, if any.
func (s *Store) DeleteSecret(name string) error {
	_, err := s.db.Exec(`DELETE FROM secrets WHERE name = ?`, name)
	return err
}
