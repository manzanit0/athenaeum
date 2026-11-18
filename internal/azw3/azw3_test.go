package azw3

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/kf8"
	"github.com/manzanit0/athenaeum/internal/meta"
)

// paragraphs is the number of paragraphs in the long chapter.
const paragraphs = 400

// container is the META-INF/container.xml of the sample EPUB.
const container = `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`

// packageDoc is the package document of the sample EPUB.
const packageDoc = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="id">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
<dc:identifier id="id">urn:uuid:5d4a7f0c-0000-4000-8000-000000000002</dc:identifier>
<dc:title>The Tidewater Almanac</dc:title><dc:creator>Ines Marlow</dc:creator><dc:language>en</dc:language>
<dc:publisher>Saltmarsh Press</dc:publisher><dc:subject>Tides</dc:subject>
<meta name="cover" content="cover-image"/>
</metadata>
<manifest>
<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
<item id="css" href="styles/book.css" media-type="text/css"/>
<item id="cover-image" href="images/cover.png" media-type="image/png"/>
<item id="moon" href="images/moon.png" media-type="image/png"/>
<item id="chart" href="images/chart.svg" media-type="image/svg+xml"/>
<item id="c1" href="text/one.xhtml" media-type="application/xhtml+xml"/>
<item id="c2" href="text/two.xhtml" media-type="application/xhtml+xml"/>
<item id="c3" href="text/three.xhtml" media-type="application/xhtml+xml"/>
<item id="notes" href="text/notes.xhtml" media-type="application/xhtml+xml"/>
</manifest>
<spine toc="ncx"><itemref idref="c1"/><itemref idref="c2"/><itemref idref="c3"/></spine>
</package>`

// contents is the table of contents of the sample EPUB.
const contents = `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><navMap>
<navPoint id="n1"><navLabel><text>Spring Tides</text></navLabel><content src="text/one.xhtml"/></navPoint>
<navPoint id="n2"><navLabel><text>Neap Tides</text></navLabel><content src="text/two.xhtml"/>
  <navPoint id="n3"><navLabel><text>The Three Hundredth</text></navLabel><content src="text/two.xhtml#p300"/></navPoint>
