package library

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/store"
)

// legacyName is the details file earlier versions kept in a folder of its
// own for every book.
const legacyName = "metadata.json"

// formatPreference orders the formats by how much their embedded details
// can be trusted, best first.
var formatPreference = []string{"epub", "kepub", "azw3", "mobi", "azw", "prc", "pdf"}

// sidecar is the description of a book kept in the library folder so the
// database can be rebuilt from the folder alone.
type sidecar struct {
	// UUID identifies the book across library copies.
	UUID string `json:"uuid"`
	meta.Metadata
	// CreatedAt is when the book was first added.
	CreatedAt time.Time `json:"created_at"`
	// Files lists the files of the book relative to the library folder.
	Files []string `json:"files"`
}

// RescanResult summarises how a rescan changed the database.
type RescanResult struct {
	// Added is the number of books found on disk and added.
	Added int `json:"added"`
	// Updated is the number of books whose record changed.
	Updated int `json:"updated"`
	// Removed is the number of books no longer on disk.
	Removed int `json:"removed"`
}

// claims records which files of the library belong to a book.
type claims struct {
	// paths holds the library relative paths of the files.
	paths map[string]bool
	// infos describes the files, to recognise one reached by another
	// spelling of its path.
	infos []fs.FileInfo
}

// add records the file at rel as belonging to a book.
func (c *claims) add(rel string, info fs.FileInfo) {
	c.paths[rel] = true
	c.infos = append(c.infos, info)
}

// has reports whether the file at rel belongs to a book.
func (c *claims) has(rel string, info fs.FileInfo) bool {
	return c.paths[rel] || slices.ContainsFunc(c.infos, func(other fs.FileInfo) bool {
		return os.SameFile(other, info)
	})
}

// find is one ebook in the library that belongs to no book.
type find struct {
	// dir is the library relative folder holding the file.
	dir string
	// file describes the file.
	file store.Format
}

// sidecarPath returns the library relative path of the details file of
// the book identified by uuid.
func sidecarPath(uuid string) string {
	return path.Join(metaDir, uuid+".json")
}

// validID reports whether uuid is safe to use in a file name.
func validID(uuid string) bool {
	plain := func(r rune) bool {
		return r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	}
	return uuid != "" && strings.IndexFunc(uuid, func(r rune) bool { return !plain(r) }) < 0
}

// readSidecar reads the details file at file, reporting whether it
// describes a book.
func readSidecar(file string) (sidecar, bool) {
	var sc sidecar
	data, err := os.ReadFile(file)
	if err != nil || json.Unmarshal(data, &sc) != nil || !validID(sc.UUID) {
		return sc, false
	}
	sc.Authors = meta.CleanList(sc.Authors)
	sc.Tags = meta.CleanList(sc.Tags)
	sc.Published = meta.Date(sc.Published)
	return sc, true
}

// writeSidecar saves b's details and the list of its files.
func (l *Library) writeSidecar(b *store.Book) error {
	sc := sidecar{UUID: b.UUID, Metadata: b.Metadata, CreatedAt: b.CreatedAt, Files: []string{}}
	for _, f := range b.Formats {
		sc.Files = append(sc.Files, path.Join(b.Path, f.Filename))
	}
	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.abs(metaDir), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(l.tmp, "sidecar-*.json")
	if err != nil {
		return err
	}
	_, err = tmp.Write(append(data, '\n'))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), l.abs(sidecarPath(b.UUID)))
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// Rescan makes the database match the library folder. Books are known by
// their details files. Ebooks that belong to no book, such as files
// copied or synced into the folder, become books, and every book's files
// are moved to where its details say they belong. Books whose files are
// gone are dropped.
func (l *Library) Rescan(ctx context.Context) (RescanResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var result RescanResult
	if err := l.convertLegacy(); err != nil {
		return result, err
	}
	claimed := &claims{paths: map[string]bool{}}
	books, err := l.known(claimed)
	if err != nil {
		return result, err
	}
	if books, err = l.adopt(ctx, books, claimed); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, b := range books {
		if err := l.settle(b, &result); err != nil {
			return result, err
		}
		seen[b.UUID] = true
	}
	stored, err := l.store.Books(store.ListOptions{})
	if err != nil {
		return result, err
	}
	for _, b := range stored {
		if seen[b.UUID] {
			continue
		}
		if err := l.store.DeleteBook(b.ID); err != nil {
			return result, err
		}
		result.Removed++
	}
	return result, nil
}

