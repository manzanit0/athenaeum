package reflow_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/pdf/pdftest"
	"github.com/manzanit0/athenaeum/internal/preview"
	"github.com/manzanit0/athenaeum/internal/reflow"
)

// header is the running header of the sample book.
const header = "The Tidewater Almanac"

// rows returns content that shows the given lines one below the other,
// starting at x, y and stepping down by leading.
func rows(font string, size, x, y, leading float64, lines ...string) string {
	var sb strings.Builder
	for i, line := range lines {
		sb.WriteString(pdftest.Text(font, size, x, y-float64(i)*leading, line))
	}
	return sb.String()
}

// furniture returns the running header and the page number of a page.
func furniture(number int) string {
	return pdftest.Text("F1", 9, 72, 760, header) + pdftest.Text("F1", 9, 300, 30, fmt.Sprint(number))
}

// sampleBook builds a three page book with a title, two chapters, a
// paragraph that runs over a page break, a list, an image, styled text
// and a page set in two columns.
func sampleBook() *pdftest.Builder {
	b := pdftest.New()
	fonts := map[string]int{"F1": b.Font("Helvetica"), "F2": b.Font("Helvetica-Bold"), "F3": b.Font("Times-Italic")}
	var photo bytes.Buffer
	jpeg.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 40, 20)), nil)
	picture := b.Stream("/Type /XObject /Subtype /Image /Width 40 /Height 20 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode",
		photo.Bytes())
	first := furniture(1) +
		pdftest.Text("F2", 24, 72, 700, "The Tidewater Almanac") +
		pdftest.Text("F1", 10, 72, 670, "A year of tides, set down by the harbour master") +
		pdftest.Text("F2", 18, 72, 600, "Spring Tides") +
		rows("F1", 10, 72, 570, 14,
			"The spring tides arrive twice in every month of the year, when the",
			"sun and the moon pull on the water along one and the same line,",
			"and the harbour empties.") +
		rows("F1", 10, 87, 528, 14, "Careful keepers have written down their obser-") +
		rows("F1", 10, 72, 514, 14,
			"vations for a hundred years, and from those long records anyone",
			"who can read a table may learn the height of tomorrow's tide and")
	second := furniture(2) +
		rows("F1", 10, 72, 700, 14,
			"the hour at which it will turn.") +
		rows("F1", 10, 72, 660, 14, "\x95 A reliable barometer", "\x95 Charts of the northern passage") +
		"q 200 0 0 100 72 520 cm /Im1 Do Q\n" +
		pdftest.Text("F2", 18, 72, 480, "Neap Tides") +
		"BT /F1 10 Tf 72 450 Td (An ) Tj /F3 10 Tf (italic remark) Tj /F1 10 Tf ( and a ) Tj /F2 10 Tf (bold warning) Tj /F1 10 Tf ( share this line.) Tj ET\n"
	third := furniture(3) +
		rows("F1", 10, 72, 700, 14,
			"The left column is read first, from",
			"its top to its very last line, and its",
			"final sentence is not finished at the") +
		rows("F1", 10, 330, 700, 14,
			"bottom, so it carries on up here in",
			"the right column and ends.",
			"") +
		rows("F1", 10, 330, 644, 14,
			"A second paragraph of the right",
			"column closes the page.")
	pages := []pdftest.Page{
		{Content: first, Fonts: fonts},
		{Content: second, Fonts: fonts, XObjects: map[string]int{"Im1": picture}},
		{Content: third, Fonts: fonts},
	}
	b.Document(pages, "/Lang (en-GB)")
	b.Info = b.Add("<< /Title (Almanac from the PDF) /Author (Ines Marlow) /Keywords (tides; harbours) /ModDate (D:20240229103000+01'00') >>")
	return b
}

// convert writes the PDF in data to a temporary folder, converts it and
// returns the report with the path of the EPUB.
func convert(t *testing.T, data []byte, opts reflow.Options) (*reflow.Report, string) {
	t.Helper()
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "tide tables.pdf"), filepath.Join(dir, "book.epub")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := reflow.Convert(context.Background(), src, dst, opts)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return report, dst
}

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

