// Package kf8 writes books in KF8, the format inside Amazon's AZW3 files.
//
// A KF8 book is a Palm database. Its text is a run of HTML files, each
// stored as a skeleton (the document without its body content) followed
// by the chunks of content that belong inside it, then the stylesheets.
// Index records say where every skeleton and chunk goes and list the
// table of contents; further records hold the images.
//
// The layout follows what calibre writes and what the MobileRead wiki
// documents: https://wiki.mobileread.com/wiki/MOBI and
// https://wiki.mobileread.com/wiki/KF8.
package kf8

import (
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Link placeholders.
const (
	// LinkPrefix starts a link to a position in the book.
	LinkPrefix = "kindle:pos:fid:"
	// linkSeparator sits between the chunk number and the offset.
	linkSeparator = ":off:"
	// chunkDigits is the width of the chunk number in a link.
	chunkDigits = 4
	// offsetDigits is the width of the offset in a link.
	offsetDigits = 10
)

// ErrEmpty is returned for a book without any content.
var ErrEmpty = errors.New("kf8: the book has no content")

// Book is everything needed to write a KF8 file.
type Book struct {
	// Title is the title of the book.
	Title string
	// Authors lists the authors.
	Authors []string
	// Publisher is the name of the publisher.
	Publisher string
	// Description is a summary of the book.
	Description string
	// ISBN is the International Standard Book Number.
	ISBN string
	// Subjects lists the subjects of the book.
	Subjects []string
	// Published is the publication date.
	Published string
	// Language is the language code, such as "en".
	Language string
	// ID identifies the book; the Kindle keys its records on it.
	ID string
	// Created is the creation time written into the file.
	Created time.Time
	// Files are the HTML documents in reading order.
	Files []File
	// Flows are the stylesheets, referenced as kindle:flow:0001 onwards.
	Flows [][]byte
	// Contents is the table of contents.
	Contents []Entry
	// Resources are the images, referenced as kindle:embed:0001 onwards.
	Resources [][]byte
	// Cover is the index of the cover in Resources, or -1.
	Cover int
	// Thumbnail is the index of the cover thumbnail in Resources, or -1.
	Thumbnail int
}

// File is one HTML document of a book.
type File struct {
	// Skeleton is the document without the content held by the chunks.
	Skeleton []byte
	// Chunks are the pieces of content, in document order.
	Chunks []Chunk
}

// Chunk is a piece of the content of a file.
type Chunk struct {
	// Raw is the markup of the chunk.
	Raw []byte
	// InsertAt is the offset of the chunk in the complete document, that
	// is the skeleton with every earlier chunk already in place.
	InsertAt int
	// Selector tells where the chunk goes relative to a skeleton element.
	Selector string
}

// Position is a place in the complete form of a file.
type Position struct {
	// File is the index of the file.
	File int
	// Offset is the byte offset within the complete document.
	Offset int
}

// Entry is one line of the table of contents.
type Entry struct {
	// Title is the text of the line.
	Title string
	// Level is the nesting depth, starting at zero.
	Level int
	// Target is where the line leads.
	Target Position
}

// size returns the length of the complete form of f.
func (f *File) size() int {
	n := len(f.Skeleton)
	for _, c := range f.Chunks {
		n += len(c.Raw)
	}
	return n
}

// Base32 formats v in base 32 with at least width digits, the notation
// of kindle: addresses.
func Base32(v, width int) string {
	s := strings.ToUpper(strconv.FormatInt(int64(v), 32))
	if len(s) < width {
		s = strings.Repeat("0", width-len(s)) + s
	}
	return s
}

// LinkPlaceholder returns a link address of the final width that carries
// the number n until the real position is known.
func LinkPlaceholder(n int) string {
	return LinkPrefix + Base32(0, chunkDigits) + linkSeparator + fmt.Sprintf("%0*d", offsetDigits, n)
}

// Locate returns the number of the chunk holding p and the offset of p
// within it. A place in the skeleton belongs to the chunk that follows.
func (b *Book) Locate(p Position) (chunk, offset int) {
	for _, f := range b.Files[:p.File] {
		chunk += len(f.Chunks)
	}
	chunks := b.Files[p.File].Chunks
	for i, c := range chunks {
		if p.Offset < c.InsertAt {
			return chunk + i, 0
		}
		if p.Offset < c.InsertAt+len(c.Raw) {
			return chunk + i, p.Offset - c.InsertAt
		}
	}
	last := len(chunks) - 1
	return chunk + last, len(chunks[last].Raw)
}

// Link returns the address of the position p.
func (b *Book) Link(p Position) string {
	chunk, offset := b.Locate(p)
	return LinkPrefix + Base32(chunk, chunkDigits) + linkSeparator + Base32(offset, offsetDigits)
}

// validate reports whether the book can be written.
func (b *Book) validate() error {
	if len(b.Files) == 0 {
		return ErrEmpty
	}
	for i, f := range b.Files {
		if len(f.Chunks) == 0 {
			return fmt.Errorf("%w: file %d has no chunks", ErrEmpty, i)
		}
	}
	for _, e := range b.Contents {
		if e.Target.File < 0 || e.Target.File >= len(b.Files) {
			return fmt.Errorf("kf8: contents entry %q points outside the book", e.Title)
		}
	}
	return nil
}

// starts returns the offset of every file in the complete text.
func (b *Book) starts() []int {
	out := make([]int, len(b.Files))
	offset := 0
	for i := range b.Files {
		out[i] = offset
		offset += b.Files[i].size()
	}
	return out
}

// flows returns the text of the book: the files, each as its skeleton
// followed by its chunks, then the stylesheets.
func (b *Book) flows() [][]byte {
	var html []byte
	for _, f := range b.Files {
		html = append(html, f.Skeleton...)
		for _, c := range f.Chunks {
			html = append(html, c.Raw...)
		}
	}
	return append([][]byte{html}, b.Flows...)
}

// skeletonIndex returns the index locating the skeleton of every file.
func (b *Book) skeletonIndex() *index {
	ix := &index{tags: []tagDef{{1, 1, 3}, {6, 2, 12}, endTag}}
	for i, start := range b.starts() {
		f := &b.Files[i]
		ix.entries = append(ix.entries, indexEntry{
			key: fmt.Sprintf("SKEL%010d", i),
			values: [][]int{
				{len(f.Chunks), len(f.Chunks)},
				{start, len(f.Skeleton), start, len(f.Skeleton)},
			},
		})
	}
	return ix
}

// chunkIndex returns the index locating every chunk, with the strings
// holding their selectors.
func (b *Book) chunkIndex() (*index, [][]byte) {
	ix := &index{tags: []tagDef{{2, 1, 1}, {3, 1, 2}, {4, 1, 4}, {6, 2, 8}, endTag}}
	var selectors stringTable
	sequence := 0
	for i, start := range b.starts() {
		within := 0
		for _, c := range b.Files[i].Chunks {
			ix.entries = append(ix.entries, indexEntry{
				key: fmt.Sprintf("%010d", start+c.InsertAt),
				values: [][]int{
					{selectors.add(c.Selector)}, {i}, {sequence}, {within, len(c.Raw)},
				},
			})
			within += len(c.Raw)
			sequence++
		}
	}
	records := selectors.finish()
	ix.strings = len(records)
	return ix, records
}

// tocRow is a line of the table of contents resolved against the text.
type tocRow struct {
	// title is the text of the line.
	title string
	// depth is the nesting depth, starting at zero.
	depth int
	// offset is where the line leads in the complete text.
	offset int
	// length is how much text the line covers.
	length int
	// parent is the position of the enclosing line, or -1.
	parent int
	// firstChild is the position of the first nested line, or -1.
	firstChild int
	// lastChild is the position of the last nested line, or -1.
	lastChild int
	// chunk is the number of the chunk the line leads to.
	chunk int
	// within is the offset of the target inside that chunk.
	within int
	// order is the position of the line in the table as given.
	order int
}

// sortStable sorts items with a stable insertion-friendly comparison.
func sortStable[T any](items []T, less func(a, b T) bool) {
	slices.SortStableFunc(items, func(a, b T) int {
		switch {
		case less(a, b):
			return -1
		case less(b, a):
			return 1
		}
		return 0
	})
}

// tocRows resolves the table of contents into rows sorted by depth and
// then by position, the order the Kindle expects, linked to each other
// by their positions in that order.
func (b *Book) tocRows(htmlLength int) []tocRow {
	starts := b.starts()
	rows := make([]tocRow, len(b.Contents))
	parents := make([]int, len(b.Contents))
	var open []int
	for i, e := range b.Contents {
		depth := min(max(e.Level, 0), len(open))
		open = open[:depth]
		chunk, within := b.Locate(e.Target)
		rows[i] = tocRow{
			title: e.Title, depth: depth, offset: starts[e.Target.File] + e.Target.Offset,
			parent: -1, firstChild: -1, lastChild: -1, chunk: chunk, within: within, order: i,
		}
		parents[i] = -1
		if depth > 0 {
			parents[i] = open[depth-1]
		}
		open = append(open, i)
	}
	sortStable(rows, func(x, y tocRow) bool {
		if x.depth != y.depth {
			return x.depth < y.depth
		}
		return x.offset < y.offset
	})
	position := make([]int, len(rows))
	for i, row := range rows {
		position[row.order] = i
	}
	for i := range rows {
		rows[i].length = rowLength(rows, rows[i], htmlLength)
		if parent := parents[rows[i].order]; parent >= 0 {
			rows[i].parent = position[parent]
			enclosing := &rows[position[parent]]
			if enclosing.firstChild < 0 {
				enclosing.firstChild = i
			}
			enclosing.lastChild = i
		}
	}
	return rows
}

// rowLength returns how much text the row covers: up to the next row at
// the same or a shallower depth, or to the end of the HTML.
func rowLength(rows []tocRow, row tocRow, end int) int {
	next := end
	for _, other := range rows {
		if other.depth <= row.depth && other.offset > row.offset && other.offset < next {
			next = other.offset
		}
	}
	return next - row.offset
}

// contentsIndex returns the index holding the table of contents, with
// the strings holding its titles.
func contentsIndex(rows []tocRow) (*index, [][]byte) {
	ix := &index{tags: []tagDef{
		{1, 1, 1}, {2, 1, 2}, {3, 1, 4}, {4, 1, 8}, {21, 1, 16}, {22, 1, 32}, {23, 1, 64}, {6, 2, 128},
		endTag, endTag,
	}}
	var titles stringTable
	width := max(2, len(fmt.Sprintf("%X", len(rows)-1)))
	for i, row := range rows {
		values := [][]int{
			{row.offset}, {row.length}, {titles.add(row.title)}, {row.depth}, nil, nil, nil,
			{row.chunk, row.within},
		}
		if row.parent >= 0 {
			values[4] = []int{row.parent}
		}
		if row.firstChild >= 0 {
			values[5], values[6] = []int{row.firstChild}, []int{row.lastChild}
		}
		ix.entries = append(ix.entries, indexEntry{key: fmt.Sprintf("%0*X", width, i), values: values})
	}
	records := titles.finish()
	ix.strings = len(records)
	return ix, records
}

// overlap returns the bytes after position end that complete a character
// cut in two by a record boundary at end.
func overlap(text []byte, end int) []byte {
	if end >= len(text) || utf8.RuneStart(text[end]) {
		return nil
	}
	stop := end
	for stop < len(text) && !utf8.RuneStart(text[stop]) {
		stop++
	}
	return text[end:stop]
}

// textRecords cuts the text into compressed records. Each record ends
// with the bytes completing a character split by the cut, a count of
// those bytes and, when given, its indexing entry.
func textRecords(text []byte, indexing [][]byte) [][]byte {
	var out [][]byte
	for start := 0; start < len(text); start += recordSize {
		end := min(start+recordSize, len(text))
		extra := overlap(text, end)
		record := compress(text[start:end])
		record = append(record, extra...)
		record = append(record, byte(len(extra)))
		if len(indexing) > len(out) {
			record = append(record, indexing[len(out)]...)
		}
		out = append(out, record)
	}
	return out
}

// flowTable returns the FDST record listing where every flow starts and
// ends in the text.
func flowTable(flows [][]byte) []byte {
	var b buffer
	b.WriteString("FDST")
	b.u32(12, len(flows))
	offset := 0
	for _, flow := range flows {
		b.u32(offset, offset+len(flow))
		offset += len(flow)
	}
	return b.Bytes()
}

// flis returns the fixed FLIS record every book carries.
func flis() []byte {
	var b buffer
	b.WriteString("FLIS")
	b.u32(8)
	b.u16(65, 0)
	b.u32(0, null)
	b.u16(1, 3)
	b.u32(3, 1, null)
	return b.Bytes()
}

// fcis returns the FCIS record, which repeats the length of the text.
func fcis(textLength int) []byte {
	var b buffer
	b.WriteString("FCIS")
	b.u32(20, 16, 2, 0, textLength, 0, 40, 0, 40, 8)
	b.u16(1, 1)
	b.u32(0)
	return b.Bytes()
}

// eofRecord closes the list of records.
var eofRecord = []byte{0xE9, 0x8E, 0x0D, 0x0A}

// layout records where the parts of a book ended up among its records.
type layout struct {
	// textLength is the length of the uncompressed text.
	textLength int
	// textRecords is the number of text records.
	textRecords int
	// firstNonText is the index of the first record after the text.
	firstNonText int
	// skeletons is the index of the skeleton index, or null.
	skeletons int
	// chunks is the index of the chunk index, or null.
	chunks int
	// contents is the index of the contents index, or null.
	contents int
	// firstResource is the index of the first image record, or null.
	firstResource int
	// flowTable is the index of the FDST record.
	flowTable int
	// flowCount is the number of flows.
	flowCount int
	// fcis is the index of the FCIS record.
	fcis int
	// flis is the index of the FLIS record.
	flis int
	// indexed reports whether text records carry indexing entries.
	indexed bool
}

// assemble returns the records of the book. The first one, the header,
// is built last because it points at all the others.
func (b *Book) assemble() [][]byte {
	flows := b.flows()
	var text []byte
	for _, flow := range flows {
		text = append(text, flow...)
	}
	records := [][]byte{nil}
	add := func(more ...[]byte) int {
		records = append(records, more...)
		return len(records) - len(more)
	}
	l := layout{textLength: len(text), contents: null, firstResource: null, flowCount: len(flows)}
	rows := b.tocRows(len(flows[0]))
	trailers := indexing(rows, len(text))
	l.indexed = trailers != nil
	packed := textRecords(text, trailers)
	l.textRecords = len(packed)
	add(packed...)
	size := 0
	for _, record := range packed {
		size += len(record)
	}
	if size%4 != 0 {
		add(make([]byte, 4-size%4))
	}
	l.firstNonText = len(records)
	l.skeletons = add(b.skeletonIndex().records()...)
	chunks, selectors := b.chunkIndex()
	l.chunks = add(chunks.records()...)
	add(selectors...)
	if len(rows) > 0 {
		contents, titles := contentsIndex(rows)
		l.contents = add(contents.records()...)
		add(titles...)
	}
	if len(b.Resources) > 0 {
		l.firstResource = add(b.Resources...)
	}
	l.flowTable = add(flowTable(flows))
	l.flis = add(flis())
	l.fcis = add(fcis(len(text)))
	add(eofRecord)
	records[0] = b.header(l)
	return records
}

// databaseName returns the name of the Palm database: the title reduced
// to at most 31 plain characters.
func (b *Book) databaseName() []byte {
	name := make([]byte, 32)
	n := 0
	for _, r := range b.Title {
		if n == 31 {
			break
		}
		switch {
		case r == ' ':
			name[n] = '_'
		case r > ' ' && r < 0x7F && !strings.ContainsRune(`/\:*?"<>|`, r):
			name[n] = byte(r)
		default:
			continue
		}
		n++
	}
	if n == 0 {
		copy(name, "Unknown")
	}
	return name
}

// uid returns the number identifying the book inside its header.
func (b *Book) uid() int {
	return int(crc32.ChecksumIEEE([]byte(b.ID + b.Title)))
}

// Write writes the book to w as a Palm database.
func (b *Book) Write(w io.Writer) error {
	if err := b.validate(); err != nil {
		return err
	}
	records := b.assemble()
	if len(records) > 0xFFFF {
		return errors.New("kf8: the book is too large")
	}
	var head buffer
	head.Write(b.databaseName())
	head.u16(0, 0)
	stamp := int(b.Created.Unix())
	head.u32(stamp, stamp, 0, 0, 0, 0)
	head.WriteString("BOOKMOBI")
	head.u32(2*len(records)-1, 0)
	head.u16(len(records))
	offset := 78 + 8*len(records) + 2
	for i, record := range records {
		head.u32(offset, 2*i)
		offset += len(record)
	}
	head.u16(0)
	if _, err := w.Write(head.Bytes()); err != nil {
		return err
	}
	for _, record := range records {
		if _, err := w.Write(record); err != nil {
			return err
		}
	}
	return nil
}