// convertLegacy turns the folders earlier versions kept for every book,
// each with its own details file and cover, into books of this layout.
func (l *Library) convertLegacy() error {
	var folders []string
	err := filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p == l.abs(metaDir) {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == legacyName {
			folders = append(folders, filepath.Dir(p))
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, dir := range folders {
		if err := l.convertFolder(dir); err != nil {
			return err
		}
	}
	return nil
}

// convertFolder writes the details of the book kept in the legacy folder
// dir where this layout expects them. A folder whose book is already
// known is left alone.
func (l *Library) convertFolder(dir string) error {
	sc, ok := readSidecar(filepath.Join(dir, legacyName))
	if !ok {
		return nil
	}
	if _, err := os.Lstat(l.abs(sidecarPath(sc.UUID))); err == nil {
		return nil
	}
	rel, err := filepath.Rel(l.root, dir)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	formats, cover, err := scanFolder(dir)
	if err != nil {
		return err
	}
	b := &store.Book{UUID: sc.UUID, Metadata: sc.Metadata, Path: rel, Formats: formats, CreatedAt: sc.CreatedAt}
	if cover != "" {
		if err := l.move(b, path.Join(rel, cover), path.Join(metaDir, sc.UUID+filepath.Ext(cover))); err != nil {
			return err
		}
	}
	if err := l.writeSidecar(b); err != nil {
		return err
	}
	return l.discard(b, path.Join(rel, legacyName))
}

// scanFolder lists the ebook files and the cover file found in dir.
func scanFolder(dir string) ([]store.Format, string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}
	var formats []store.Format
	var cover string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, "", err
		}
		if format, ok := meta.FormatOf(e.Name()); ok {
			formats = append(formats, store.Format{Format: format, Filename: e.Name(), Size: info.Size()})
			continue
		}
		if stem(e.Name()) == "cover" {
			cover = e.Name()
		}
	}
	return formats, cover, nil
}

// known returns the books described by the details files, oldest first,
// each with the files it lists that are still there. A book that lists
// files of which none is left is not returned.
func (l *Library) known(claimed *claims) ([]*store.Book, error) {
	entries, err := os.ReadDir(l.abs(metaDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	covers := map[string]string{}
	var sidecars []sidecar
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if filepath.Ext(name) != ".json" {
			covers[stem(name)] = name
			continue
		}
		if sc, ok := readSidecar(filepath.Join(l.abs(metaDir), name)); ok && sc.UUID == stem(name) {
			sidecars = append(sidecars, sc)
		}
	}
	slices.SortFunc(sidecars, func(a, b sidecar) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.UUID, b.UUID)
	})
	var books []*store.Book
	for _, sc := range sidecars {
		if b := l.resolve(sc, claimed); len(b.Formats) > 0 || len(sc.Files) == 0 {
			b.Cover = covers[b.UUID]
			books = append(books, b)
		}
	}
	return books, nil
}

// resolve returns the book sc describes with the files it lists that
// exist, share a folder and belong to no other book.
func (l *Library) resolve(sc sidecar, claimed *claims) *store.Book {
	b := &store.Book{UUID: sc.UUID, Metadata: sc.Metadata, CreatedAt: sc.CreatedAt, Formats: []store.Format{}}
	b.Path = authorDir(b)
	for _, rel := range sc.Files {
		rel = path.Clean(filepath.ToSlash(rel))
		format, ok := meta.FormatOf(rel)
		if !ok || !filepath.IsLocal(filepath.FromSlash(rel)) || strings.HasPrefix(rel, metaDir+"/") {
			continue
		}
		info, err := os.Lstat(l.abs(rel))
		if err != nil || !info.Mode().IsRegular() || claimed.has(rel, info) {
			continue
		}
		if _, twice := b.Format(format); twice || len(b.Formats) > 0 && path.Dir(rel) != b.Path {
			continue
		}
		claimed.add(rel, info)
		b.Path = path.Dir(rel)
		b.Formats = append(b.Formats, store.Format{Format: format, Filename: path.Base(rel), Size: info.Size()})
	}
	return b
}

// loose returns the ebooks in the library that belong to no book, grouped
// into the files that make up one book: those that share a folder and a
// name and differ in format.
func (l *Library) loose(claimed *claims) ([][]find, error) {
	var groups [][]find
	index := map[string]int{}
	err := filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == l.root {
			return err
		}
		hidden := strings.HasPrefix(d.Name(), ".")
		if d.IsDir() {
			_, err := os.Lstat(filepath.Join(p, legacyName))
			if hidden || p == l.abs(metaDir) || err == nil {
				return filepath.SkipDir
			}
			return nil
		}
		format, ok := meta.FormatOf(d.Name())
		if hidden || !ok || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(l.root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if claimed.has(rel, info) {
			return nil
		}
		found := find{dir: path.Dir(rel), file: store.Format{Format: format, Filename: d.Name(), Size: info.Size()}}
		key := strings.ToLower(path.Join(found.dir, stem(d.Name())))
		at, grouped := index[key]
		if grouped && !slices.ContainsFunc(groups[at], func(f find) bool { return f.file.Format == format }) {
			groups[at] = append(groups[at], found)
			return nil
		}
		index[key] = len(groups)
		groups = append(groups, []find{found})
		return nil
	})
	return groups, err
}

