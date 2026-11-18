// Package library manages the book files on disk and keeps the database
// index in step with them.
package library

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/store"
)

var (
	// ErrUnsupportedFormat is returned for files the library does not accept.
	ErrUnsupportedFormat = errors.New("unsupported ebook format")
	// ErrInvalidCover is returned when cover data is not a known image type.
	ErrInvalidCover = errors.New("cover must be a JPEG, PNG, GIF or WebP image")
	// ErrTitleRequired is returned when metadata has no title.
	ErrTitleRequired = errors.New("title is required")
	// ErrInvalidDate is returned when a publication date is not a date.
	ErrInvalidDate = errors.New("the publication date must be a year, a year and month or a full date, such as 2019, 2019-03 or 2019-03-12")
)

// maxNameRunes bounds the length of generated file and folder names.
const maxNameRunes = 80

// metaDir is the folder inside the library that holds the details and the
// cover of every book, named after the identifier of the book.
const metaDir = "_athenaeum"

// CoverExtractor extracts a cover from the ebook at path using an external
// tool, returning nil when it cannot.
type CoverExtractor func(ctx context.Context, path string) *meta.Cover

// Library stores books under a root folder, one folder per author, with
// the files of a book side by side in the folder of its first author.
type Library struct {
	// root is the folder holding the author folders.
	root string
	// tmp is a scratch folder on the same filesystem as root.
	tmp string
	// store is the database index.
	store *store.Store
	// extractCover is an optional fallback for formats without a native
	// cover parser.
	extractCover CoverExtractor
	// mu serialises changes to the files on disk.
	mu sync.Mutex
}

// New creates a library rooted at root that uses tmp for scratch files.
func New(root, tmp string, st *store.Store, extractCover CoverExtractor) (*Library, error) {
	for _, dir := range []string{root, tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &Library{root: root, tmp: tmp, store: st, extractCover: extractCover}, nil
}

// Root returns the folder holding the author folders.
func (l *Library) Root() string {
	return l.root
}

// TempDir returns a scratch folder on the same filesystem as the library.
func (l *Library) TempDir() string {
	return l.tmp
}

// FormatPath returns the absolute path of a book's file.
func (l *Library) FormatPath(b *store.Book, f store.Format) string {
	return filepath.Join(l.root, filepath.FromSlash(b.Path), f.Filename)
}

// CoverPath returns the absolute path of a book's cover, or an empty
// string when it has none.
func (l *Library) CoverPath(b *store.Book) string {
	if b.Cover == "" {
		return ""
	}
	return filepath.Join(l.root, metaDir, b.Cover)
}

// abs returns the absolute path of the library relative path rel.
func (l *Library) abs(rel string) string {
	return filepath.Join(l.root, filepath.FromSlash(rel))
}

// Import adds the ebook read from r as a new book. name is the original
// file name and determines the format.
func (l *Library) Import(ctx context.Context, name string, r io.Reader) (*store.Book, error) {
	format, ok := meta.FormatOf(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, name)
	}
	tmp, err := l.spool(format, r)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	m, cover := meta.Read(tmp, name)
	if cover == nil && l.extractCover != nil {
		cover = l.extractCover(ctx, tmp)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := &store.Book{UUID: newUUID(), Metadata: m}
	b.Path = authorDir(b)
	if err := l.saveCover(b, cover); err != nil {
		return nil, err
	}
	if err := l.store.CreateBook(b); err != nil {
		os.Remove(l.CoverPath(b))
		return nil, err
	}
	if err := l.placeFormat(b, format, tmp); err != nil {
		return nil, err
	}
	return l.saved(b.ID)
}

// saveCover stores cover, which may be nil, as the cover of b.
func (l *Library) saveCover(b *store.Book, cover *meta.Cover) error {
	if cover == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(l.root, metaDir), 0o755); err != nil {
		return err
	}
	b.Cover = b.UUID + "." + cover.Ext
	return os.WriteFile(l.CoverPath(b), cover.Data, 0o644)
}

// saved writes the details file of the book with the given ID and returns
// the book as it is stored.
func (l *Library) saved(id int64) (*store.Book, error) {
	b, err := l.store.Book(id)
	if err != nil {
		return nil, err
	}
	return b, l.writeSidecar(b)
}

// AddFormat stores the ebook read from r as an additional format of the
// book with the given ID, replacing any existing file of that format.
func (l *Library) AddFormat(id int64, name string, r io.Reader) (*store.Book, error) {
	format, ok := meta.FormatOf(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, name)
	}
	tmp, err := l.spool(format, r)
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp)
	return l.AddFormatFile(id, format, tmp)
}

