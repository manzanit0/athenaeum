package kf8

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/meta"
)

// TestPalmDOCRoundTrip checks that compressed records expand to what
// they were made from, and that repetitive text gets smaller.
func TestPalmDOCRoundTrip(t *testing.T) {
	binary := make([]byte, 600)
	for i := range binary {
		binary[i] = byte(i * 7)
	}
	inputs := map[string][]byte{
		"empty":      nil,
		"short":      []byte("ab"),
		"markup":     []byte(strings.Repeat(`<p class="calibre1">It was a quiet morning.</p>`+"\n", 80)),
		"multibyte":  []byte(strings.Repeat("naïve café 日本語 — ", 150)),
		"binary":     binary,
		"run":        bytes.Repeat([]byte{'a'}, 4096),
		"highbytes":  bytes.Repeat([]byte{0xFF, 0x01, 0x80}, 300),
		"spaceupper": []byte(strings.Repeat("The Quick Brown Fox @ Home ", 40)),
	}
	for name, input := range inputs {
		packed := compress(input)
		got, err := decompress(packed)
		if err != nil || !bytes.Equal(got, input) {
			t.Errorf("%s: round trip failed: %v (%d bytes in, %d out)", name, err, len(input), len(got))
		}
		if name == "markup" && len(packed) > len(input)/3 {
			t.Errorf("markup compressed to %d of %d bytes, want under a third", len(packed), len(input))
		}
	}
	if _, err := decompress([]byte{0x80}); !errors.Is(err, errCorrupt) {
		t.Errorf("decompress of a cut code = %v, want errCorrupt", err)
	}
}

// TestIntegerEncodings checks the two variable width integer forms
// against the worked example in calibre's documentation of the format.
func TestIntegerEncodings(t *testing.T) {
	if got := varint(0x11111); !bytes.Equal(got, []byte{0x04, 0x22, 0x91}) {
		t.Errorf("varint(0x11111) = % x, want 04 22 91", got)
	}
	if got := varint(0); !bytes.Equal(got, []byte{0x80}) {
		t.Errorf("varint(0) = % x, want 80", got)
	}
	if got := trailer([]byte{1, 2, 3}); !bytes.Equal(got, []byte{1, 2, 3, 0x84}) {
		t.Errorf("trailer = % x, want 01 02 03 84", got)
	}
	long := trailer(make([]byte, 200))
	if n := len(long); n != 202 || long[n-2] != 0x81 || long[n-1] != 0x4A {
		t.Errorf("trailer of 200 bytes ends % x (length %d), want 81 4a (202)", long[len(long)-2:], n)
	}
	if got := Base32(1023, 4); got != "00VV" {
		t.Errorf("Base32(1023, 4) = %q, want 00VV", got)
	}
}

// TestIndexingMatchesCalibre checks the indexing entries of text records
// against the bytes calibre 8.5 wrote for a book with the same table of
// contents: three top level lines, the second with two nested ones.
func TestIndexingMatchesCalibre(t *testing.T) {
	rows := []tocRow{
		{offset: 377, length: 917, parent: -1},
		{offset: 1294, length: 65080, parent: -1, firstChild: 3, lastChild: 4},
		{offset: 66374, length: 1078, parent: -1},
		{offset: 17386, length: 32400, depth: 1, parent: 1},
		{offset: 49786, length: 16588, depth: 1, parent: 1},
	}
	got := indexing(rows, 68364)
	if len(got) != 17 {
		t.Fatalf("indexing returned %d entries, want 17", len(got))
	}
	want := map[int][]byte{
		0:  {0x86, 0x88, 0x02},
		1:  {0x8b, 0x88, 0x80},
		4:  {0x8b, 0x88, 0x80, 0xa0},
		5:  {0x8a, 0x88, 0xa1, 0x80},
		16: {0x8a, 0x88, 0xb0, 0xa8},
	}
	for record, data := range want {
		if !bytes.Equal(got[record], trailer(data)) {
			t.Errorf("record %d: indexing = % x, want % x", record, got[record], trailer(data))
		}
	}
}

