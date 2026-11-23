package docx_test

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/docx"
	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/preview"
)

// unpack returns the files of the EPUB at path by name.
func unpack(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	files := map[string]string{}
	for i, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r)
		r.Close()
		files[f.Name] = string(data)
		if i == 0 && (f.Name != "mimetype" || f.Method != zip.Store || string(data) != "application/epub+zip") {
			t.Errorf("first file is %q stored with method %d holding %q", f.Name, f.Method, data)
		}
	}
	return files
}

// checkXML fails the test when one of the XML files of an EPUB is not
// well formed.
func checkXML(t *testing.T, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if !strings.HasSuffix(name, ".xhtml") && !strings.HasSuffix(name, ".opf") && !strings.HasSuffix(name, ".ncx") &&
			!strings.HasSuffix(name, ".xml") {
			continue
		}
		d := xml.NewDecoder(strings.NewReader(content))
		for {
			if _, err := d.Token(); err == io.EOF {
				break
			} else if err != nil {
				t.Errorf("%s is not well formed: %v", name, err)
				break
			}
		}
	}
}

// contains fails the test unless text holds every one of the wanted
// pieces in the order given.
func contains(t *testing.T, name, text string, wanted ...string) {
	t.Helper()
	at := 0
	for _, piece := range wanted {
		found := strings.Index(text[at:], piece)
		if found < 0 {
			t.Errorf("%s: %q is missing, or out of order, in:\n%s", name, piece, text)
			return
		}
		at += found + len(piece)
	}
}

// lacks fails the test when text holds one of the unwanted pieces.
func lacks(t *testing.T, name, text string, unwanted ...string) {
	t.Helper()
	for _, piece := range unwanted {
		if strings.Contains(text, piece) {
			t.Errorf("%s: %q should have been left out of:\n%s", name, piece, text)
		}
	}
}

// documentHead opens the main part of a built document, declaring the
// namespaces its elements use.
const documentHead = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
 xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
 xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
 xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
 xmlns:mc="http://schemas.openxmlformats.org/markup-compatibility/2006"
 xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"
 xmlns:v="urn:schemas-microsoft-com:vml"><w:body>`

// documentTail closes the main part of a built document.
const documentTail = `<w:sectPr/></w:body></w:document>`

// wordStyles holds the styles the built documents use, named as Word
// names them.
const wordStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:docDefaults><w:rPrDefault><w:rPr><w:sz w:val="24"/><w:lang w:val="en-GB"/></w:rPr></w:rPrDefault></w:docDefaults>
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
  <w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/>
    <w:pPr><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:sz w:val="36"/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/>
    <w:pPr><w:outlineLvl w:val="1"/></w:pPr><w:rPr><w:b/></w:rPr></w:style>
  <w:style w:type="paragraph" w:styleId="Chapter"><w:name w:val="Chapter Title"/><w:basedOn w:val="Heading1"/></w:style>
  <w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/></w:style>
  <w:style w:type="paragraph" w:styleId="TOC1"><w:name w:val="toc 1"/><w:basedOn w:val="Normal"/></w:style>
  <w:style w:type="character" w:styleId="Strong"><w:name w:val="Strong"/><w:rPr><w:b/></w:rPr></w:style>
</w:styles>
`

// document describes a Word document to build for a test.
type document struct {
	// body holds the elements of the body.
	body string
	// styles replaces the usual styles; "none" leaves them out.
	styles string
	// numbering holds the list definitions, if any.
	numbering string
	// footnotes holds the footnotes part, if any.
	footnotes string
	// rels holds further references of the main part, as XML.
	rels string
	// files holds further files by name.
	files map[string]string
}