// AddFormatFile moves the file at src into the book with the given ID as
// its file for format. src must be inside the library's scratch folder.
func (l *Library) AddFormatFile(id int64, format, src string) (*store.Book, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.store.Book(id)
	if err != nil {
		return nil, err
	}
	if err := l.placeFormat(b, format, src); err != nil {
		return nil, err
	}
	if err := l.store.UpdateBook(b); err != nil {
		return nil, err
	}
	return l.saved(id)
}

// placeFormat moves src next to b's other files and records it as format.
// A file b already has in that format is replaced.
func (l *Library) placeFormat(b *store.Book, format, src string) error {
	f, replacing := b.Format(format)
	if !replacing {
		base, err := l.base(b)
		if err != nil {
			return err
		}
		f = store.Format{Format: format, Filename: base + "." + format}
	}
	dst := l.FormatPath(b, f)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil && !replacing {
		return fmt.Errorf("another file is already called %s", f.Filename)
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return err
	}
	f.Size = info.Size()
	return l.store.SetFormat(b.ID, f)
}

// base returns the file name, without extension, shared by b's files. A
// book without files gets a name nothing else in its folder uses.
func (l *Library) base(b *store.Book) (string, error) {
	if len(b.Formats) > 0 {
		return stem(b.Formats[0].Filename), nil
	}
	return l.freeBase(b.Path, fileBase(b), nil)
}

