package meta_test

import (
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/meta"
)

// sample is the book used to build test files.
var sample = booktest.Book{
	Title:       "The Left Hand of Darkness",
	Author:      "Ursula K. Le Guin",
	Publisher:   "Ace Books",
	Language:    "en",
	ISBN:        "9780441478125",
	Description: "<p>A lone envoy visits the planet Winter.</p>",
	Series:      "Hainish Cycle",
	Tags:        []string{"Science Fiction", "Classics"},
	Cover:       booktest.PNG(color.RGBA{R: 30, G: 60, B: 90, A: 255}),
}

// write stores data in a temporary file called name and returns its path.
func write(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReadEPUB checks that every supported EPUB field is extracted.
func TestReadEPUB(t *testing.T) {
	path := write(t, "book.epub", booktest.EPUB(sample))
	got, cover := meta.Read(path, "book.epub")
	want := meta.Metadata{
		Title:       sample.Title,
		Authors:     []string{sample.Author},
		Series:      sample.Series,
		SeriesIndex: 2,
		Publisher:   sample.Publisher,
		Language:    sample.Language,
		ISBN:        sample.ISBN,
		Description: "A lone envoy visits the planet Winter.",
		Tags:        sample.Tags,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("metadata mismatch\n got: %+v\nwant: %+v", got, want)
	}
	if cover == nil || cover.Ext != "png" || len(cover.Data) != len(sample.Cover) {
		t.Errorf("cover = %+v, want the embedded PNG", cover)
	}
}

// TestReadMOBI checks that MOBI header and EXTH fields are extracted.
func TestReadMOBI(t *testing.T) {
	path := write(t, "book.mobi", booktest.MOBI(sample))
	got, cover := meta.Read(path, "book.mobi")
	if got.Title != sample.Title {
		t.Errorf("title = %q, want %q", got.Title, sample.Title)
	}
	if !reflect.DeepEqual(got.Authors, []string{sample.Author}) {
		t.Errorf("authors = %q", got.Authors)
	}
	if got.Publisher != sample.Publisher || got.ISBN != sample.ISBN || got.Language != sample.Language {
		t.Errorf("publisher, isbn, language = %q, %q, %q", got.Publisher, got.ISBN, got.Language)
	}
	if !reflect.DeepEqual(got.Tags, sample.Tags) {
		t.Errorf("tags = %q, want %q", got.Tags, sample.Tags)
	}
	if cover == nil || cover.Ext != "png" {
		t.Errorf("cover = %+v, want the embedded PNG", cover)
	}
	if header := meta.ReadHeader(path, "book.mobi"); header.Title != sample.Title {
		t.Errorf("header title = %q, want %q", header.Title, sample.Title)
	}
}

// TestReadPDF checks that the PDF information dictionary is read.
func TestReadPDF(t *testing.T) {
	book := sample
	book.Title = "Notes (Second Edition)"
	path := write(t, "book.pdf", booktest.PDF(book))
	got, cover := meta.Read(path, "book.pdf")
	if got.Title != book.Title {
		t.Errorf("title = %q, want %q", got.Title, book.Title)
	}
	if !reflect.DeepEqual(got.Authors, []string{sample.Author}) {
		t.Errorf("authors = %q", got.Authors)
	}
	if !reflect.DeepEqual(got.Tags, sample.Tags) {
		t.Errorf("tags = %q, want %q", got.Tags, sample.Tags)
	}
	if cover != nil {
		t.Errorf("cover = %+v, want none", cover)
	}
}

// TestReadFallsBackToFileName checks that unreadable files get their
// title and author from the file name.
func TestReadFallsBackToFileName(t *testing.T) {
	path := write(t, "broken.epub", []byte("not a zip archive"))
	got, cover := meta.Read(path, "Dune - Frank Herbert.epub")
	if got.Title != "Dune" || !reflect.DeepEqual(got.Authors, []string{"Frank Herbert"}) {
		t.Errorf("got %q by %q", got.Title, got.Authors)
	}
	if cover != nil {
		t.Errorf("cover = %+v, want none", cover)
	}
	if got, _ := meta.Read(path, "notes.txt"); got.Title != "notes" || len(got.Authors) != 0 {
		t.Errorf("got %q by %q", got.Title, got.Authors)
	}
}

// TestFormatOf checks format detection from file names.
func TestFormatOf(t *testing.T) {
	cases := map[string]bool{"a.EPUB": true, "b.azw3": true, "c.pdf": true, "d.exe": false, "noext": false}
	for name, want := range cases {
		if _, ok := meta.FormatOf(name); ok != want {
			t.Errorf("FormatOf(%q) accepted = %v, want %v", name, ok, want)
		}
	}
}

// TestDates checks which dates are accepted, how they are written and
// what is made of the dates files carry.
func TestDates(t *testing.T) {
	for in, want := range map[string]string{
		"":                                 "",
		" 2019 ":                           "2019",
		"2019-3":                           "2019-03",
		"2019-03-12":                       "2019-03-12",
		"2019/03/12":                       "2019-03-12",
		"August 2016":                      "2016-08",
		"12 March 2019":                    "2019-03-12",
		"Mar 12, 2019":                     "2019-03-12",
		"2020-03-16T15:47:47.202811+00:00": "2020-03-16",
		"2016-10-18 09:40:19.917000-05:00": "2016-10-18",
	} {
		if got, ok := meta.PartialDate(in); !ok || got != want {
			t.Errorf("PartialDate(%q) = %q, %t; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"19", "2019-13", "2019-02-30", "12/03/2019", "soon", "2019-03-12x"} {
		if got, ok := meta.PartialDate(in); ok {
			t.Errorf("PartialDate(%q) = %q, want it refused", in, got)
		}
	}
	for in, want := range map[string]string{
		"2019-03":                 "2019-03",
		"Copyright 1998, Addison": "1998",
		"12/03/2019":              "2019",
		"first printing":          "",
	} {
		if got := meta.Date(in); got != want {
			t.Errorf("Date(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestReadDOCX checks that the properties and the preview picture of a
// Word document are read.
func TestReadDOCX(t *testing.T) {
	book := sample
	book.Description = "A lone envoy visits the planet Winter."
	book.Cover = booktest.JPEG(color.RGBA{R: 30, G: 60, B: 90, A: 255})
	path := write(t, "book.docx", booktest.DOCX(book))
	got, cover := meta.Read(path, "unhelpful name.docx")
	want := meta.Metadata{
		Title:       sample.Title,
		Authors:     []string{sample.Author},
		Language:    sample.Language,
		Description: book.Description,
		Tags:        sample.Tags,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("metadata mismatch\n got: %+v\nwant: %+v", got, want)
	}
	if cover == nil || cover.Ext != "jpg" || len(cover.Data) != len(book.Cover) {
		t.Errorf("cover = %+v, want the preview picture", cover)
	}
	bare := write(t, "bare.docx", booktest.DOCX(booktest.Book{}))
	got, cover = meta.Read(bare, "Dune - Frank Herbert.docx")
	if got.Title != "Dune" || !reflect.DeepEqual(got.Authors, []string{"Frank Herbert"}) || cover != nil {
		t.Errorf("a document without properties gave %q by %q with cover %v", got.Title, got.Authors, cover)
	}
}

// TestReadPages checks that a Pages document gets its name from its
// file and its cover from the picture of its first page, and that
// another file with its extension gets neither.
func TestReadPages(t *testing.T) {
	preview := booktest.JPEG(color.RGBA{R: 200, G: 100, A: 255})
	path := write(t, "sketches.pages", booktest.Pages(booktest.Book{Title: "Inside", Cover: preview}))
	got, cover := meta.Read(path, "Shore Sketches - Edda Voss.pages")
	if got.Title != "Shore Sketches" || !reflect.DeepEqual(got.Authors, []string{"Edda Voss"}) {
		t.Errorf("got %q by %q, want the name of the file", got.Title, got.Authors)
	}
	if cover == nil || cover.Ext != "jpg" || len(cover.Data) != len(preview) {
		t.Errorf("cover = %+v, want the picture of the first page", cover)
	}
	if format, ok := meta.FormatOf("Shore Sketches.PAGES"); format != "pages" || !ok {
		t.Errorf("FormatOf = %q, %v", format, ok)
	}
	other := write(t, "other.pages", booktest.EPUB(sample))
	if _, cover := meta.Read(other, "other.pages"); cover != nil {
		t.Errorf("a file that is not a Pages document gave the cover %+v", cover)
	}
}
