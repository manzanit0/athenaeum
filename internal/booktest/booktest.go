// Package booktest builds small but well formed ebook files for tests.
package booktest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"strings"

	"github.com/manzanit0/athenaeum/internal/pdf/pdftest"
)

// Book describes the ebook to build.
type Book struct {
	// Title is the book title.
	Title string
	// Author is the book author.
	Author string
	// Publisher is the name of the publisher.
	Publisher string
	// Language is the language code.
	Language string
	// ISBN is the International Standard Book Number.
	ISBN string
	// Description is a summary of the book.
	Description string
	// Series is the name of the series.
	Series string
	// Tags lists the subjects of the book.
	Tags []string
	// Cover is an encoded cover image; nil builds a book without one.
	Cover []byte
	// Chapters names the chapters of an EPUB; empty builds a single
	// chapter called "Chapter 1".
	Chapters []string
}

// chapters returns the names of the chapters of b.
func (b Book) chapters() []string {
	if len(b.Chapters) == 0 {
		return []string{"Chapter 1"}
	}
	return b.Chapters
}

// PNG returns a small PNG image filled with the given colour.
func PNG(fill color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 60, 90))
	for y := range 90 {
		for x := range 60 {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// containerXML is the fixed META-INF/container.xml of the built EPUBs.
const containerXML = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`

// EPUB returns b as an EPUB 2 file.
func EPUB(b Book) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, method uint16, data []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err == nil {
			_, err = w.Write(data)
		}
		if err != nil {
			panic(err)
		}
	}
	add("mimetype", zip.Store, []byte("application/epub+zip"))
	add("META-INF/container.xml", zip.Deflate, []byte(containerXML))
	add("OEBPS/content.opf", zip.Deflate, []byte(opf(b)))
	add("OEBPS/toc.ncx", zip.Deflate, []byte(ncx(b)))
	for i, name := range b.chapters() {
		add(fmt.Sprintf("OEBPS/chapter%d.xhtml", i+1), zip.Deflate, []byte(chapter(b, name)))
	}
	if b.Cover != nil {
		add("OEBPS/images/cover.png", zip.Store, b.Cover)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// opf returns the package document of b.
func opf(b Book) string {
	var sb strings.Builder
	element := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&sb, "    <dc:%s>%s</dc:%s>\n", name, html.EscapeString(value), name)
		}
	}
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
    <dc:identifier id="id">urn:uuid:5d4a7f0c-0000-4000-8000-000000000001</dc:identifier>
`)
	element("title", b.Title)
	element("creator", b.Author)
	element("publisher", b.Publisher)
	element("language", b.Language)
	element("description", b.Description)
	for _, tag := range b.Tags {
		element("subject", tag)
	}
	if b.ISBN != "" {
		fmt.Fprintf(&sb, "    <dc:identifier opf:scheme=\"ISBN\">%s</dc:identifier>\n", html.EscapeString(b.ISBN))
	}
	if b.Series != "" {
		fmt.Fprintf(&sb, "    <meta name=\"calibre:series\" content=\"%s\"/>\n", html.EscapeString(b.Series))
		sb.WriteString("    <meta name=\"calibre:series_index\" content=\"2\"/>\n")
	}
	if b.Cover != nil {
		sb.WriteString("    <meta name=\"cover\" content=\"cover-image\"/>\n")
	}
	sb.WriteString("  </metadata>\n  <manifest>\n")
	sb.WriteString("    <item id=\"ncx\" href=\"toc.ncx\" media-type=\"application/x-dtbncx+xml\"/>\n")
	for i := range b.chapters() {
		fmt.Fprintf(&sb, "    <item id=\"chapter%[1]d\" href=\"chapter%[1]d.xhtml\" media-type=\"application/xhtml+xml\"/>\n", i+1)
	}
	if b.Cover != nil {
		sb.WriteString("    <item id=\"cover-image\" href=\"images/cover.png\" media-type=\"image/png\"/>\n")
	}
	sb.WriteString("  </manifest>\n  <spine toc=\"ncx\">\n")
	for i := range b.chapters() {
		fmt.Fprintf(&sb, "    <itemref idref=\"chapter%d\"/>\n", i+1)
	}
	sb.WriteString("  </spine>\n</package>\n")
	return sb.String()
}