// adopt turns the ebooks that belong to no book into books and returns
// books with them. Files that match a book by title and first author,
// in formats that book lacks, become formats of it.
func (l *Library) adopt(ctx context.Context, books []*store.Book, claimed *claims) ([]*store.Book, error) {
	groups, err := l.loose(claimed)
	if err != nil {
		return nil, err
	}
	for _, group := range groups {
		m, cover := l.describe(ctx, group)
		b := match(books, m, group)
		if b == nil {
			b = &store.Book{UUID: newUUID(), Metadata: m, Path: group[0].dir, CreatedAt: time.Now().UTC()}
			books = append(books, b)
		}
		if err := l.join(b, group); err != nil {
			return nil, err
		}
		if b.Cover == "" {
			if err := l.saveCover(b, cover); err != nil {
				return nil, err
			}
		}
		if err := l.writeSidecar(b); err != nil {
			return nil, err
		}
	}
	return books, nil
}

// describe reads the details and the cover of the book made up of group
// from its files, trusting the best format first.
func (l *Library) describe(ctx context.Context, group []find) (meta.Metadata, *meta.Cover) {
	rank := func(f find) int {
		if i := slices.Index(formatPreference, f.file.Format); i >= 0 {
			return i
		}
		return len(formatPreference)
	}
	slices.SortStableFunc(group, func(a, b find) int { return rank(a) - rank(b) })
	var m meta.Metadata
	var cover *meta.Cover
	for i, f := range group {
		read, found := meta.Read(l.abs(path.Join(f.dir, f.file.Filename)), f.file.Filename)
		if i == 0 {
			m = read
		}
		if cover == nil {
			cover = found
		}
	}
	if cover == nil && l.extractCover != nil {
		cover = l.extractCover(ctx, l.abs(path.Join(group[0].dir, group[0].file.Filename)))
	}
	return m, cover
}

// match returns the first of books with the title and first author of m
// that has none of the formats in group, or nil.
func match(books []*store.Book, m meta.Metadata, group []find) *store.Book {
	key := func(title string, authors []string) string {
		return strings.ToLower(title + "\n" + strings.Join(authors[:min(len(authors), 1)], ""))
	}
	for _, b := range books {
		clash := slices.ContainsFunc(group, func(f find) bool {
			_, has := b.Format(f.file.Format)
			return has
		})
		if !clash && key(b.Title, b.Authors) == key(m.Title, m.Authors) {
			return b
		}
	}
	return nil
}

// join makes the files in group formats of b. A book that has files
// already gets them next to those, under the same name.
func (l *Library) join(b *store.Book, group []find) error {
	if len(b.Formats) == 0 {
		b.Path = group[0].dir
		for _, f := range group {
			b.Formats = append(b.Formats, f.file)
		}
		return nil
	}
	base := stem(b.Formats[0].Filename)
	for _, f := range group {
		name := base + "." + f.file.Format
		if err := l.move(b, path.Join(f.dir, f.file.Filename), path.Join(b.Path, name)); err != nil {
			return err
		}
		b.Formats = append(b.Formats, store.Format{Format: f.file.Format, Filename: name, Size: f.file.Size})
	}
	l.prune(group[0].dir)
	return nil
}

// settle moves the files of b to where its details say they belong and
// brings its database record up to date.
func (l *Library) settle(b *store.Book, result *RescanResult) error {
	moved, err := l.arrange(b)
	if err != nil {
		return err
	}
	if moved {
		if err := l.writeSidecar(b); err != nil {
			return err
		}
	}
	existing, err := l.store.BookByUUID(b.UUID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		if err := l.store.CreateBook(b); err != nil {
			return err
		}
		result.Added++
		return l.store.ReplaceFormats(b.ID, b.Formats)
	case err != nil:
		return err
	}
	if sameBook(existing, b) {
		return nil
	}
	b.ID = existing.ID
	if err := l.store.UpdateBook(b); err != nil {
		return err
	}
	result.Updated++
	return l.store.ReplaceFormats(b.ID, b.Formats)
}

// sameBook reports whether the stored book a already matches the book b
// found on disk.
func sameBook(a, b *store.Book) bool {
	if a.Path != b.Path || a.Cover != b.Cover || !reflect.DeepEqual(a.Metadata, b.Metadata) {
		return false
	}
	if len(a.Formats) != len(b.Formats) {
		return false
	}
	for _, f := range b.Formats {
		if old, ok := a.Format(f.Format); !ok || old.Filename != f.Filename || old.Size != f.Size {
			return false
		}
	}
	return true
}