// TestConvertBook converts the sample book and checks the structure,
// the text and the package of the result.
func TestConvertBook(t *testing.T) {
	cover := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(cover, pngImage(), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := reflow.Options{Cover: cover, Metadata: meta.Metadata{
		Title: "The Tidewater Almanac", Authors: []string{"Ines Marlow", "Odile Brandt"}, Series: "Harbour Books",
		SeriesIndex: 2, Publisher: "Saltmarsh & Sons", Language: "en", ISBN: "9780000000002",
		Description: "Tides <for> a year.", Tags: []string{"Tides"}, Published: "1931",
	}}
	report, dst := convert(t, sampleBook().Bytes(""), opts)
	if report.Pages != 3 || report.Sections != 3 || report.Images != 1 || len(report.Warnings) != 0 {
		t.Errorf("report = %+v", *report)
	}
	files := unpack(t, dst)
	checkXML(t, files)

	title, spring, neap := files["OEBPS/section0001.xhtml"], files["OEBPS/section0002.xhtml"], files["OEBPS/section0003.xhtml"]
	contains(t, "title section", title, "<h1>The Tidewater Almanac</h1>", "<p>A year of tides, set down by the harbour master</p>")
	contains(t, "first chapter", spring,
		"<h2>Spring Tides</h2>",
		"<p>The spring tides arrive twice in every month of the year, when the sun and the moon pull on the water along one and the same line, and the harbour empties.</p>",
		"<p>Careful keepers have written down their observations for a hundred years,",
		"the height of tomorrow&#39;s tide and the hour at which it will turn.</p>",
		"<ul>\n<li>A reliable barometer</li>\n<li>Charts of the northern passage</li>\n</ul>",
		`<div class="figure"><img src="images/image0001.jpg" alt=""/></div>`)
	contains(t, "second chapter", neap,
		"<h2>Neap Tides</h2>",
		"<p>An <i>italic remark</i> and a <b>bold warning</b> share this line.</p>",
		"<p>The left column is read first, from its top to its very last line, and its final sentence is not finished at the bottom, so it carries on up here in the right column and ends.</p>",
		"<p>A second paragraph of the right column closes the page.</p>")
	for name, content := range files {
		if strings.HasPrefix(name, "OEBPS/section") && (strings.Contains(content, "<p>"+header) || strings.Contains(content, "<p>2</p>")) {
			t.Errorf("%s still holds a running header or a page number:\n%s", name, content)
		}
	}

	contains(t, "package", files["OEBPS/content.opf"],
		`<dc:identifier id="pub-id">urn:uuid:`, "<dc:title>The Tidewater Almanac</dc:title>", "<dc:language>en</dc:language>",
		"<dc:creator>Ines Marlow</dc:creator>", "<dc:creator>Odile Brandt</dc:creator>",
		"<dc:publisher>Saltmarsh &amp; Sons</dc:publisher>", "<dc:description>Tides &lt;for&gt; a year.</dc:description>",
		"<dc:date>1931</dc:date>", "<dc:subject>Tides</dc:subject>", "<dc:identifier>urn:isbn:9780000000002</dc:identifier>",
		`<meta property="dcterms:modified">2024-02-29T10:30:00Z</meta>`, `<meta name="cover" content="cover-image"/>`,
		`<meta name="calibre:series" content="Harbour Books"/>`, `<meta name="calibre:series_index" content="2"/>`,
		`properties="nav"`, `href="images/cover.png" media-type="image/png" properties="cover-image"`,
		`<itemref idref="cover"/>`, `<itemref idref="section0001"/>`, `<reference type="cover"`)
	contains(t, "navigation", files["OEBPS/nav.xhtml"], `<a href="section0001.xhtml">The Tidewater Almanac</a>`,
		`<a href="section0002.xhtml">Spring Tides</a>`, `<a href="section0003.xhtml">Neap Tides</a>`)
	contains(t, "NCX", files["OEBPS/toc.ncx"], `<text>Spring Tides</text></navLabel><content src="section0002.xhtml"/>`)

	outline, err := preview.ReadOutline(dst)
	if err != nil {
		t.Fatalf("the preview cannot open the book: %v", err)
	}
	if len(outline.Sections) != 4 || len(outline.Contents) != 3 || outline.Sections[2].Title != "Spring Tides" {
		t.Errorf("outline = %+v", outline)
	}
	read, readCover := meta.Read(dst, "book.epub")
	if read.Title != "The Tidewater Almanac" || len(read.Authors) != 2 || read.Series != "Harbour Books" ||
		read.ISBN != "9780000000002" || readCover == nil || readCover.Ext != "png" {
		t.Errorf("metadata read back = %+v, cover %v", read, readCover)
	}
}

// pngImage returns a small PNG file.
func pngImage() []byte {
	return []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x00\x00\x00\x00:~\x9bU" +
		"\x00\x00\x00\nIDATx\x9cc`\x00\x00\x00\x02\x00\x01H\xaf\xa4q\x00\x00\x00\x00IEND\xaeB`\x82")
}