// ncx returns the table of contents of b.
func ncx(b Book) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head><meta name="dtb:uid" content="urn:uuid:5d4a7f0c-0000-4000-8000-000000000001"/></head>
  <docTitle><text>%s</text></docTitle>
  <navMap>
`, html.EscapeString(b.Title))
	for i, name := range b.chapters() {
		fmt.Fprintf(&sb, `    <navPoint id="chapter%[1]d" playOrder="%[1]d">
      <navLabel><text>%[2]s</text></navLabel>
      <content src="chapter%[1]d.xhtml"/>
    </navPoint>
`, i+1, html.EscapeString(name))
	}
	sb.WriteString("  </navMap>\n</ncx>\n")
	return sb.String()
}

// chapter returns the chapter of b called name.
func chapter(b Book, name string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head><title>%[1]s</title></head>
  <body><h1>%[1]s</h1><h2>%[2]s</h2><p>It was a quiet morning in the library.</p></body>
</html>
`, html.EscapeString(b.Title), html.EscapeString(name))
}

// mobiHeaderLength is the length of the MOBI header in the built files.
const mobiHeaderLength = 232

// MOBI returns b as a MOBI file with an EXTH metadata block.
func MOBI(b Book) []byte {
	text := []byte("<html><body><p>It was a quiet morning in the library.</p></body></html>")
	records := [][]byte{mobiRecord0(b, len(text)), text}
	if b.Cover != nil {
		records = append(records, b.Cover)
	}
	var buf bytes.Buffer
	name := make([]byte, 32)
	copy(name, strings.ReplaceAll(b.Title, " ", "_"))
	buf.Write(name)
	buf.Write(make([]byte, 28))
	buf.WriteString("BOOKMOBI")
	buf.Write(make([]byte, 8))
	binary.Write(&buf, binary.BigEndian, uint16(len(records)))
	offset := uint32(78 + len(records)*8 + 2)
	for i, rec := range records {
		binary.Write(&buf, binary.BigEndian, offset)
		binary.Write(&buf, binary.BigEndian, uint32(i*2))
		offset += uint32(len(rec))
	}
	buf.Write([]byte{0, 0})
	for _, rec := range records {
		buf.Write(rec)
	}
	return buf.Bytes()
}

// mobiRecord0 returns the header record of the MOBI file for b, whose
// text is textLength bytes long.
func mobiRecord0(b Book, textLength int) []byte {
	exth := mobiEXTH(b)
	rec := make([]byte, 16+mobiHeaderLength)
	be := binary.BigEndian
	be.PutUint16(rec[0:], 1)
	be.PutUint32(rec[4:], uint32(textLength))
	be.PutUint16(rec[8:], 1)
	be.PutUint16(rec[10:], 4096)
	copy(rec[16:], "MOBI")
	be.PutUint32(rec[20:], mobiHeaderLength)
	be.PutUint32(rec[24:], 2)
	be.PutUint32(rec[28:], 65001)
	be.PutUint32(rec[36:], 6)
	be.PutUint32(rec[84:], uint32(len(rec)+len(exth)))
	be.PutUint32(rec[88:], uint32(len(b.Title)))
	be.PutUint32(rec[108:], 2)
	be.PutUint32(rec[128:], 0x40)
	rec = append(rec, exth...)
	rec = append(rec, b.Title...)
	return append(rec, 0, 0)
}