// sampleBook returns a small book with two files, a stylesheet, a nested
// table of contents and a cover.
func sampleBook() *Book {
	body := []byte(strings.Repeat("<p>Café 日本語 and more text to fill the record.</p>", 150))
	skeleton := []byte(`<html><head><title>x</title></head><body aid="0000"></body></html>`)
	at := bytes.Index(skeleton, []byte("</body>"))
	file := File{Skeleton: skeleton, Chunks: []Chunk{
		{Raw: body[:4000], InsertAt: at, Selector: "P-//*[@aid='0000']"},
		{Raw: body[4000:], InsertAt: at + 4000, Selector: "P-//*[@aid='0000']"},
	}}
	cover := booktest.PNG(color.RGBA{R: 200, A: 255})
	return &Book{
		Title: "The Tidewater Almanac", Authors: []string{"Ines Marlow"}, Publisher: "Saltmarsh Press",
		Language: "en-GB", ID: "5d4a7f0c", Created: time.Unix(1700000000, 0),
		Files: []File{file, file},
		Flows: [][]byte{[]byte("p { margin: 0 }")},
		Contents: []Entry{
			{Title: "One", Target: Position{File: 0, Offset: at}},
			{Title: "Inside one", Level: 1, Target: Position{File: 0, Offset: at + 5000}},
			{Title: "Two", Target: Position{File: 1, Offset: at}},
		},
		Resources: [][]byte{cover},
		Cover:     0,
		Thumbnail: -1,
	}
}

// records splits a Palm database into its records.
func records(t *testing.T, data []byte) [][]byte {
	t.Helper()
	if string(data[60:68]) != "BOOKMOBI" {
		t.Fatalf("database type = %q, want BOOKMOBI", data[60:68])
	}
	count := int(binary.BigEndian.Uint16(data[76:]))
	out := make([][]byte, count)
	for i := range count {
		start := binary.BigEndian.Uint32(data[78+8*i:])
		end := uint32(len(data))
		if i+1 < count {
			end = binary.BigEndian.Uint32(data[78+8*(i+1):])
		}
		out[i] = data[start:end]
	}
	return out
}

// text expands the text records of a book, dropping their trailing
// entries: the indexing entry and the character overlap.
func text(t *testing.T, recs [][]byte) []byte {
	t.Helper()
	var out []byte
	for i := 1; i <= int(binary.BigEndian.Uint16(recs[0][8:])); i++ {
		rec := recs[i]
		size := int(rec[len(rec)-1] & 0x7F)
		rec = rec[:len(rec)-size]
		rec = rec[:len(rec)-int(rec[len(rec)-1]&3)-1]
		plain, err := decompress(rec)
		if err != nil {
			t.Fatalf("text record %d: %v", i, err)
		}
		out = append(out, plain...)
	}
	return out
}

