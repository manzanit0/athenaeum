package library_test

import (
	"bytes"
	"context"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/library"
	"github.com/manzanit0/athenaeum/internal/store"
)

// sample is the book imported by the tests.
var sample = booktest.Book{
	Title:  "Kindred",
	Author: "Octavia E. Butler",
	Tags:   []string{"Fiction"},
	Cover:  booktest.PNG(color.RGBA{R: 200, A: 255}),
}

// newLibrary returns a library and its store rooted in a temporary
// folder.
func newLibrary(t *testing.T) (*library.Library, *store.Store) {
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
	return lib, st
}

// importSample imports the sample EPUB into lib.
func importSample(t *testing.T, lib *library.Library) *store.Book {
	t.Helper()
	book, err := lib.Import(context.Background(), "kindred.epub", bytes.NewReader(booktest.EPUB(sample)))
	if err != nil {
		t.Fatal(err)
	}
	return book
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// write stores data in the library file rel, creating its folder.
func write(t *testing.T, lib *library.Library, rel string, data []byte) {
	t.Helper()
	file := filepath.Join(lib.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// rescan rescans lib and fails the test on error.
func rescan(t *testing.T, lib *library.Library) library.RescanResult {
	t.Helper()
	result, err := lib.Rescan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// files returns the library relative paths of the files of the books in
// st, sorted.
func files(t *testing.T, st *store.Store) []string {
	t.Helper()
	books, err := st.Books(store.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, b := range books {
		for _, f := range b.Formats {
			out = append(out, b.Path+"/"+f.Filename)
		}
	}
	slices.Sort(out)
	return out
}

// TestImport checks that importing stores the file in the folder of its
// author and the cover and details in the folder kept for those.
func TestImport(t *testing.T) {
	lib, _ := newLibrary(t)
	book := importSample(t, lib)
	if book.Title != sample.Title || !reflect.DeepEqual(book.Authors, []string{sample.Author}) {
		t.Errorf("imported %q by %q", book.Title, book.Authors)
	}
	if len(book.Formats) != 1 || book.Formats[0].Format != "epub" {
		t.Fatalf("formats = %+v", book.Formats)
	}
	if book.Path != "Octavia E. Butler" || book.Formats[0].Filename != "Kindred - Octavia E. Butler.epub" {
		t.Errorf("stored as %q in %q", book.Formats[0].Filename, book.Path)
	}
	for _, path := range []string{
		filepath.Join(lib.Root(), "Octavia E. Butler", "Kindred - Octavia E. Butler.epub"),
		filepath.Join(lib.Root(), "_athenaeum", book.UUID+".png"),
		filepath.Join(lib.Root(), "_athenaeum", book.UUID+".json"),
	} {
		if !exists(path) {
			t.Errorf("%s was not created", path)
		}
	}
	if lib.CoverPath(book) != filepath.Join(lib.Root(), "_athenaeum", book.UUID+".png") {
		t.Errorf("cover path = %q", lib.CoverPath(book))
	}
	if _, err := lib.Import(context.Background(), "virus.exe", bytes.NewReader(nil)); err == nil {
		t.Error("importing an unsupported format succeeded")
	}
}

// TestSameTitleTwice checks that a second book with the title and author
// of another gets a name of its own, for all its formats.
func TestSameTitleTwice(t *testing.T) {
	lib, st := newLibrary(t)
	importSample(t, lib)
	second := importSample(t, lib)
	second, err := lib.AddFormat(second.ID, "kindred.mobi", bytes.NewReader(booktest.MOBI(sample)))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"Octavia E. Butler/Kindred - Octavia E. Butler (2).epub",
		"Octavia E. Butler/Kindred - Octavia E. Butler (2).mobi",
		"Octavia E. Butler/Kindred - Octavia E. Butler.epub",
	}
	if got := files(t, st); !reflect.DeepEqual(got, want) {
		t.Errorf("files = %q, want %q", got, want)
	}
	if result := rescan(t, lib); result != (library.RescanResult{}) {
		t.Errorf("rescan = %+v, want the names left as they are", result)
	}
}

// TestUpdateMetadataRelocates checks that renaming a book moves its
// files and records where they were for sync.
func TestUpdateMetadataRelocates(t *testing.T) {
	lib, st := newLibrary(t)
	book := importSample(t, lib)
	old := book.Path + "/" + book.Formats[0].Filename
	m := book.Metadata
	m.Title = "Kindred: A Novel"
	m.Authors = []string{"Octavia Butler"}
	updated, err := lib.UpdateMetadata(book.ID, m)
	if err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(lib.Root(), "Octavia E. Butler")) {
		t.Error("the folder of the old author still exists")
	}
	if want := "Kindred - A Novel - Octavia Butler.epub"; updated.Path != "Octavia Butler" || updated.Formats[0].Filename != want {
		t.Errorf("stored as %q in %q, want %q", updated.Formats[0].Filename, updated.Path, want)
	}
	if !exists(lib.FormatPath(updated, updated.Formats[0])) || !exists(lib.CoverPath(updated)) {
		t.Error("the file was not moved or the cover was lost")
	}
	tombstones, err := st.Tombstones()
	if err != nil || !reflect.DeepEqual(tombstones, []string{old}) {
		t.Errorf("tombstones = %q, %v; want the old file", tombstones, err)
	}
	if result := rescan(t, lib); result != (library.RescanResult{}) {
		t.Errorf("rescan after renaming = %+v, want no change", result)
	}
	m.Title = " "
	if _, err := lib.UpdateMetadata(book.ID, m); err == nil {
		t.Error("an empty title was accepted")
	}
}

// TestFormatsAndCover checks adding and removing formats and covers.
func TestFormatsAndCover(t *testing.T) {
	lib, st := newLibrary(t)
	book := importSample(t, lib)
	book, err := lib.AddFormat(book.ID, "kindred.mobi", bytes.NewReader(booktest.MOBI(sample)))
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Formats) != 2 {
		t.Fatalf("formats = %+v", book.Formats)
	}
	mobi, _ := book.Format("mobi")
	path := lib.FormatPath(book, mobi)
	if book, err = lib.DeleteFormat(book.ID, "mobi"); err != nil {
		t.Fatal(err)
	}
	if len(book.Formats) != 1 || exists(path) {
		t.Errorf("mobi was not removed: %+v", book.Formats)
	}
	if result := rescan(t, lib); result != (library.RescanResult{}) {
		t.Errorf("rescan after removing a format = %+v, want no change", result)
	}
	if _, err := lib.SetCover(book.ID, []byte("not an image")); err == nil {
		t.Error("an invalid cover was accepted")
	}
	cover := lib.CoverPath(book)
	if book, err = lib.RemoveCover(book.ID); err != nil || book.Cover != "" || exists(cover) {
		t.Errorf("cover = %q, %v", book.Cover, err)
	}
	if err := lib.Delete(book.ID); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"Octavia E. Butler", "_athenaeum/" + book.UUID + ".json"} {
		if exists(filepath.Join(lib.Root(), gone)) {
			t.Errorf("%s was not removed", gone)
		}
	}
	tombstones, _ := st.Tombstones()
	for _, want := range []string{"Octavia E. Butler/Kindred - Octavia E. Butler.epub", "_athenaeum/" + book.UUID + ".json"} {
		if !slices.Contains(tombstones, want) {
			t.Errorf("tombstones = %q, want %s among them", tombstones, want)
		}
	}
}

// TestRescan checks that the database follows the files on disk.
func TestRescan(t *testing.T) {
	lib, st := newLibrary(t)
	book := importSample(t, lib)
	if result := rescan(t, lib); result != (library.RescanResult{}) {
		t.Fatalf("rescan of an unchanged library = %+v", result)
	}
	if err := st.DeleteBook(book.ID); err != nil {
		t.Fatal(err)
	}
	if result := rescan(t, lib); result.Added != 1 {
		t.Fatalf("rescan = %+v; want one book added", result)
	}
	restored, err := st.BookByUUID(book.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Title != book.Title || restored.Cover != book.Cover || restored.Path != book.Path || len(restored.Formats) != 1 {
		t.Errorf("restored book = %+v", restored)
	}
	if err := os.Remove(lib.FormatPath(book, book.Formats[0])); err != nil {
		t.Fatal(err)
	}
	if result := rescan(t, lib); result.Removed != 1 {
		t.Errorf("rescan = %+v; want one book removed", result)
	}
}

// TestRescanAdopts checks that ebooks dropped anywhere in the library
// become books filed under their author, that files sharing a name
// become one book and that other files are left where they are.
func TestRescanAdopts(t *testing.T) {
	lib, st := newLibrary(t)
	solaris := booktest.Book{Title: "Solaris", Author: "Stanislaw Lem", Cover: booktest.PNG(color.RGBA{B: 90, A: 255})}
	write(t, lib, "Inbox/To sort/solaris_v2.epub", booktest.EPUB(solaris))
	write(t, lib, "Inbox/To sort/solaris_v2.mobi", booktest.MOBI(solaris))
	write(t, lib, "Inbox/Roadside Picnic - Arkady Strugatsky.txt", []byte("The zone."))
	write(t, lib, "Inbox/notes.doc", []byte("Not a book."))
	write(t, lib, "Inbox/.partial.epub", []byte("Half a download."))
	if result := rescan(t, lib); result.Added != 2 {
		t.Fatalf("rescan = %+v, want two books added", result)
	}
	want := []string{
		"Arkady Strugatsky/Roadside Picnic - Arkady Strugatsky.txt",
		"Stanislaw Lem/Solaris - Stanislaw Lem.epub",
		"Stanislaw Lem/Solaris - Stanislaw Lem.mobi",
	}
	if got := files(t, st); !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %q, want %q", got, want)
	}
	for _, rel := range append(want, "Inbox/notes.doc", "Inbox/.partial.epub") {
		if !exists(filepath.Join(lib.Root(), filepath.FromSlash(rel))) {
			t.Errorf("%s is missing", rel)
		}
	}
	if exists(filepath.Join(lib.Root(), "Inbox", "To sort")) {
		t.Error("the emptied folder was left behind")
	}
	books, _ := st.Books(store.ListOptions{Query: "Solaris"})
	if len(books) != 1 || books[0].Cover == "" || !exists(lib.CoverPath(books[0])) {
		t.Errorf("adopted book = %+v, want it to have its cover", books)
	}
	tombstones, _ := st.Tombstones()
	if !slices.Contains(tombstones, "Inbox/To sort/solaris_v2.epub") {
		t.Errorf("tombstones = %q, want the places the files were in", tombstones)
	}
	if result := rescan(t, lib); result != (library.RescanResult{}) {
		t.Errorf("second rescan = %+v, want no change", result)
	}

	write(t, lib, "solaris.pdf", booktest.TextPDF(solaris))
	write(t, lib, "another copy.epub", booktest.EPUB(solaris))
	if result := rescan(t, lib); result.Added != 1 || result.Updated != 1 {
		t.Errorf("rescan = %+v, want a format added to the book and the copy kept apart", result)
	}
	want = append(want, "Stanislaw Lem/Solaris - Stanislaw Lem (2).epub", "Stanislaw Lem/Solaris - Stanislaw Lem.pdf")
	slices.Sort(want)
	if got := files(t, st); !reflect.DeepEqual(got, want) {
		t.Errorf("files = %q, want %q", got, want)
	}
}

// TestRescanConvertsLegacyFolders checks that a book kept in a folder of
// its own by an earlier version keeps its identity and details when its
// files are moved to the folder of its author.
func TestRescanConvertsLegacyFolders(t *testing.T) {
	lib, st := newLibrary(t)
	book := importSample(t, lib)
	old := "Octavia E. Butler/Kindred (" + book.UUID[:8] + ")"
	details := `{"uuid": "` + book.UUID + `", "title": "Kindred", "authors": ["Octavia E. Butler"], "tags": ["Fiction"],
		"created_at": "2024-05-01T10:00:00Z"}`
	write(t, lib, old+"/metadata.json", []byte(details))
	write(t, lib, old+"/cover.png", sample.Cover)
	for _, moved := range []string{lib.FormatPath(book, book.Formats[0]), lib.CoverPath(book)} {
		if err := os.Remove(moved); err != nil {
			t.Fatal(err)
		}
	}
	write(t, lib, old+"/Kindred - Octavia E. Butler.epub", booktest.EPUB(sample))
	if err := os.Remove(filepath.Join(lib.Root(), "_athenaeum", book.UUID+".json")); err != nil {
		t.Fatal(err)
	}
	rescan(t, lib)
	converted, err := st.Book(book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if converted.UUID != book.UUID || converted.Path != "Octavia E. Butler" || converted.Cover != book.UUID+".png" {
		t.Errorf("converted book = %+v", converted)
	}
	if got := files(t, st); !reflect.DeepEqual(got, []string{"Octavia E. Butler/Kindred - Octavia E. Butler.epub"}) {
		t.Errorf("files = %q", got)
	}
	if exists(filepath.Join(lib.Root(), filepath.FromSlash(old))) || !exists(lib.CoverPath(converted)) {
		t.Error("the old folder was left behind or the cover was lost")
	}
	if result := rescan(t, lib); result != (library.RescanResult{}) {
		t.Errorf("second rescan = %+v, want no change", result)
	}
}
