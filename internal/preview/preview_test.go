package preview_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/preview"
)

// write stores data in a new file called name and returns its path.
func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

// archive returns a ZIP file holding the given files.
func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, content)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestReadOutlineEPUB2 reads the reading order and the NCX table of
// contents of an EPUB 2 file.
func TestReadOutlineEPUB2(t *testing.T) {
	book := booktest.Book{Title: "The Tidewater Almanac", Chapters: []string{"Spring Tides", "Neap Tides"}}
	outline, err := preview.ReadOutline(write(t, "almanac.epub", booktest.EPUB(book)))
	if err != nil {
		t.Fatal(err)
	}
	sections := []preview.Section{
		{Href: "OEBPS/chapter1.xhtml", Title: "Spring Tides"},
		{Href: "OEBPS/chapter2.xhtml", Title: "Neap Tides"},
	}
	if !reflect.DeepEqual(outline.Sections, sections) {
		t.Errorf("sections = %+v, want %+v", outline.Sections, sections)
	}
	contents := []preview.Entry{
		{Title: "Spring Tides", Href: "OEBPS/chapter1.xhtml"},
		{Title: "Neap Tides", Href: "OEBPS/chapter2.xhtml"},
	}
	if !reflect.DeepEqual(outline.Contents, contents) {
		t.Errorf("contents = %+v, want %+v", outline.Contents, contents)
	}
}

// epub3 holds the files of an EPUB 3 book whose table of contents is a
// nested navigation document and whose file names need escaping.
var epub3 = map[string]string{
	"META-INF/container.xml": `<container><rootfiles><rootfile full-path="book/package.opf"/></rootfiles></container>`,
	"book/package.opf": `<package version="3.0">
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="one" href="text/part%20one.xhtml" media-type="application/xhtml+xml"/>
    <item id="two" href="text/part two.xhtml" media-type="application/xhtml+xml"/>
    <item id="gone" href="../../outside.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine><itemref idref="one"/><itemref idref="two"/><itemref idref="gone"/><itemref idref="unknown"/></spine>
</package>`,
	"book/nav.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body>
  <nav epub:type="landmarks"><ol><li><a href="text/part%20one.xhtml">Start</a></li></ol></nav>
  <nav epub:type="toc"><h1>Contents</h1><ol>
    <li><a href="text/part%20one.xhtml">Part
      <em>One</em></a><ol>
      <li><a href="text/part%20one.xhtml#ebb">The Ebb</a></li>
    </ol></li>
    <li><a href="text/part%20two.xhtml">Part Two</a></li>
  </ol></nav>
  <nav epub:type="page-list"><ol><li><a href="text/part%20two.xhtml">9</a></li></ol></nav>
</body></html>`,
	"book/text/part one.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>One.</p></body></html>`,
	"book/text/part two.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Two.</p></body></html>`,
	"book/style/page.css":      `p { margin: 0 }`,
}

// TestReadOutlineEPUB3 reads the navigation document of an EPUB 3 file
// and leaves out spine entries that point nowhere.
func TestReadOutlineEPUB3(t *testing.T) {
	outline, err := preview.ReadOutline(write(t, "book.epub", archive(t, epub3)))
	if err != nil {
		t.Fatal(err)
	}
	sections := []preview.Section{
		{Href: "book/text/part%20one.xhtml", Title: "Part One"},
		{Href: "book/text/part%20two.xhtml", Title: "Part Two"},
	}
	if !reflect.DeepEqual(outline.Sections, sections) {
		t.Errorf("sections = %+v, want %+v", outline.Sections, sections)
	}
	contents := []preview.Entry{
		{Title: "Part One", Href: "book/text/part%20one.xhtml"},
		{Title: "The Ebb", Href: "book/text/part%20one.xhtml#ebb", Level: 1},
		{Title: "Part Two", Href: "book/text/part%20two.xhtml"},
	}
	if !reflect.DeepEqual(outline.Contents, contents) {
		t.Errorf("contents = %+v, want %+v", outline.Contents, contents)
	}
}

// TestReadOutlineRejectsOtherFiles fails for files that are not EPUBs.
func TestReadOutlineRejectsOtherFiles(t *testing.T) {
	if _, err := preview.ReadOutline(write(t, "book.epub", booktest.MOBI(booktest.Book{Title: "Solaris"}))); err == nil {
		t.Error("ReadOutline accepted a MOBI file")
	}
	empty := map[string]string{
		"META-INF/container.xml": epub3["META-INF/container.xml"],
		"book/package.opf":       `<package><manifest/><spine/></package>`,
	}
	if _, err := preview.ReadOutline(write(t, "empty.epub", archive(t, empty))); err == nil {
		t.Error("ReadOutline accepted an EPUB with nothing to read")
	}
}

// TestOpenFile reads files out of an EPUB with their media types.
func TestOpenFile(t *testing.T) {
	epub := write(t, "book.epub", archive(t, epub3))
	types := map[string]string{
		"book/text/part one.xhtml": "application/xhtml+xml",
		"book/style/page.css":      "text/css; charset=utf-8",
		"book/package.opf":         "application/oebps-package+xml",
	}
	for name, want := range types {
		file, err := preview.OpenFile(epub, name)
		if err != nil {
			t.Fatalf("OpenFile(%q): %v", name, err)
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil || string(data) != epub3[name] || file.Size != int64(len(data)) || file.MediaType != want {
			t.Errorf("OpenFile(%q) = %q (%d bytes, %s), %v", name, data, file.Size, file.MediaType, err)
		}
	}
	for _, name := range []string{"book/missing.xhtml", "book/text", "../book.epub", "/etc/passwd", ""} {
		if _, err := preview.OpenFile(epub, name); !errors.Is(err, preview.ErrNotFound) {
			t.Errorf("OpenFile(%q) error = %v, want ErrNotFound", name, err)
		}
	}
}

// TestCache names renditions after the file they come from and removes
// them by book and format.
func TestCache(t *testing.T) {
	cache := &preview.Cache{Dir: filepath.Join(t.TempDir(), "cache")}
	if err := cache.Drop("b1", ""); err != nil {
		t.Fatalf("Drop on a missing folder: %v", err)
	}
	src := write(t, "Solaris.MOBI", []byte("one"))
	first, err := cache.Path("b1", src)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := cache.Path("b1", src); again != first {
		t.Errorf("Path changed from %q to %q for an unchanged file", first, again)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatal(err)
	}
	if changed, _ := cache.Path("b1", src); changed == first {
		t.Error("Path did not change with the file")
	}

	tmp, err := cache.Temp()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, first); err != nil {
		t.Fatal(err)
	}
	other, _ := cache.Path("b1", write(t, "Solaris.azw3", []byte("two")))
	foreign, _ := cache.Path("b10", src)
	for _, file := range []string{other, foreign} {
		if err := os.WriteFile(file, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(file string) bool {
		_, err := os.Stat(file)
		return err == nil
	}
	if err := cache.Drop("b1", "mobi"); err != nil {
		t.Fatal(err)
	}
	if exists(first) || !exists(other) || !exists(foreign) {
		t.Errorf("after dropping mobi: mobi %v, azw3 %v, other book %v", exists(first), exists(other), exists(foreign))
	}
	if err := cache.Drop("b1", ""); err != nil {
		t.Fatal(err)
	}
	if exists(other) || !exists(foreign) {
		t.Errorf("after dropping the book: azw3 %v, other book %v", exists(other), exists(foreign))
	}
}