// TestWrite checks the database a book is written as: its text survives,
// the header points at the right records and the metadata is readable.
func TestWrite(t *testing.T) {
	book := sampleBook()
	var buf bytes.Buffer
	if err := book.Write(&buf); err != nil {
		t.Fatal(err)
	}
	recs := records(t, buf.Bytes())
	want := bytes.Join(book.flows(), nil)
	if got := text(t, recs); !bytes.Equal(got, want) {
		t.Errorf("text is %d bytes, want %d; equal prefix %d", len(got), len(want), commonPrefix(got, want))
	}
	head := recs[0]
	field := func(offset int) int { return int(binary.BigEndian.Uint32(head[offset:])) }
	if field(4) != len(want) || string(head[16:20]) != "MOBI" || field(20) != 264 || field(36) != 8 {
		t.Errorf("header: text length %d, magic %q, length %d, version %d", field(4), head[16:20], field(20), field(36))
	}
	if field(240) != 3 || field(92) != 9 {
		t.Errorf("header: extra data flags %d, locale %d; want 3 and 9", field(240), field(92))
	}
	for name, offset := range map[string]int{"contents": 244, "chunks": 248, "skeletons": 252} {
		if rec := recs[field(offset)]; string(rec[:4]) != "INDX" {
			t.Errorf("%s index points at a record starting %q", name, rec[:4])
		}
	}
	checks := map[string]struct {
		// offset is where the header holds the record number.
		offset int
		// magic is how the record starts.
		magic string
	}{"images": {108, "\x89PNG"}, "flows": {192, "FDST"}, "fcis": {200, "FCIS"}, "flis": {208, "FLIS"}}
	for name, check := range checks {
		if rec := recs[field(check.offset)]; !bytes.HasPrefix(rec, []byte(check.magic)) {
			t.Errorf("%s points at a record starting %q", name, rec[:4])
		}
	}
	if string(head[field(84):field(84)+field(88)]) != book.Title {
		t.Errorf("full title = %q", head[field(84):field(84)+field(88)])
	}
	if !bytes.Equal(recs[len(recs)-1], eofRecord) {
		t.Errorf("last record = % x", recs[len(recs)-1])
	}

	file := filepath.Join(t.TempDir(), "book.azw3")
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	m, cover := meta.Read(file, "book.azw3")
	if m.Title != book.Title || len(m.Authors) != 1 || m.Authors[0] != "Ines Marlow" || m.Publisher != "Saltmarsh Press" {
		t.Errorf("metadata read back = %+v", m)
	}
	if cover == nil || !bytes.Equal(cover.Data, book.Resources[0]) {
		t.Errorf("cover read back = %v", cover)
	}
}

// commonPrefix returns the length of the shared start of a and b.
func commonPrefix(a, b []byte) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// TestTableOfContents checks how lines are ordered, linked and measured.
func TestTableOfContents(t *testing.T) {
	book := sampleBook()
	html := len(book.flows()[0])
	rows := book.tocRows(html)
	titles := []string{rows[0].title, rows[1].title, rows[2].title}
	if strings.Join(titles, ",") != "One,Two,Inside one" {
		t.Fatalf("rows are ordered %v, want depth first then position", titles)
	}
	if rows[0].firstChild != 2 || rows[0].lastChild != 2 || rows[2].parent != 0 || rows[1].parent != -1 {
		t.Errorf("links: %+v", rows)
	}
	if rows[0].offset+rows[0].length != rows[1].offset || rows[1].offset+rows[1].length != html {
		t.Errorf("lengths do not tile the text: %+v", rows)
	}
	if rows[2].chunk != 1 || rows[2].within != 1000 {
		t.Errorf("nested line leads to chunk %d offset %d, want 1 and 1000", rows[2].chunk, rows[2].within)
	}
	if got := book.Link(Position{File: 1, Offset: 10}); got != "kindle:pos:fid:0002:off:0000000000" {
		t.Errorf("link into a skeleton = %q, want the start of the next chunk", got)
	}
	if got := len(LinkPlaceholder(42)); got != len(book.Link(Position{})) {
		t.Errorf("placeholder is %d characters, a link %d", got, len(book.Link(Position{})))
	}
}

// TestValidate checks that books without content are refused.
func TestValidate(t *testing.T) {
	var buf bytes.Buffer
	if err := (&Book{}).Write(&buf); !errors.Is(err, ErrEmpty) {
		t.Errorf("Write of an empty book = %v, want ErrEmpty", err)
	}
	book := sampleBook()
	book.Files[1].Chunks = nil
	if err := book.Write(&buf); !errors.Is(err, ErrEmpty) {
		t.Errorf("Write of a book with an empty file = %v, want ErrEmpty", err)
	}
	book = sampleBook()
	book.Contents[0].Target.File = 9
	if err := book.Write(&buf); err == nil {
		t.Error("Write accepted a table of contents pointing outside the book")
	}
}