</navPoint>
<navPoint id="n4"><navLabel><text>Storm Surge</text></navLabel><content src="text/three.xhtml"/></navPoint>
<navPoint id="n5"><navLabel><text>Notes</text></navLabel><content src="text/notes.xhtml"/></navPoint>
</navMap></ncx>`

// stylesheet is the stylesheet of the sample EPUB.
const stylesheet = `@charset "utf-8";
@font-face { font-family: X; src: url(../fonts/x.ttf); }
h1 { color: #224; }
.moon { background: url("../images/moon.png") no-repeat; }
.chart { background: url(../images/chart.svg); }
`

// chapterOne is a chapter with links, images and markup to drop.
const chapterOne = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="en">
<head><title>Spring &amp; Tides</title><link rel="stylesheet" type="text/css" href="../styles/book.css"/>
<style>p { background: url(../images/moon.png) }</style></head>
<body class="chapter" id="start"><h1 id="top">Spring Tides</h1>
<p>See the <a href="two.xhtml#p300">three hundredth</a>, the <a href="three.xhtml">last chapter</a>,
the <a href="https://example.com/tides?a=1&amp;b=2">web</a>, the <a href="notes.xhtml">notes</a>
or <a href="#top" epub:type="noteref">the top</a>.</p>
<p><img src="../images/moon.png" alt="The moon"/><br/>Caf&#233;&nbsp;&amp; cr&#232;me &lt;tides&gt;.</p>
<p><img src="../images/chart.svg" alt="A chart of the tides"/><img src="../images/missing.png"/></p>
<script>alert(1)</script></body></html>`

// chapterThree is a short chapter linking back to the first.
const chapterThree = `<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Storm Surge</title></head>
<body><h1>Storm Surge</h1><p>Back to <a href="one.xhtml#top">the start</a>.</p></body></html>`

// chapterTwo returns a chapter far larger than a chunk, wrapped in one
// element so it has to be split inside it.
func chapterTwo() string {
	var sb strings.Builder
	sb.WriteString(`<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Neap Tides</title></head><body><div class="wrapper"><h1>Neap Tides</h1>`)
	for i := range paragraphs {
		fmt.Fprintf(&sb, "<p id=\"p%d\">Paragraph %d — naïve café 日本語 text that goes on for a while so the chapter spans many chunks.</p>\n", i, i)
	}
	sb.WriteString(`</div></body></html>`)
	return sb.String()
}

// sample writes an EPUB made of the given files, added to or replacing
// those of the standard sample, and returns its path.
func sample(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"META-INF/container.xml": container,
		"OEBPS/content.opf":      packageDoc,
		"OEBPS/toc.ncx":          contents,
		"OEBPS/styles/book.css":  stylesheet,
		"OEBPS/text/one.xhtml":   chapterOne,
		"OEBPS/text/two.xhtml":   chapterTwo(),
		"OEBPS/text/three.xhtml": chapterThree,
		"OEBPS/text/notes.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>Notes.</p></body></html>`,
		"OEBPS/images/cover.png": string(booktest.PNG(color.RGBA{R: 20, G: 60, B: 110, A: 255})),
		"OEBPS/images/moon.png":  string(booktest.PNG(color.RGBA{R: 230, G: 230, B: 200, A: 255})),
		"OEBPS/images/chart.svg": `<svg xmlns="http://www.w3.org/2000/svg"/>`,
	}
	for name, content := range extra {
		files[name] = content
	}
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
	path := filepath.Join(t.TempDir(), "almanac.epub")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// complete returns the document a file stands for: its skeleton with
// every chunk put in at its place.
func complete(f kf8.File) []byte {
	doc := bytes.Clone(f.Skeleton)
	for _, c := range f.Chunks {
		doc = append(doc[:c.InsertAt:c.InsertAt], append(bytes.Clone(c.Raw), doc[c.InsertAt:]...)...)
	}
	return doc
}

// wellFormed fails the test unless doc is well formed XML.
func wellFormed(t *testing.T, name string, doc []byte) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(doc))
	for {
		if _, err := d.Token(); err == io.EOF {
			return
		} else if err != nil {
			t.Fatalf("%s is not well formed: %v", name, err)
		}
	}
}

// linkPattern matches a link to a position in the book.
var linkPattern = regexp.MustCompile(`kindle:pos:fid:([0-9A-V]{4}):off:([0-9A-V]{10})`)

// follow returns the markup a link address leads to.
func follow(t *testing.T, book *kf8.Book, address string) string {
	t.Helper()
	match := linkPattern.FindStringSubmatch(address)
	if match == nil {
		t.Fatalf("%q is not a position link", address)
	}
	chunk, _ := strconv.ParseInt(match[1], 32, 64)
	offset, _ := strconv.ParseInt(match[2], 32, 64)
	for _, f := range book.Files {
		if int(chunk) < len(f.Chunks) {
			return string(f.Chunks[chunk].Raw[offset:])
		}
		chunk -= int64(len(f.Chunks))
	}
	t.Fatalf("%q points past the last chunk", address)
	return ""
}