// TestConvertIsRepeatable gives the same file for the same input, in
// both layouts a PDF can be stored in.
func TestConvertIsRepeatable(t *testing.T) {
	_, one := convert(t, sampleBook().Bytes(""), reflow.Options{})
	_, two := convert(t, sampleBook().Bytes(""), reflow.Options{})
	_, packed := convert(t, sampleBook().BytesCompressed(""), reflow.Options{})
	first, _ := os.ReadFile(one)
	second, _ := os.ReadFile(two)
	if !bytes.Equal(first, second) {
		t.Error("two conversions of the same file differ")
	}
	want := unpack(t, one)["OEBPS/section0002.xhtml"]
	if got := unpack(t, packed)["OEBPS/section0002.xhtml"]; got != want {
		t.Errorf("the compressed layout gives a different chapter:\n%s\nwant:\n%s", got, want)
	}
}

// TestMetadataFallback takes what the options leave out from the PDF and
// then from the file name.
func TestMetadataFallback(t *testing.T) {
	_, dst := convert(t, sampleBook().Bytes(""), reflow.Options{})
	contains(t, "package", unpack(t, dst)["OEBPS/content.opf"], "<dc:title>Almanac from the PDF</dc:title>",
		"<dc:language>en-GB</dc:language>", "<dc:creator>Ines Marlow</dc:creator>",
		"<dc:subject>tides</dc:subject>", "<dc:subject>harbours</dc:subject>")
	plain := sampleBook()
	plain.Info = 0
	_, dst = convert(t, plain.Bytes(""), reflow.Options{Metadata: meta.Metadata{Language: "Klingon"}})
	contains(t, "package", unpack(t, dst)["OEBPS/content.opf"], "<dc:title>tide tables</dc:title>",
		"<dc:language>en-GB</dc:language>", `<meta property="dcterms:modified">2000-01-01T00:00:00Z</meta>`)
}

// outlined builds a book whose chapters are only marked by bookmarks.
func outlined() *pdftest.Builder {
	b := pdftest.New()
	fonts := map[string]int{"F1": b.Font("Helvetica")}
	body := func(name string) string {
		return rows("F1", 10, 72, 700, 14, name+" opens with a sentence of its own.", "It goes on for a second line.") +
			rows("F1", 10, 72, 640, 14, "Soundings in "+name, "The lead line was cast every hour that night.")
	}
	outline, one, part, two := b.Reserve(), b.Reserve(), b.Reserve(), b.Reserve()
	b.Document([]pdftest.Page{
		{Content: rows("F1", 10, 72, 700, 14, "A preface that belongs to no chapter."), Fonts: fonts},
		{Content: body("Chapter one"), Fonts: fonts},
		{Content: body("Chapter two"), Fonts: fonts},
	}, fmt.Sprintf("/Outlines %d 0 R", outline))
	b.Set(outline, fmt.Sprintf("<< /First %d 0 R >>", one))
	b.Set(one, fmt.Sprintf("<< /Title (The First <Watch>) /Dest [%d 0 R /Fit] /First %d 0 R /Next %d 0 R >>", b.PageNums[1], part, two))
	b.Set(part, fmt.Sprintf("<< /Title (Soundings in Chapter one) /Dest [%d 0 R /XYZ 0 660 0] >>", b.PageNums[1]))
	b.Set(two, fmt.Sprintf("<< /Title (The Second Watch) /Dest [%d 0 R /XYZ 72 720 0] >>", b.PageNums[2]))
	return b
}