// mobiEXTH returns the EXTH metadata block for b.
func mobiEXTH(b Book) []byte {
	var records bytes.Buffer
	count := uint32(0)
	add := func(kind uint32, data []byte) {
		if len(data) == 0 {
			return
		}
		binary.Write(&records, binary.BigEndian, kind)
		binary.Write(&records, binary.BigEndian, uint32(len(data)+8))
		records.Write(data)
		count++
	}
	add(100, []byte(b.Author))
	add(101, []byte(b.Publisher))
	add(103, []byte(b.Description))
	add(104, []byte(b.ISBN))
	for _, tag := range b.Tags {
		add(105, []byte(tag))
	}
	add(524, []byte(b.Language))
	if b.Cover != nil {
		add(201, []byte{0, 0, 0, 0})
	}
	var exth bytes.Buffer
	exth.WriteString("EXTH")
	binary.Write(&exth, binary.BigEndian, uint32(12+records.Len()))
	binary.Write(&exth, binary.BigEndian, count)
	exth.Write(records.Bytes())
	for exth.Len()%4 != 0 {
		exth.WriteByte(0)
	}
	return exth.Bytes()
}

// PDF returns b as a single page PDF with an information dictionary.
func PDF(b Book) []byte {
	escape := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 400] >>",
		fmt.Sprintf("<< /Title (%s) /Author (%s) /Keywords (%s) >>",
			escape.Replace(b.Title), escape.Replace(b.Author), escape.Replace(strings.Join(b.Tags, ", "))),
	}
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R /Info 4 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return buf.Bytes()
}

// pdfLines is the paragraph set under every chapter heading of the PDFs
// built by TextPDF.
var pdfLines = []string{
	"It was a quiet morning in the library, and the tide tables lay open",
	"on the long desk by the window where the keeper had left them the",
	"night before, weighted with a stone from the beach so that the wind",
	"could not turn their pages.",
}

// TextPDF returns b as a PDF with a text layer: one page per chapter,
// the first of which starts with the title.
func TextPDF(b Book) []byte {
	doc := pdftest.New()
	fonts := map[string]int{"F1": doc.Font("Helvetica"), "F2": doc.Font("Helvetica-Bold")}
	var pages []pdftest.Page
	for i, name := range b.chapters() {
		var content strings.Builder
		y := 700.0
		if i == 0 {
			content.WriteString(pdftest.Text("F2", 24, 72, y, b.Title))
			y -= 60
		}
		content.WriteString(pdftest.Text("F2", 18, 72, y, name))
		for _, line := range pdfLines {
			y -= 14
			content.WriteString(pdftest.Text("F1", 10, 72, y-16, line))
		}
		pages = append(pages, pdftest.Page{Content: content.String(), Fonts: fonts})
	}
	doc.Document(pages, "")
	doc.Info = doc.Add(fmt.Sprintf("<< /Title (%s) /Author (%s) >>", pdftest.Escape(b.Title), pdftest.Escape(b.Author)))
	return doc.Bytes("")
}

// ScannedPDF returns b as a PDF whose pages are pictures, the way a
// scanner makes them: scans pages that each show one grey image and
// nothing else, followed by one page with a text layer for every
// chapter.
func ScannedPDF(b Book, scans int) []byte {
	doc := pdftest.New()
	const width, height = 40, 60
	pixels := bytes.Repeat([]byte{0xEE}, width*height)
	image := doc.Flate(fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d "+
		"/ColorSpace /DeviceGray /BitsPerComponent 8", width, height), pixels)
	var pages []pdftest.Page
	for range scans {
		pages = append(pages, pdftest.Page{
			Content: "q 612 0 0 792 0 0 cm /Im1 Do Q", XObjects: map[string]int{"Im1": image},
		})
	}
	fonts := map[string]int{"F1": doc.Font("Helvetica"), "F2": doc.Font("Helvetica-Bold")}
	for _, name := range b.Chapters {
		var content strings.Builder
		content.WriteString(pdftest.Text("F2", 18, 72, 700, name))
		for i, line := range pdfLines {
			content.WriteString(pdftest.Text("F1", 10, 72, 670-float64(i)*14, line))
		}
		pages = append(pages, pdftest.Page{Content: content.String(), Fonts: fonts})
	}
	doc.Document(pages, "")
	doc.Info = doc.Add(fmt.Sprintf("<< /Title (%s) /Author (%s) >>", pdftest.Escape(b.Title), pdftest.Escape(b.Author)))
	return doc.Bytes("")
}