// TestLargeIndex checks that an index too big for one record is spread
// over several, each within the size its offsets can address.
func TestLargeIndex(t *testing.T) {
	ix := &index{tags: []tagDef{{1, 1, 1}, endTag}}
	for i := range 9000 {
		ix.entries = append(ix.entries, indexEntry{key: strings.Repeat("k", 20), values: [][]int{{i}}})
	}
	recs := ix.records()
	if len(recs) < 4 {
		t.Fatalf("index has %d records, want it split", len(recs))
	}
	total := 0
	for _, rec := range recs[1:] {
		if len(rec) >= 0x10000 || string(rec[:4]) != "INDX" {
			t.Errorf("data record of %d bytes starting %q", len(rec), rec[:4])
		}
		total += int(binary.BigEndian.Uint32(rec[24:]))
	}
	if total != 9000 || int(binary.BigEndian.Uint32(recs[0][24:])) != len(recs)-1 {
		t.Errorf("records hold %d entries in %d data records", total, binary.BigEndian.Uint32(recs[0][24:]))
	}
}

// TestReadBack checks that a written book reads back as it was: the
// same documents, stylesheets, images and table of contents.
func TestReadBack(t *testing.T) {
	book := sampleBook()
	file := filepath.Join(t.TempDir(), "book.azw3")
	out, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := book.Write(out); err != nil {
		t.Fatal(err)
	}
	out.Close()
	read, err := Read(file)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	if len(read.Files) != 2 || len(read.Flows) != 1 || !bytes.Equal(read.Flows[0], book.Flows[0]) {
		t.Fatalf("read %d files and %d flows", len(read.Files), len(read.Flows))
	}
	for i, f := range book.Files {
		want := bytes.Clone(f.Skeleton)
		for _, c := range f.Chunks {
			want = append(want[:c.InsertAt:c.InsertAt], append(bytes.Clone(c.Raw), want[c.InsertAt:]...)...)
		}
		if !bytes.Equal(read.Files[i], want) {
			t.Errorf("file %d differs: %d bytes read, %d written", i, len(read.Files[i]), len(want))
		}
	}
	if len(read.Contents) != 3 {
		t.Fatalf("read %d contents lines, want 3", len(read.Contents))
	}
	for i, want := range book.Contents {
		if got := read.Contents[i]; got != want {
			t.Errorf("contents line %d = %+v, want %+v", i, got, want)
		}
	}
	if read.Cover != 0 || read.Resources() < 1 {
		t.Fatalf("cover is resource %d of %d", read.Cover, read.Resources())
	}
	if cover, err := read.Resource(read.Cover); err != nil || !bytes.Equal(cover, book.Resources[0]) {
		t.Errorf("cover read back differs: %v", err)
	}
	if at, ok := read.Position(3, 7); !ok || at.File != 1 || at.Offset != book.Files[1].Chunks[1].InsertAt+7 {
		t.Errorf("Position(3, 7) = %+v, %v", at, ok)
	}
	if n, ok := ParseAddress("00VV?mime=image/png"); !ok || n != 1023 {
		t.Errorf("ParseAddress = %d, %v", n, ok)
	}
}

// TestReadRefusals checks the files that are not readable KF8 books.
func TestReadRefusals(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var buf bytes.Buffer
	if err := sampleBook().Write(&buf); err != nil {
		t.Fatal(err)
	}
	good := buf.Bytes()
	header := int(binary.BigEndian.Uint32(good[78:]))
	protected := bytes.Clone(good)
	protected[header+13] = 2
	huffman := bytes.Clone(good)
	binary.BigEndian.PutUint16(huffman[header:], 17480)
	cases := map[string]struct {
		// data is the content of the file.
		data []byte
		// want is the error reading it gives.
		want error
	}{
		"old.mobi":       {booktest.MOBI(booktest.Book{Title: "Solaris"}), ErrNotKF8},
		"book.epub":      {booktest.EPUB(booktest.Book{Title: "Solaris"}), ErrNotKF8},
		"empty.azw3":     {nil, ErrNotKF8},
		"protected.azw3": {protected, ErrProtected},
		"huffman.azw3":   {huffman, ErrCompression},
		"cut.azw3":       {good[:len(good)/2], errCorrupt},
	}
	for name, c := range cases {
		if _, err := Read(write(name, c.data)); !errors.Is(err, c.want) {
			t.Errorf("Read(%s) = %v, want %v", name, err, c.want)
		}
	}
}