// write builds the document in a temporary folder and returns its path.
func (d document) write(t *testing.T) string {
	t.Helper()
	rels := d.rels
	files := map[string]string{
		"_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`</Relationships>`,
		"word/document.xml": documentHead + d.body + documentTail,
	}
	part := func(name, kind, content string) {
		if content == "" {
			return
		}
		files["word/"+name] = content
		rels += fmt.Sprintf(`<Relationship Id="part-%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/%s" Target="%s"/>`,
			kind, kind, name)
	}
	if d.styles == "" {
		d.styles = wordStyles
	}
	if d.styles != "none" {
		part("styles.xml", "styles", d.styles)
	}
	part("numbering.xml", "numbering", d.numbering)
	part("footnotes.xml", "footnotes", d.footnotes)
	files["word/_rels/document.xml.rels"] = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + rels + `</Relationships>`
	for name, content := range d.files {
		files[name] = content
	}
	path := filepath.Join(t.TempDir(), "book.docx")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	for name, content := range files {
		w, err := zw.Create(name)
		if err == nil {
			_, err = w.Write([]byte(content))
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// convert builds d, converts it and returns the report and the files of
// the book, after checking that they are well formed and that the
// reader of the application can open them.
func convert(t *testing.T, d document, opts docx.Options) (*docx.Report, map[string]string) {
	t.Helper()
	src := d.write(t)
	dst := filepath.Join(filepath.Dir(src), "book.epub")
	report, err := docx.Convert(context.Background(), src, dst, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preview.ReadOutline(dst); err != nil {
		t.Errorf("the preview cannot open the book: %v", err)
	}
	files := unpack(t, dst)
	checkXML(t, files)
	return report, files
}

// para returns a paragraph in the given style holding the given runs.
func para(style, runs string) string {
	if style == "" {
		return "<w:p>" + runs + "</w:p>"
	}
	return `<w:p><w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>` + runs + "</w:p>"
}

// run returns a run of the given text.
func run(text string) string {
	return `<w:r><w:t xml:space="preserve">` + text + `</w:t></w:r>`
}

// styled returns a run of the given text with the given properties.
func styled(properties, text string) string {
	return `<w:r><w:rPr>` + properties + `</w:rPr><w:t xml:space="preserve">` + text + `</w:t></w:r>`
}

// TestConvertBook checks a whole book: its sections, its table of
// contents, its details and its cover.
func TestConvertBook(t *testing.T) {
	book := booktest.Book{
		Title: "The Tidewater Almanac", Author: "Ines Marlow", Language: "en",
		Description: "A year on the estuary.", Tags: []string{"Tides", "Weather"},
		Chapters: []string{"Spring Tides", "Neap Tides", "Storm Surge"},
	}
	dir := t.TempDir()
	src, dst, cover := filepath.Join(dir, "almanac.docx"), filepath.Join(dir, "almanac.epub"), filepath.Join(dir, "cover.png")
	if err := os.WriteFile(src, booktest.DOCX(book), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cover, booktest.PNG(color.RGBA{R: 30, G: 60, B: 90, A: 255}), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := docx.Convert(context.Background(), src, dst, docx.Options{Cover: cover})
	if err != nil {
		t.Fatal(err)
	}
	if report.Sections != 3 || len(report.Warnings) != 0 {
		t.Errorf("report = %+v, want three sections and no warnings", *report)
	}
	outline, err := preview.ReadOutline(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(outline.Sections) != 3 || len(outline.Contents) != 3 || outline.Contents[1].Title != "Neap Tides" {
		t.Errorf("outline = %+v, want three sections listed by their headings", outline)
	}
	files := unpack(t, dst)
	checkXML(t, files)
	contains(t, "package", files["OEBPS/content.opf"],
		"<dc:title>The Tidewater Almanac</dc:title>", "<dc:language>en</dc:language>",
		"<dc:creator>Ines Marlow</dc:creator>", "<dc:description>A year on the estuary.</dc:description>",
		"<dc:subject>Tides</dc:subject>", "<dc:subject>Weather</dc:subject>",
		`href="images/cover.png" media-type="image/png" properties="cover-image"`)
	contains(t, "second section", files["OEBPS/section0002.xhtml"],
		`<h1 id="heading-2">Neap Tides</h1>`, "<p>It was a quiet morning in the library.</p>")
	read, _ := meta.Read(dst, "almanac.epub")
	if read.Title != book.Title || len(read.Authors) != 1 || read.Authors[0] != book.Author {
		t.Errorf("the book declares %+v, want the details of the document", read)
	}
}

// TestOptionsOverride checks that the details given with a conversion
// come before those of the document.
func TestOptionsOverride(t *testing.T) {
	d := document{body: para("", run("Text."))}
	_, files := convert(t, d, docx.Options{
		Metadata: meta.Metadata{Title: "Given Title", Authors: []string{"Given Author"}, Language: "es"},
		ID:       "book-1",
	})
	contains(t, "package", files["OEBPS/content.opf"],
		`<dc:identifier id="id">book-1</dc:identifier>`, "<dc:title>Given Title</dc:title>",
		"<dc:language>es</dc:language>", "<dc:creator>Given Author</dc:creator>")
	contains(t, "section", files["OEBPS/section0001.xhtml"], `xml:lang="es"`, "<title>Given Title</title>")
	contains(t, "contents", files["OEBPS/nav.xhtml"], `<a href="section0001.xhtml">Given Title</a>`)
}

// TestText checks how the text of paragraphs comes through: headings
// by style, by a style based on one and by outline level, the looks of
// runs, quotations, alignment and line breaks.
func TestText(t *testing.T) {
	d := document{body: para("Chapter", run("One")) +
		para("", run("Plain ")+styled("<w:b/>", "bold")+styled("<w:i/>", " slanted")+
			styled(`<w:u w:val="single"/>`, " lined")+styled("<w:strike/>", " struck")+
			styled(`<w:vertAlign w:val="superscript"/>`, "2")+styled(`<w:rStyle w:val="Strong"/>`, " strong")+
			styled(`<w:b w:val="0"/><w:u w:val="none"/>`, " plain")) +
		para("Heading2", run("Part &lt;a&gt; &amp; b")) +
		para("Quote", run("Quoted.")) +
		`<w:p><w:pPr><w:jc w:val="center"/></w:pPr>` + run("Centred") + `<w:r><w:br/><w:t>next line</w:t><w:tab/><w:t>tabbed</w:t></w:r></w:p>` +
		`<w:p><w:pPr><w:outlineLvl w:val="0"/></w:pPr>` + run("Two") + `</w:p>` +
		para("", "") + para("", run("End."))}
	report, files := convert(t, d, docx.Options{})
	if report.Sections != 2 {
		t.Errorf("sections = %d, want one for each heading of the highest level", report.Sections)
	}
	contains(t, "first section", files["OEBPS/section0001.xhtml"], `xml:lang="en-GB"`,
		`<h1 id="heading-1">One</h1>`,
		"<p>Plain <b>bold</b><i> slanted</i><u> lined</u><s> struck</s><sup>2</sup><b> strong</b> plain</p>",
		`<h2 id="heading-2">Part &lt;a&gt; &amp; b</h2>`,
		"<blockquote><p>Quoted.</p></blockquote>",
		`<p class="center">Centred<br/>next line tabbed</p>`)
	contains(t, "second section", files["OEBPS/section0002.xhtml"], `<h1 id="heading-3">Two</h1>`, "<p>End.</p>")
	lacks(t, "second section", files["OEBPS/section0002.xhtml"], "<p></p>")
	contains(t, "contents", files["OEBPS/nav.xhtml"],
		`<a href="section0001.xhtml#heading-1">One</a>`, `<a href="section0001.xhtml#heading-2">Part &lt;a&gt; &amp; b</a>`,
		`<a href="section0002.xhtml#heading-3">Two</a>`)
}

// TestLists checks that list items nest by depth and that numbered
// lists are told from bulleted ones.
func TestLists(t *testing.T) {
	item := func(list, depth int, text string) string {
		return fmt.Sprintf(`<w:p><w:pPr><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr></w:pPr>%s</w:p>`,
			depth, list, run(text))
	}
	d := document{
		body: item(1, 0, "First") + item(1, 1, "Inner") + item(1, 0, "Second") +
			item(2, 0, "Step one") + item(2, 0, "Step two") + para("", run("After.")),
		numbering: `<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
			`<w:abstractNum w:abstractNumId="7"><w:lvl w:ilvl="0"><w:numFmt w:val="bullet"/></w:lvl><w:lvl w:ilvl="1"><w:numFmt w:val="bullet"/></w:lvl></w:abstractNum>` +
			`<w:abstractNum w:abstractNumId="8"><w:lvl w:ilvl="0"><w:numFmt w:val="decimal"/></w:lvl></w:abstractNum>` +
			`<w:num w:numId="1"><w:abstractNumId w:val="7"/></w:num><w:num w:numId="2"><w:abstractNumId w:val="8"/></w:num></w:numbering>`,
	}
	_, files := convert(t, d, docx.Options{})
	contains(t, "section", files["OEBPS/section0001.xhtml"],
		"<ul>\n<li>First<ul>\n<li>Inner</li>\n</ul>\n</li>\n<li>Second</li>\n</ul>\n",
		"<ol>\n<li>Step one</li>\n<li>Step two</li>\n</ol>\n<p>After.</p>")
}

// TestTable checks that tables keep their rows, their cells and the
// columns a cell covers.
func TestTable(t *testing.T) {
	cell := func(properties, text string) string {
		return "<w:tc><w:tcPr>" + properties + "</w:tcPr>" + para("", run(text)) + "</w:tc>"
	}
	d := document{body: "<w:tbl><w:tr>" + cell(`<w:gridSpan w:val="2"/>`, "Tides") + "</w:tr><w:tr>" +
		cell("", "March") + cell("", "4.2 m") + "</w:tr></w:tbl>"}
	_, files := convert(t, d, docx.Options{})
	contains(t, "section", files["OEBPS/section0001.xhtml"],
		"<table>\n<tr><td colspan=\"2\"><p>Tides</p>\n</td></tr>\n<tr><td><p>March</p>\n</td><td><p>4.2 m</p>\n</td></tr>\n</table>")
}

// TestLinks checks links to addresses and to anchors, across sections,
// whether written as link elements or as fields.
func TestLinks(t *testing.T) {
	d := document{
		body: para("Heading1", run("One")) +
			"<w:p>" + `<w:hyperlink r:id="link1">` + styled(`<w:u w:val="single"/>`, "outside") + `</w:hyperlink>` +
			`<w:hyperlink w:anchor="Target">` + run(" inside") + `</w:hyperlink>` +
			`<w:hyperlink w:anchor="Nowhere">` + run(" nowhere") + `</w:hyperlink>` +
			`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText> HYPERLINK "https://example.com/field" </w:instrText></w:r>` +
			`<w:r><w:fldChar w:fldCharType="separate"/></w:r>` + run(" by field") + `<w:r><w:fldChar w:fldCharType="end"/></w:r>` +
			run(" after") + "</w:p>" +
			para("Heading1", run("Two")) +
			`<w:p><w:bookmarkStart w:id="1" w:name="Target"/><w:bookmarkEnd w:id="1"/></w:p>` +
			para("", `<w:bookmarkStart w:id="2" w:name="_GoBack"/>`+run("Here.")),
		rels: `<Relationship Id="link1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="https://example.com/a?b=1&amp;c=2" TargetMode="External"/>`,
	}
	_, files := convert(t, d, docx.Options{})
	contains(t, "first section", files["OEBPS/section0001.xhtml"],
		`<a href="https://example.com/a?b=1&amp;c=2">outside</a>`,
		`<a href="section0002.xhtml#bm-Target"> inside</a>`, " nowhere",
		`<a href="https://example.com/field"> by field</a> after</p>`)
	lacks(t, "first section", files["OEBPS/section0001.xhtml"], "HYPERLINK", "<u>")
	contains(t, "second section", files["OEBPS/section0002.xhtml"], `<p><a id="bm-Target"></a>Here.</p>`)
	lacks(t, "second section", files["OEBPS/section0002.xhtml"], "_GoBack")
}

// TestNotes checks that footnotes are gathered in a section of their
// own, linked from the text and back.
func TestNotes(t *testing.T) {
	note := func(id, kind, text string) string {
		return `<w:footnote w:id="` + id + `"` + kind + `>` + para("", `<w:r><w:footnoteRef/></w:r>`+run(text)) + `</w:footnote>`
	}
	d := document{
		body: para("Heading1", run("One")) +
			para("", run("Claim")+`<w:r><w:footnoteReference w:id="2"/></w:r>`+run(" and more")+`<w:r><w:footnoteReference w:id="9"/></w:r>`),
		footnotes: `<w:footnotes xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
			note("0", ` w:type="separator"`, "line") + note("2", "", "The source.") + `</w:footnotes>`,
	}
	report, files := convert(t, d, docx.Options{})
	if report.Sections != 1 {
		t.Errorf("sections = %d, want the notes not counted among them", report.Sections)
	}
	contains(t, "section", files["OEBPS/section0001.xhtml"],
		`Claim<sup><a id="ref-1" href="notes.xhtml#note-1" epub:type="noteref">1</a></sup> and more</p>`)
	contains(t, "notes", files["OEBPS/notes.xhtml"], "<h1>Notes</h1>",
		`<div class="note" id="note-1">`, `<a href="section0001.xhtml#ref-1">1</a>`, "<p>The source.</p>")
	lacks(t, "notes", files["OEBPS/notes.xhtml"], "line")
	contains(t, "contents", files["OEBPS/nav.xhtml"], `<a href="notes.xhtml">Notes</a>`)
}

// TestPictures checks that pictures a book can show are carried over,
// once each, and that the others are counted as left out.
func TestPictures(t *testing.T) {
	drawing := func(id string) string {
		return `<w:r><w:drawing><wp:inline><wp:docPr id="1" name="Picture" descr="A &quot;chart&quot;"/>` +
			`<a:graphic><a:graphicData><a:blip r:embed="` + id + `"/></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`
	}
	image := func(id, target string) string {
		return `<Relationship Id="` + id + `" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="` + target + `"/>`
	}
	d := document{
		body: para("", drawing("img1")) + para("", run("See ")+drawing("img1")+run(".")) +
			para("", drawing("img2")) + para("", drawing("gone")) +
			para("", `<w:r><w:pict><v:shape><v:imagedata r:id="img3"/></v:shape></w:pict></w:r>`),
		rels: image("img1", "media/image1.png") + image("img2", "media/image2.emf") + image("img3", "media/image3.jpeg"),
		files: map[string]string{
			"word/media/image1.png":  string(booktest.PNG(color.RGBA{R: 200, A: 255})),
			"word/media/image2.emf":  "not a picture a book can show",
			"word/media/image3.jpeg": string(booktest.JPEG(color.RGBA{G: 200, A: 255})),
		},
	}
	report, files := convert(t, d, docx.Options{})
	if report.Images != 2 || len(report.Warnings) != 1 || !strings.HasPrefix(report.Warnings[0], "2 pictures") {
		t.Errorf("report = %+v, want two pictures shown and two left out", *report)
	}
	contains(t, "section", files["OEBPS/section0001.xhtml"],
		`<div class="figure"><img src="images/image0001.png" alt="A &#34;chart&#34;"/></div>`,
		`<p>See <img src="images/image0001.png" alt="A &#34;chart&#34;"/>.</p>`,
		`<div class="figure"><img src="images/image0004.jpg" alt=""/></div>`)
	if files["OEBPS/images/image0001.png"] == "" || files["OEBPS/images/image0004.jpg"] == "" {
		t.Errorf("the pictures are missing from the book: %v", len(files))
	}
	contains(t, "package", files["OEBPS/content.opf"], `href="images/image0001.png" media-type="image/png"`)
}

// TestLeftOut checks what a book has no use for or cannot hold: tables
// of contents with the headings that announce them, page numbers,
// deleted text, text boxes and equations.
func TestLeftOut(t *testing.T) {
	d := document{body: para("Heading1", run("Contents")) +
		`<w:p><w:pPr><w:pStyle w:val="TOC1"/></w:pPr><w:r><w:fldChar w:fldCharType="begin"/></w:r>` +
		`<w:r><w:instrText xml:space="preserve"> TOC \o "1-3" </w:instrText></w:r><w:r><w:fldChar w:fldCharType="separate"/></w:r>` +
		run("One") + run("1") + `</w:p>` +
		para("TOC1", run("Two 5")+`<w:r><w:fldChar w:fldCharType="end"/></w:r>`) +
		para("Heading1", run("One")) +
		para("", run("Kept")+`<w:del><w:r><w:delText> deleted</w:delText></w:r></w:del>`+`<w:ins>`+run(" added")+`</w:ins>`+
			run(", page ")+`<w:fldSimple w:instr=" PAGE ">`+run("7")+`</w:fldSimple>`+
			`<w:r><w:fldChar w:fldCharType="begin"/></w:r><w:r><w:instrText> PAGEREF _Toc1 \h </w:instrText></w:r>`+
			`<w:r><w:fldChar w:fldCharType="separate"/></w:r>`+run("12")+`<w:r><w:fldChar w:fldCharType="end"/></w:r>`+run(".")) +
		para("", `<w:r><mc:AlternateContent><mc:Choice><w:drawing><w:txbxContent>`+para("", run("In a box"))+
			`</w:txbxContent></w:drawing></mc:Choice><mc:Fallback><w:pict><w:txbxContent>`+para("", run("In a box again"))+
			`</w:txbxContent></w:pict></mc:Fallback></mc:AlternateContent></w:r>`+run("Beside the box.")) +
		para("", `<m:oMathPara><m:oMath><m:r><m:t>x=1</m:t></m:r></m:oMath></m:oMathPara>`+run("Formula above."))}
	report, files := convert(t, d, docx.Options{})
	if report.Sections != 1 {
		t.Errorf("sections = %d, want one: the table of contents and its heading left out", report.Sections)
	}
	section := files["OEBPS/section0001.xhtml"]
	contains(t, "section", section, `<h1 id="heading-1">One</h1>`, "<p>Kept added, page .</p>",
		"<p>Beside the box.</p>", "<p>Formula above.</p>")
	lacks(t, "section", section, "Contents", "Two 5", "deleted", "TOC", "PAGEREF", "12", "In a box", "x=1")
	want := []string{
		"1 piece of text in a box placed on the page could not be included.",
		"1 equation could not be included.",
	}
	if strings.Join(report.Warnings, "|") != strings.Join(want, "|") {
		t.Errorf("warnings = %q, want %q", report.Warnings, want)
	}
}

// TestInferredHeadings checks that a document without heading styles
// gets its headings from the size of its text.
func TestInferredHeadings(t *testing.T) {
	sized := func(size, text string) string {
		return "<w:p>" + styled(`<w:b/><w:sz w:val="`+size+`"/>`, text) + "</w:p>"
	}
	body := "<w:p>" + styled(`<w:sz w:val="24"/>`, strings.Repeat("Ordinary text of the book. ", 12)) + "</w:p>"
	d := document{styles: "none", body: sized("48", "Spring Tides") + body + sized("36", "Neap Tides") + body +
		sized("48", "Storm Surge") + body + "<w:p>" + styled(`<w:b/><w:sz w:val="24"/>`, "Bold but small.") + "</w:p>"}
	report, files := convert(t, d, docx.Options{})
	if report.Sections != 2 {
		t.Errorf("sections = %d, want one for each of the largest headings", report.Sections)
	}
	contains(t, "first section", files["OEBPS/section0001.xhtml"],
		`<h1 id="heading-1">Spring Tides</h1>`, `<h2 id="heading-2">Neap Tides</h2>`)
	contains(t, "second section", files["OEBPS/section0002.xhtml"],
		`<h1 id="heading-3">Storm Surge</h1>`, "<p><b>Bold but small.</b></p>")
}

// TestPageBreaks checks that a paragraph that starts a page starts a
// section, however the document says so.
func TestPageBreaks(t *testing.T) {
	d := document{body: para("", run("First page.")) +
		`<w:p><w:pPr><w:pageBreakBefore/></w:pPr>` + run("Second page.") + `</w:p>` +
		para("", run("Still second.")+`<w:r><w:br w:type="page"/></w:r>`) + para("", run("Third page.")) +
		para("", `<w:r><w:br w:type="page"/></w:r>`) + para("", run("Fourth page.")) +
		para("", `<w:r><w:br w:type="page"/></w:r>`+run("Fifth page."))}
	report, files := convert(t, d, docx.Options{})
	if report.Sections != 5 {
		t.Errorf("sections = %d, want five", report.Sections)
	}
	contains(t, "second section", files["OEBPS/section0002.xhtml"], "<p>Second page.</p>", "<p>Still second.</p>")
	contains(t, "third section", files["OEBPS/section0003.xhtml"], "<p>Third page.</p>")
	contains(t, "fourth section", files["OEBPS/section0004.xhtml"], "<p>Fourth page.</p>")
	contains(t, "fifth section", files["OEBPS/section0005.xhtml"], "<p>Fifth page.</p>")
}

// TestNotDocument checks that files that are not Word documents are
// refused.
func TestNotDocument(t *testing.T) {
	dir := t.TempDir()
	text := filepath.Join(dir, "text.docx")
	if err := os.WriteFile(text, []byte("not a ZIP file"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.docx")
	if err := os.WriteFile(other, booktest.EPUB(booktest.Book{Title: "An EPUB"}), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{text, other} {
		dst := filepath.Join(dir, "out.epub")
		if _, err := docx.Convert(context.Background(), src, dst, docx.Options{}); !errors.Is(err, docx.ErrNotDocument) {
			t.Errorf("%s: err = %v, want ErrNotDocument", filepath.Base(src), err)
		}
		if _, err := os.Stat(dst); err == nil {
			t.Errorf("%s: a book was written", filepath.Base(src))
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".docx-*")); len(left) > 0 {
		t.Errorf("temporary files were left behind: %v", left)
	}
}