// convert builds the sample book.
func convert(t *testing.T) (*kf8.Book, *Report) {
	t.Helper()
	book, report, err := build(context.Background(), sample(t, nil), Options{Created: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	return book, report
}

// TestDocuments checks that every document survives as well formed
// markup, split into chunks, with what the Kindle cannot use removed.
func TestDocuments(t *testing.T) {
	book, report := convert(t)
	if len(book.Files) != 3 || report.Files != 3 {
		t.Fatalf("book has %d files, want 3", len(book.Files))
	}
	docs := make([]string, len(book.Files))
	for i, f := range book.Files {
		doc := complete(f)
		wellFormed(t, fmt.Sprintf("file %d", i), doc)
		docs[i] = string(doc)
	}
	if n := strings.Count(docs[1], "<p id="); n != paragraphs {
		t.Errorf("long chapter has %d paragraphs, want %d", n, paragraphs)
	}
	if n := len(book.Files[1].Chunks); n < 5 {
		t.Errorf("long chapter has %d chunks, want it split", n)
	}
	for i, c := range book.Files[1].Chunks {
		if len(c.Raw) > chunkSize {
			t.Errorf("chunk %d is %d bytes, over the limit", i, len(c.Raw))
		}
	}
	if !strings.Contains(string(book.Files[1].Skeleton), `<div class="wrapper" aid=`) {
		t.Errorf("the wrapper of the long chapter is not in its skeleton: %s", book.Files[1].Skeleton)
	}
	for _, want := range []string{
		`<body class="chapter" id="start" aid="0000">`, `<title>Spring &amp; Tides</title>`, ` lang="en"`,
		`href="kindle:flow:0001?mime=text/css"`, `<img src="kindle:embed:0001?mime=image/png" alt="The moon"/>`,
		"Café &amp; crème &lt;tides&gt;.", `href="https://example.com/tides?a=1&amp;b=2"`,
		"A chart of the tides", `<a aid="0006">notes</a>`, `epub:type="noteref"`,
		`<style type="text/css">p { background: url(kindle:embed:0001?mime=image/png) }</style>`,
	} {
		if !strings.Contains(docs[0], want) {
			t.Errorf("first chapter lacks %s", want)
		}
	}
	for _, unwanted := range []string{"<script", "alert", "chart.svg", "missing.png", "srcset"} {
		if strings.Contains(docs[0], unwanted) {
			t.Errorf("first chapter still has %s", unwanted)
		}
	}
	if len(report.Warnings) != 2 {
		t.Errorf("warnings = %q, want one for the SVG image and one for the missing image", report.Warnings)
	}
}

// TestLinksAndContents checks that links and the table of contents lead
// to the right places in the book.
func TestLinksAndContents(t *testing.T) {
	book, _ := convert(t)
	first := string(complete(book.Files[0]))
	links := linkPattern.FindAllString(first, -1)
	if len(links) != 3 {
		t.Fatalf("first chapter has %d position links, want 3: %v", len(links), links)
	}
	targets := []string{`<p id="p300" aid=`, `<h1 aid=`, `<h1 id="top" aid=`}
	for i, want := range targets {
		if got := follow(t, book, links[i]); !strings.HasPrefix(got, want) {
			t.Errorf("link %d leads to %.40q, want %s", i, got, want)
		}
	}
	back := linkPattern.FindString(string(complete(book.Files[2])))
	if got := follow(t, book, back); !strings.HasPrefix(got, `<h1 id="top"`) {
		t.Errorf("the link back leads to %.40q", got)
	}

	titles := make([]string, len(book.Contents))
	for i, e := range book.Contents {
		titles[i] = fmt.Sprintf("%d:%s", e.Level, e.Title)
	}
	if got := strings.Join(titles, " "); got != "0:Spring Tides 0:Neap Tides 1:The Three Hundredth 0:Storm Surge" {
		t.Errorf("contents = %s", got)
	}
	if got := follow(t, book, book.Link(book.Contents[2].Target)); !strings.HasPrefix(got, `<p id="p300"`) {
		t.Errorf("nested contents line leads to %.40q", got)
	}
	if got := follow(t, book, book.Link(book.Contents[3].Target)); !strings.HasPrefix(got, `<h1 aid=`) {
		t.Errorf("last contents line leads to %.40q", got)
	}
}

// TestStylesAndImages checks the stylesheets and images of the book.
func TestStylesAndImages(t *testing.T) {
	book, report := convert(t)
	if len(book.Flows) != 1 {
		t.Fatalf("book has %d stylesheets, want 1", len(book.Flows))
	}
	css := string(book.Flows[0])
	for _, unwanted := range []string{"@font-face", "@charset", "x.ttf", "chart.svg"} {
		if strings.Contains(css, unwanted) {
			t.Errorf("stylesheet still has %s: %s", unwanted, css)
		}
	}
	if !strings.Contains(css, "url(kindle:embed:0001?mime=image/png) no-repeat") || !strings.Contains(css, ".chart { background: none; }") {
		t.Errorf("stylesheet references were not rewritten: %s", css)
	}
	if report.Images != 1 || len(book.Resources) != 3 {
		t.Fatalf("book has %d content images and %d resources, want 1 and 3", report.Images, len(book.Resources))
	}
	if book.Cover != 1 || book.Thumbnail != 2 {
		t.Errorf("cover is resource %d and thumbnail %d, want 1 and 2", book.Cover, book.Thumbnail)
	}
	if thumb := book.Resources[book.Thumbnail]; !bytes.HasPrefix(thumb, []byte{0xFF, 0xD8, 0xFF, 0xE0}) || !bytes.Contains(thumb[:12], []byte("JFIF")) {
		t.Errorf("thumbnail starts % x, want a JFIF JPEG", thumb[:12])
	}
}

// TestConvert checks the file written for a book and the details taken
// from the options instead of the EPUB.
func TestConvert(t *testing.T) {
	dir := t.TempDir()
	cover := filepath.Join(dir, "cover.png")
	red := booktest.PNG(color.RGBA{R: 200, A: 255})
	if err := os.WriteFile(cover, red, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "book.azw3")
	opts := Options{
		Metadata: meta.Metadata{Title: "Tides, Revised", Authors: []string{"I. Marlow", "A. Editor"}, ISBN: "9780000000002"},
		Cover:    cover,
		ID:       "5d4a7f0c",
	}
	if _, err := Convert(context.Background(), sample(t, nil), out, opts); err != nil {
		t.Fatal(err)
	}
	m, read := meta.Read(out, "book.azw3")
	if m.Title != "Tides, Revised" || strings.Join(m.Authors, "+") != "I. Marlow+A. Editor" || m.ISBN != "9780000000002" {
		t.Errorf("metadata = %+v", m)
	}
	if m.Publisher != "Saltmarsh Press" || m.Language != "en" || strings.Join(m.Tags, "") != "Tides" {
		t.Errorf("details the options left out were not taken from the EPUB: %+v", m)
	}
	if read == nil || !bytes.Equal(read.Data, red) {
		t.Error("the cover given in the options is not the cover of the book")
	}
}

// TestRefusals checks the EPUB files that cannot be converted.
func TestRefusals(t *testing.T) {
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "book.azw3")
	protected := sample(t, map[string]string{"META-INF/encryption.xml": `<encryption><EncryptedData>
		<EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#aes128-cbc"/></EncryptedData></encryption>`})
	if _, err := Convert(ctx, protected, out, Options{}); !errors.Is(err, ErrProtected) {
		t.Errorf("Convert of a protected EPUB = %v, want ErrProtected", err)
	}
	fonts := sample(t, map[string]string{"META-INF/encryption.xml": `<encryption><EncryptedData>
		<EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/></EncryptedData></encryption>`})
	if _, err := Convert(ctx, fonts, out, Options{}); err != nil {
		t.Errorf("Convert of an EPUB with obfuscated fonts = %v", err)
	}
	broken := sample(t, map[string]string{"OEBPS/text/three.xhtml": `<html><body><p>Unclosed</div></body></html>`})
	if _, err := Convert(ctx, broken, out, Options{}); err == nil || !strings.Contains(err.Error(), "three.xhtml") {
		t.Errorf("Convert of malformed markup = %v, want an error naming the file", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Convert(cancelled, sample(t, nil), out, Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Convert with a cancelled context = %v", err)
	}
	if _, err := Convert(ctx, filepath.Join(t.TempDir(), "missing.epub"), out, Options{}); err == nil {
		t.Error("Convert of a missing file succeeded")
	}
}

// TestEmptyDocument checks that a document without content still makes
// a valid file.
func TestEmptyDocument(t *testing.T) {
	path := sample(t, map[string]string{"OEBPS/text/three.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"><body></body></html>`})
	book, _, err := build(context.Background(), path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Files[2].Chunks) != 1 {
		t.Fatalf("empty document has %d chunks, want 1", len(book.Files[2].Chunks))
	}
	var buf bytes.Buffer
	if err := book.Write(&buf); err != nil {
		t.Errorf("Write: %v", err)
	}
}
