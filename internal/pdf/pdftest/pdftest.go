// Package pdftest builds small PDF files for tests.
package pdftest

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"sort"
	"strings"
)

// Builder collects the objects of a PDF file.
type Builder struct {
	// objects holds the object bodies; object n is at index n-1.
	objects [][]byte
	// streams marks the objects that are streams.
	streams map[int]bool
	// Root is the object number of the document catalog.
	Root int
	// Info is the object number of the information dictionary, or zero.
	Info int
	// PageNums holds the object numbers of the pages made by Document.
	PageNums []int
}

// Page describes one page for Document.
type Page struct {
	// Content is the content stream.
	Content string
	// Fonts maps resource names to font object numbers.
	Fonts map[string]int
	// XObjects maps resource names to XObject numbers.
	XObjects map[string]int
	// Width is the page width in points; zero means 612.
	Width float64
	// Height is the page height in points; zero means 792.
	Height float64
	// Rotate is the page rotation in degrees.
	Rotate int
}

// New returns an empty builder.
func New() *Builder {
	return &Builder{streams: map[int]bool{}}
}

// Add adds an object with the given body and returns its number.
func (b *Builder) Add(body string) int {
	b.objects = append(b.objects, []byte(body))
	return len(b.objects)
}

// Reserve returns the number of an object to be defined later with Set.
func (b *Builder) Reserve() int {
	return b.Add("null")
}

// Set defines the body of the object num.
func (b *Builder) Set(num int, body string) {
	b.objects[num-1] = []byte(body)
}

// Stream adds a stream whose dictionary holds the given entries next to
// its length, and returns its number.
func (b *Builder) Stream(entries string, data []byte) int {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "<< %s /Length %d >>\nstream\n", entries, len(data))
	buf.Write(data)
	buf.WriteString("\nendstream")
	b.objects = append(b.objects, buf.Bytes())
	b.streams[len(b.objects)] = true
	return len(b.objects)
}

// Deflate compresses data the way FlateDecode expects.
func Deflate(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

// Flate adds a stream compressed with FlateDecode and returns its
// number.
func (b *Builder) Flate(entries string, data []byte) int {
	return b.Stream(entries+" /Filter /FlateDecode", Deflate(data))
}

// Font adds a simple font that uses the standard typeface base with
// WinAnsiEncoding and no widths, and returns its number.
func (b *Builder) Font(base string) int {
	return b.Add(fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /%s /Encoding /WinAnsiEncoding >>", base))
}

// resources formats a resource sub-dictionary.
func resources(kind string, entries map[string]int) string {
	if len(entries) == 0 {
		return ""
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	var sb strings.Builder
	fmt.Fprintf(&sb, "/%s <<", kind)
	for _, name := range names {
		fmt.Fprintf(&sb, " /%s %d 0 R", name, entries[name])
	}
	sb.WriteString(" >> ")
	return sb.String()
}

// Document adds the page tree and the catalog for pages and returns the
// number of the catalog. The catalog holds the extra entries given.
func (b *Builder) Document(pages []Page, catalogEntries string) int {
	tree := b.Reserve()
	var kids strings.Builder
	for _, page := range pages {
		w, h := page.Width, page.Height
		if w == 0 {
			w = 612
		}
		if h == 0 {
			h = 792
		}
		content := b.Flate("", []byte(page.Content))
		num := b.Add(fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %g %g] /Rotate %d /Contents %d 0 R /Resources << %s%s>> >>",
			tree, w, h, page.Rotate, content, resources("Font", page.Fonts), resources("XObject", page.XObjects)))
		b.PageNums = append(b.PageNums, num)
		fmt.Fprintf(&kids, "%d 0 R ", num)
	}
	b.Set(tree, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), len(pages)))
	b.Root = b.Add(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R %s >>", tree, catalogEntries))
	return b.Root
}

// trailer formats the entries shared by both kinds of trailer.
func (b *Builder) trailer(size int, extra string) string {
	info := ""
	if b.Info != 0 {
		info = fmt.Sprintf(" /Info %d 0 R", b.Info)
	}
	return fmt.Sprintf("/Size %d /Root %d 0 R%s %s", size, b.Root, info, extra)
}

// Bytes returns the file with a classic cross reference table. The
// trailer holds the extra entries given.
func (b *Builder) Bytes(trailerEntries string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(b.objects))
	for i, body := range b.objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, body)
	}
	start := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(b.objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offset)
	}
	trailer := b.trailer(len(b.objects)+1, trailerEntries)
	fmt.Fprintf(&buf, "trailer\n<< %s >>\nstartxref\n%d\n%%%%EOF\n", trailer, start)
	return buf.Bytes()
}

// BytesCompressed returns the file in the PDF 1.5 layout: every object
// that is not a stream is kept in an object stream and the cross
// reference table is a stream.
func (b *Builder) BytesCompressed(trailerEntries string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.5\n%\xe2\xe3\xcf\xd3\n")
	count := len(b.objects) + 2
	container, table := len(b.objects)+1, len(b.objects)+2
	rows := make([][3]int, count+1)
	var header, bodies bytes.Buffer
	contained := 0
	for i, body := range b.objects {
		num := i + 1
		if b.streams[num] {
			rows[num] = [3]int{1, buf.Len(), 0}
			fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", num, body)
			continue
		}
		fmt.Fprintf(&header, "%d %d ", num, bodies.Len())
		bodies.Write(body)
		bodies.WriteByte('\n')
		rows[num] = [3]int{2, container, contained}
		contained++
	}
	rows[container] = [3]int{1, buf.Len(), 0}
	packed := Deflate(append(header.Bytes(), bodies.Bytes()...))
	fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /ObjStm /N %d /First %d /Filter /FlateDecode /Length %d >>\nstream\n",
		container, contained, header.Len(), len(packed))
	buf.Write(packed)
	buf.WriteString("\nendstream\nendobj\n")
	start := buf.Len()
	rows[table] = [3]int{1, start, 0}
	var entries bytes.Buffer
	for _, row := range rows {
		entries.Write([]byte{byte(row[0]), byte(row[1] >> 24), byte(row[1] >> 16), byte(row[1] >> 8), byte(row[1]),
			byte(row[2] >> 8), byte(row[2])})
	}
	data := Deflate(entries.Bytes())
	fmt.Fprintf(&buf, "%d 0 obj\n<< /Type /XRef /W [1 4 2] %s /Filter /FlateDecode /Length %d >>\nstream\n",
		table, b.trailer(count+1, trailerEntries), len(data))
	buf.Write(data)
	fmt.Fprintf(&buf, "\nendstream\nendobj\nstartxref\n%d\n%%%%EOF\n", start)
	return buf.Bytes()
}

// Escape escapes s for use inside a literal string.
func Escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`).Replace(s)
}

// Text returns content that shows s in the font resource called font at
// the given size, starting at x, y measured from the lower left corner.
func Text(font string, size, x, y float64, s string) string {
	return fmt.Sprintf("BT /%s %g Tf %g %g Td (%s) Tj ET\n", font, size, x, y, Escape(s))
}