// TestOutlineSections splits a book by its bookmarks and links the
// nested ones to their place.
func TestOutlineSections(t *testing.T) {
	report, dst := convert(t, outlined().Bytes(""), reflow.Options{Metadata: meta.Metadata{Title: "Night Log"}})
	if report.Sections != 3 {
		t.Errorf("report = %+v", *report)
	}
	files := unpack(t, dst)
	checkXML(t, files)
	contains(t, "navigation", files["OEBPS/nav.xhtml"],
		`<li><a href="section0001.xhtml">Night Log</a></li>`,
		`<li><a href="section0002.xhtml">The First &lt;Watch&gt;</a>`, `<ol>`,
		`<li><a href="section0002.xhtml#a2">Soundings in Chapter one</a></li>`, `</ol>`, `</li>`,
		`<li><a href="section0003.xhtml">The Second Watch</a></li>`)
	contains(t, "preface", files["OEBPS/section0001.xhtml"], "<p>A preface that belongs to no chapter.</p>")
	contains(t, "first watch", files["OEBPS/section0002.xhtml"], "<title>The First &lt;Watch&gt;</title>",
		"<p>Chapter one opens with a sentence of its own. It goes on for a second line.</p>",
		`<p id="a2">Soundings in Chapter one</p>`, "<p>The lead line was cast every hour that night.</p>")
	if strings.Contains(files["OEBPS/section0002.xhtml"], "Chapter two") {
		t.Error("the first chapter holds text of the second")
	}
	contains(t, "NCX", files["OEBPS/toc.ncx"], `<content src="section0002.xhtml"/>`, `<content src="section0002.xhtml#a2"/>`,
		"</navPoint>\n</navPoint>", `<content src="section0003.xhtml"/>`)
}

