package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/manzanit0/athenaeum/internal/meta"
)

// Book is a library entry with its metadata and available formats.
type Book struct {
	// ID is the database identifier.
	ID int64 `json:"id"`
	// UUID identifies the book across library copies.
	UUID string `json:"uuid"`
	meta.Metadata
	// Path is the folder holding the book's files, relative to the
	// library root.
	Path string `json:"path"`
	// Cover is the name of the cover file in the library's details
	// folder, if any.
	Cover string `json:"cover"`
	// Formats lists the files available for the book.
	Formats []Format `json:"formats"`
	// CreatedAt is when the book was added.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when the book last changed.
	UpdatedAt time.Time `json:"updated_at"`
}

// Format is one file of a book.
type Format struct {
	// Format is the lower case file extension.
	Format string `json:"format"`
	// Filename is the file name inside the folder of the book.
	Filename string `json:"filename"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
	// CreatedAt is when the file was added.
	CreatedAt time.Time `json:"created_at"`
}

// Format returns the book's file in the given format.
func (b *Book) Format(format string) (Format, bool) {
	for _, f := range b.Formats {
		if f.Format == format {
			return f, true
		}
	}
	return Format{}, false
}

// ListOptions filters and orders a book listing.
type ListOptions struct {
	// Query holds space separated search terms that must all match.
	Query string
	// Sort is one of title, author, published, publisher, language,
	// added or updated.
	Sort string
	// Desc reverses the sort order.
	Desc bool
	// Filter narrows the listing by tag, author, language and format.
	Filter Filter
}

// bookColumns lists the book columns in scan order.
const bookColumns = `id, uuid, title, authors, series, series_index, publisher,
	language, isbn, description, tags, published, path, cover, created_at, updated_at`

// sortColumns maps sort names to SQL ordering expressions. Books that
// lack the property sorted by come last in either direction.
var sortColumns = map[string]string{
	"title":     "title COLLATE NOCASE",
	"author":    "authors = '[]', authors COLLATE NOCASE",
	"published": "published = '', published",
	"publisher": "publisher = '', publisher COLLATE NOCASE",
	"language":  "language = '', language COLLATE NOCASE",
	"added":     "created_at",
	"updated":   "updated_at",
}

// searchColumns lists the columns matched by search terms.
var searchColumns = []string{"title", "authors", "series", "tags", "publisher", "isbn"}

// scanner is satisfied by sql.Row and sql.Rows.
type scanner interface {
	// Scan copies the current row into dest.
	Scan(dest ...any) error
}

// scanBook reads a book row from sc.
func scanBook(sc scanner) (*Book, error) {
	var b Book
	var authors, tags, created, updated string
	err := sc.Scan(&b.ID, &b.UUID, &b.Title, &authors, &b.Series, &b.SeriesIndex,
		&b.Publisher, &b.Language, &b.ISBN, &b.Description, &tags, &b.Published,
		&b.Path, &b.Cover, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b.Authors = decodeList(authors)
	b.Tags = decodeList(tags)
	b.CreatedAt = parseTime(created)
	b.UpdatedAt = parseTime(updated)
	b.Formats = []Format{}
	return &b, nil
}

// decodeList decodes a JSON array of strings, returning an empty list on
// malformed input.
func decodeList(s string) []string {
	list := []string{}
	_ = json.Unmarshal([]byte(s), &list)
	return list
}

// encodeList encodes list as a JSON array of strings.
func encodeList(list []string) string {
	data, err := json.Marshal(meta.CleanList(list))
	if err != nil {
		return "[]"
	}
	return string(data)
}

// CreateBook inserts b and sets its ID and timestamps.
func (s *Store) CreateBook(b *Book) error {
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now().UTC()
	}
	b.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO books (uuid, title, authors, series, series_index,
		publisher, language, isbn, description, tags, published, path, cover, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.UUID, b.Title, encodeList(b.Authors), b.Series, b.SeriesIndex, b.Publisher,
		b.Language, b.ISBN, b.Description, encodeList(b.Tags), b.Published, b.Path, b.Cover,
		formatTime(b.CreatedAt), formatTime(b.UpdatedAt))
	if err != nil {
		return err
	}
	b.ID, err = res.LastInsertId()
	return err
}

// UpdateBook saves the metadata, path and cover of b.
func (s *Store) UpdateBook(b *Book) error {
	b.UpdatedAt = time.Now().UTC()
	res, err := s.db.Exec(`UPDATE books SET title = ?, authors = ?, series = ?, series_index = ?,
		publisher = ?, language = ?, isbn = ?, description = ?, tags = ?, published = ?,
		path = ?, cover = ?, updated_at = ? WHERE id = ?`,
		b.Title, encodeList(b.Authors), b.Series, b.SeriesIndex, b.Publisher, b.Language,
		b.ISBN, b.Description, encodeList(b.Tags), b.Published, b.Path, b.Cover,
		formatTime(b.UpdatedAt), b.ID)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// requireRow returns ErrNotFound when res affected no rows.
func requireRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// DeleteBook removes the book with the given ID and its formats.
func (s *Store) DeleteBook(id int64) error {
	res, err := s.db.Exec(`DELETE FROM books WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// Book returns the book with the given ID.
func (s *Store) Book(id int64) (*Book, error) {
	b, err := scanBook(s.db.QueryRow(`SELECT `+bookColumns+` FROM books WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	return b, s.loadFormats([]*Book{b})
}

// BookByUUID returns the book with the given UUID.
func (s *Store) BookByUUID(uuid string) (*Book, error) {
	b, err := scanBook(s.db.QueryRow(`SELECT `+bookColumns+` FROM books WHERE uuid = ?`, uuid))
	if err != nil {
		return nil, err
	}
	return b, s.loadFormats([]*Book{b})
}

// Books returns the books matching opts.
func (s *Store) Books(opts ListOptions) ([]*Book, error) {
	where, args := searchClause(opts.Query)
	order, ok := sortColumns[opts.Sort]
	if !ok {
		order = sortColumns["title"]
	}
	if opts.Desc {
		order += " DESC"
	}
	rows, err := s.db.Query(`SELECT `+bookColumns+` FROM books`+where+` ORDER BY `+order+`, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	books := []*Book{}
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		books = append(books, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.loadFormats(books); err != nil {
		return nil, err
	}
	return slices.DeleteFunc(books, func(b *Book) bool { return !opts.Filter.Match(b) }), nil
}

// searchClause builds a WHERE clause requiring every term of query to
// match at least one searchable column.
func searchClause(query string) (string, []any) {
	var clauses []string
	var args []any
	escape := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	for _, term := range strings.Fields(query) {
		var ors []string
		for _, col := range searchColumns {
			ors = append(ors, col+` LIKE ? ESCAPE '\'`)
			args = append(args, "%"+escape.Replace(term)+"%")
		}
		clauses = append(clauses, "("+strings.Join(ors, " OR ")+")")
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// loadFormats fills in the formats of books.
func (s *Store) loadFormats(books []*Book) error {
	byID := make(map[int64]*Book, len(books))
	for _, b := range books {
		byID[b.ID] = b
	}
	query := `SELECT book_id, format, filename, size, created_at FROM formats`
	var args []any
	if len(books) == 1 {
		query += ` WHERE book_id = ?`
		args = append(args, books[0].ID)
	}
	rows, err := s.db.Query(query+` ORDER BY format`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var f Format
		var created string
		if err := rows.Scan(&id, &f.Format, &f.Filename, &f.Size, &created); err != nil {
			return err
		}
		f.CreatedAt = parseTime(created)
		if b, ok := byID[id]; ok {
			b.Formats = append(b.Formats, f)
		}
	}
	return rows.Err()
}

// SetFormat adds or replaces a format of the book with the given ID.
func (s *Store) SetFormat(bookID int64, f Format) error {
	_, err := s.db.Exec(`INSERT INTO formats (book_id, format, filename, size, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(book_id, format) DO UPDATE SET filename = excluded.filename,
		size = excluded.size, created_at = excluded.created_at`,
		bookID, f.Format, f.Filename, f.Size, now())
	return err
}

// DeleteFormat removes a format of the book with the given ID.
func (s *Store) DeleteFormat(bookID int64, format string) error {
	res, err := s.db.Exec(`DELETE FROM formats WHERE book_id = ? AND format = ?`, bookID, format)
	if err != nil {
		return err
	}
	return requireRow(res)
}

// ReplaceFormats sets the formats of the book with the given ID to exactly
// formats.
func (s *Store) ReplaceFormats(bookID int64, formats []Format) error {
	if _, err := s.db.Exec(`DELETE FROM formats WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	for _, f := range formats {
		if err := s.SetFormat(bookID, f); err != nil {
			return err
		}
	}
	return nil
}