// freeBase returns want, or want followed by a number, so that nothing in
// the library folder dir other than the files named in own goes by it.
func (l *Library) freeBase(dir, want string, own []string) (string, error) {
	entries, err := os.ReadDir(l.abs(dir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	taken := map[string]bool{}
	for _, e := range entries {
		if !slices.ContainsFunc(own, func(name string) bool { return strings.EqualFold(name, e.Name()) }) {
			taken[strings.ToLower(stem(e.Name()))] = true
		}
	}
	name := want
	for n := 2; taken[strings.ToLower(name)]; n++ {
		name = fmt.Sprintf("%s (%d)", want, n)
	}
	return name, nil
}

// spool copies r into a scratch file with the extension of format and
// returns its path.
func (l *Library) spool(format string, r io.Reader) (string, error) {
	f, err := os.CreateTemp(l.tmp, "upload-*."+format)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// UpdateMetadata replaces the metadata of the book with the given ID and
// moves its files to the folder and names that match.
func (l *Library) UpdateMetadata(id int64, m meta.Metadata) (*store.Book, error) {
	m.Title = strings.TrimSpace(m.Title)
	if m.Title == "" {
		return nil, ErrTitleRequired
	}
	published, ok := meta.PartialDate(m.Published)
	if !ok {
		return nil, ErrInvalidDate
	}
	m.Published = published
	m.Authors = meta.CleanList(m.Authors)
	m.Tags = store.Categories(m.Tags)
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.store.Book(id)
	if err != nil {
		return nil, err
	}
	b.Metadata = m
	moved, err := l.arrange(b)
	if err != nil {
		return nil, err
	}
	if err := l.store.UpdateBook(b); err != nil {
		return nil, err
	}
	if moved {
		if err := l.store.ReplaceFormats(b.ID, b.Formats); err != nil {
			return nil, err
		}
	}
	return l.saved(id)
}

// arrange moves b's files into the folder of its first author and under
// the name its details call for, and updates b to match. It reports
// whether any file moved.
func (l *Library) arrange(b *store.Book) (bool, error) {
	from, dir := b.Path, authorDir(b)
	var own []string
	if strings.EqualFold(from, dir) {
		for _, f := range b.Formats {
			own = append(own, f.Filename)
		}
	}
	base, err := l.freeBase(dir, fileBase(b), own)
	if err != nil {
		return false, err
	}
	b.Path = dir
	moved := false
	for i, f := range b.Formats {
		name := base + "." + f.Format
		if from == dir && f.Filename == name {
			continue
		}
		if err := l.move(b, path.Join(from, f.Filename), path.Join(dir, name)); err != nil {
			return moved, err
		}
		b.Formats[i].Filename = name
		moved = true
	}
	if moved {
		l.prune(from)
	}
	return moved, nil
}

// gone describes the file that was at rel for the next sync. It belonged
// to b, which is nil for a file of no book.
func gone(b *store.Book, rel string) store.Tombstone {
	if b == nil {
		return store.Tombstone{Path: rel}
	}
	return store.Tombstone{Path: rel, Book: b.UUID, Title: b.Title, Authors: b.Authors}
}

// move renames the library file from to to, refusing to replace another
// file, and records the old place as deleted for the next sync. The file
// belongs to b, which is nil for a file of no book.
func (l *Library) move(b *store.Book, from, to string) error {
	src, dst := l.abs(from), l.abs(to)
	here, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if there, err := os.Lstat(dst); err == nil && !os.SameFile(here, there) {
		return fmt.Errorf("another file is already called %s", to)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	return l.store.AddTombstone(gone(b, from))
}

// discard deletes the library file rel, which may already be gone, and
// records the deletion for the next sync. The file belongs to b, which is
// nil for a file of no book.
func (l *Library) discard(b *store.Book, rel string) error {
	if err := os.Remove(l.abs(rel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return l.store.AddTombstone(gone(b, rel))
}

// prune removes the library folder dir, and then the folders above it,
// for as long as they hold nothing. The folders are not recorded as
// deleted: their copies on MEGA may hold files that were never here.
func (l *Library) prune(dir string) {
	for ; dir != "" && dir != "." && dir != "/"; dir = path.Dir(dir) {
		entries, err := os.ReadDir(l.abs(dir))
		if err == nil && len(entries) == 1 && entries[0].Name() == ".DS_Store" {
			os.Remove(filepath.Join(l.abs(dir), ".DS_Store"))
		}
		if os.Remove(l.abs(dir)) != nil {
			return
		}
	}
}

// SetCover replaces the cover of the book with the given ID.
func (l *Library) SetCover(id int64, data []byte) (*store.Book, error) {
	ext := meta.ImageExt(data)
	if ext == "" {
		return nil, ErrInvalidCover
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.store.Book(id)
	if err != nil {
		return nil, err
	}
	if err := l.dropCover(b, b.UUID+"."+ext); err != nil {
		return nil, err
	}
	if err := l.saveCover(b, &meta.Cover{Data: data, Ext: ext}); err != nil {
		return nil, err
	}
	if err := l.store.UpdateBook(b); err != nil {
		return nil, err
	}
	return l.store.Book(id)
}

// RemoveCover deletes the cover of the book with the given ID.
func (l *Library) RemoveCover(id int64) (*store.Book, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.store.Book(id)
	if err != nil {
		return nil, err
	}
	if err := l.dropCover(b, ""); err != nil {
		return nil, err
	}
	b.Cover = ""
	if err := l.store.UpdateBook(b); err != nil {
		return nil, err
	}
	return l.store.Book(id)
}

// dropCover deletes b's cover file unless it is named keep, recording the
// deletion for the next sync.
func (l *Library) dropCover(b *store.Book, keep string) error {
	if b.Cover == "" || b.Cover == keep {
		return nil
	}
	return l.discard(b, path.Join(metaDir, b.Cover))
}

// Delete removes the book with the given ID and all its files.
func (l *Library) Delete(id int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.store.Book(id)
	if err != nil {
		return err
	}
	doomed := []string{sidecarPath(b.UUID)}
	if b.Cover != "" {
		doomed = append(doomed, path.Join(metaDir, b.Cover))
	}
	for _, f := range b.Formats {
		doomed = append(doomed, path.Join(b.Path, f.Filename))
	}
	for _, rel := range doomed {
		if err := l.discard(b, rel); err != nil {
			return err
		}
	}
	l.prune(b.Path)
	return l.store.DeleteBook(id)
}

// DeleteFormat removes one format of the book with the given ID.
func (l *Library) DeleteFormat(id int64, format string) (*store.Book, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.store.Book(id)
	if err != nil {
		return nil, err
	}
	f, ok := b.Format(format)
	if !ok {
		return nil, store.ErrNotFound
	}
	if err := l.discard(b, path.Join(b.Path, f.Filename)); err != nil {
		return nil, err
	}
	if err := l.store.DeleteFormat(id, format); err != nil {
		return nil, err
	}
	l.prune(b.Path)
	return l.saved(id)
}

// authorDir returns the library relative folder for b's files, named
// after its first author.
func authorDir(b *store.Book) string {
	return sanitize(firstAuthor(b))
}

// stem returns the file name without its extension.
func stem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// fileBase returns the file name, without extension, of b's files.
func fileBase(b *store.Book) string {
	return sanitize(b.Title) + " - " + sanitize(firstAuthor(b))
}

// firstAuthor returns the primary author of b, or "Unknown".
func firstAuthor(b *store.Book) string {
	if len(b.Authors) == 0 {
		return "Unknown"
	}
	return b.Authors[0]
}

// sanitize makes s safe to use as a file or folder name. A colon that
// introduces a subtitle becomes a dash, which reads better in a listing.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, ": ", " - ")
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, s)
	s = strings.Trim(strings.TrimSpace(s), ".")
	if runes := []rune(s); len(runes) > maxNameRunes {
		s = strings.TrimSpace(string(runes[:maxNameRunes]))
	}
	if s == "" {
		return "Unknown"
	}
	return s
}

// newUUID returns a random version 4 UUID.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0F | 0x40
	b[8] = b[8]&0x3F | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