// TestUnreadableDocuments reports scans and encrypted files with their
// own errors and leaves no file behind.
func TestUnreadableDocuments(t *testing.T) {
	scan := pdftest.New()
	var photo bytes.Buffer
	jpeg.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 60, 80)), nil)
	page := scan.Stream("/Type /XObject /Subtype /Image /Width 60 /Height 80 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode",
		photo.Bytes())
	scan.Document([]pdftest.Page{{Content: "q 612 0 0 792 0 0 cm /Im1 Do Q\n", XObjects: map[string]int{"Im1": page}}}, "")
	cases := map[string]struct {
		// data is the file to convert.
		data []byte
		// want is the error expected.
		want error
	}{
		"scan":      {scan.Bytes(""), reflow.ErrNoText},
		"encrypted": {sampleBook().Bytes("/Encrypt << /Filter /Standard >>"), reflow.ErrEncrypted},
	}
	for name, c := range cases {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "in.pdf"), filepath.Join(dir, "out.epub")
		os.WriteFile(src, c.data, 0o644)
		if _, err := reflow.Convert(context.Background(), src, dst, reflow.Options{}); !errors.Is(err, c.want) {
			t.Errorf("%s: error = %v, want %v", name, err, c.want)
		}
		if left, _ := os.ReadDir(dir); len(left) != 1 {
			t.Errorf("%s: the folder holds %d files after the failure, want only the source", name, len(left))
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.pdf")
	os.WriteFile(src, sampleBook().Bytes(""), 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reflow.Convert(ctx, src, filepath.Join(dir, "out.epub"), reflow.Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled conversion error = %v, want context.Canceled", err)
	}
	if _, err := reflow.Convert(context.Background(), filepath.Join(dir, "missing.pdf"), filepath.Join(dir, "out.epub"), reflow.Options{}); err == nil {
		t.Error("converting a missing file did not fail")
	}
}

// TestWarnings names what a conversion had to leave out.
func TestWarnings(t *testing.T) {
	b := pdftest.New()
	fonts := map[string]int{"F1": b.Font("Helvetica")}
	fonts["F2"] = b.Add("<< /Type /Font /Subtype /Type0 /BaseFont /ABCDEF+Mystery /Encoding /Identity-H /DescendantFonts [<< /Type /Font /Subtype /CIDFontType2 >>] >>")
	jpx := b.Stream("/Type /XObject /Subtype /Image /Width 40 /Height 20 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /JPXDecode", []byte("jp2"))
	content := rows("F1", 10, 72, 700, 14, "A line of text that can be read without any trouble at all.", "And a second one.") +
		"BT /F2 10 Tf 72 600 Td <00410042> Tj ET\n" +
		"q 200 0 0 100 72 400 cm /Im1 Do Q\nBI /W 1 /H 1 /BPC 8 /CS /G ID \x00 EI\n" +
		"BT /F1 10 Tf 0 1 -1 0 500 300 Tm (Sideways) Tj ET\n" + strings.Repeat("0 0 5 5 re f\n", 250)
	b.Document([]pdftest.Page{{Content: content, Fonts: fonts, XObjects: map[string]int{"Im1": jpx}}}, "")
	cover := filepath.Join(t.TempDir(), "cover.txt")
	os.WriteFile(cover, []byte("not an image"), 0o644)
	report, _ := convert(t, b.Bytes(""), reflow.Options{Cover: cover})
	want := []string{
		"The cover image is not a JPEG, PNG or GIF file and was left out.",
		"1 page holds vector drawings, such as charts, diagrams or table rules, which are not converted.",
		"Text set in the font Mystery could not be decoded and was left out.",
		"1 image was left out because of how it is stored (JPXDecode encoding).",
		"1 small inline image was left out.",
		"Text that is turned on its side was left out on 1 page.",
	}
	if strings.Join(report.Warnings, "\n") != strings.Join(want, "\n") {
		t.Errorf("warnings:\n%s\nwant:\n%s", strings.Join(report.Warnings, "\n"), strings.Join(want, "\n"))
	}
}

// manual builds a document in the style of a technical manual: a
// printed table of contents, numbered headings in bold, a numbered list
// with hanging indents and a paragraph that opens with a large initial.
func manual() *pdftest.Builder {
	b := pdftest.New()
	fonts := map[string]int{"F1": b.Font("Helvetica"), "F2": b.Font("Helvetica-Bold")}
	first := pdftest.Text("F2", 10, 72, 720, "Contents") +
		rows("F1", 10, 72, 690, 14, "1 Soundings . . . . . . . . . . . . . . . . . . 1", "1.1 The lead line . . . . . . . . . . . . . . 2") +
		pdftest.Text("F2", 10, 72, 620, "1 Soundings") +
		pdftest.Text("F1", 30, 72, 566, "T") +
		rows("F1", 10, 96, 580, 14, "he lead is armed with tallow so that it brings up", "a sample of the bottom with every cast.") +
		rows("F1", 10, 72, 552, 14, "Sand, shell and mud each tell the pilot where he is.") +
		pdftest.Text("F2", 10, 72, 500, "1.1 The lead line") +
		rows("F1", 10, 72, 470, 14, "1. Mark the line at two, three, five and seven fathoms") +
		rows("F1", 10, 86, 456, 14, "with strips of leather.") +
		rows("F1", 10, 72, 442, 14, "2. Coil it down clear before every cast.")
	b.Document([]pdftest.Page{{Content: first, Fonts: fonts}}, "")
	return b
}

// TestManualFeatures covers the constructs of the manual sample.
func TestManualFeatures(t *testing.T) {
	report, dst := convert(t, manual().Bytes(""), reflow.Options{Metadata: meta.Metadata{Title: "Pilotage"}})
	files := unpack(t, dst)
	checkXML(t, files)
	if report.Sections != 2 {
		t.Errorf("report = %+v", *report)
	}
	contains(t, "front", files["OEBPS/section0001.xhtml"],
		"<h3>Contents</h3>", "<p>1 Soundings</p>", "<p>1.1 The lead line</p>")
	contains(t, "chapter", files["OEBPS/section0002.xhtml"],
		"<h3>1 Soundings</h3>",
		"<p>The lead is armed with tallow so that it brings up a sample of the bottom with every cast. Sand, shell and mud each tell the pilot where he is.</p>",
		`<h3 id="a3">1.1 The lead line</h3>`,
		"<p>1. Mark the line at two, three, five and seven fathoms with strips of leather.</p>",
		"<p>2. Coil it down clear before every cast.</p>")
	contains(t, "navigation", files["OEBPS/nav.xhtml"], `<a href="section0001.xhtml">Contents</a>`,
		`<a href="section0002.xhtml">1 Soundings</a>`, `<a href="section0002.xhtml#a3">1.1 The lead line</a>`)
}

// TestDoubleSpacing keeps the lines of a paragraph together when the
// whole document is set with its lines far apart, as a typed manuscript
// is.
func TestDoubleSpacing(t *testing.T) {
	b := pdftest.New()
	fonts := map[string]int{"F1": b.Font("Helvetica")}
	content := rows("F1", 10, 72, 700, 28,
		"These lines are set twice as far apart as usual, which is the",
		"way a manuscript is typed, and they are still one paragraph",
		"of running text.") +
		rows("F1", 10, 72, 590, 28,
			"A second paragraph follows after a wider gap and is kept",
			"apart from the first.")
	b.Document([]pdftest.Page{{Content: content, Fonts: fonts}}, "")
	_, dst := convert(t, b.Bytes(""), reflow.Options{})
	contains(t, "manuscript", unpack(t, dst)["OEBPS/section0001.xhtml"],
		"<p>These lines are set twice as far apart as usual, which is the way a manuscript is typed, and they are still one paragraph of running text.</p>",
		"<p>A second paragraph follows after a wider gap and is kept apart from the first.</p>")
}
