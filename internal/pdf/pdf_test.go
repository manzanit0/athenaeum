package pdf_test

import (
	"bytes"
	"compress/lzw"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/pdf"
	"github.com/manzanit0/athenaeum/internal/pdf/pdftest"
)

// open parses data and fails the test when that does not work.
func open(t *testing.T, data []byte) *pdf.Document {
	t.Helper()
	doc, err := pdf.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

// texts returns the text of the runs of the page with the given index.
func texts(doc *pdf.Document, page int) []string {
	var out []string
	for _, run := range doc.Page(page).Content().Runs {
		out = append(out, run.Text)
	}
	return out
}

// near reports whether a and b differ by less than half a point.
func near(a, b float64) bool {
	return math.Abs(a-b) < 0.5
}

// twoFonts builds a one page document set in two fonts.
func twoFonts() *pdftest.Builder {
	b := pdftest.New()
	regular, bold := b.Font("Helvetica"), b.Font("Times-BoldItalic")
	content := pdftest.Text("F1", 24, 72, 700, "A Title") +
		"BT /F2 10 Tf 72 650 Td [(Ker) -20 (ned) -300 (words)] TJ 0 -14 Td (next \\(line\\)) Tj ET\n"
	b.Document([]pdftest.Page{{Content: content, Fonts: map[string]int{"F1": regular, "F2": bold}}}, "")
	return b
}

// TestRuns reads positioned text set in two fonts.
func TestRuns(t *testing.T) {
	doc := open(t, twoFonts().Bytes(""))
	defer doc.Close()
	if doc.NumPages() != 1 {
		t.Fatalf("NumPages = %d, want 1", doc.NumPages())
	}
	content := doc.Page(0).Content()
	if content.Width != 612 || content.Height != 792 {
		t.Errorf("page size = %g by %g", content.Width, content.Height)
	}
	want := []string{"A Title", "Kerned", "words", "next (line)"}
	if got := texts(doc, 0); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("runs = %q, want %q", got, want)
	}
	title, kerned, words, next := content.Runs[0], content.Runs[1], content.Runs[2], content.Runs[3]
	if !near(title.X, 72) || !near(title.Y, 92) || !near(title.Size, 24) || title.Bold || !title.Upright {
		t.Errorf("title run = %+v", title)
	}
	if title.Font != "Helvetica" || kerned.Font != "Times-BoldItalic" || !kerned.Bold || !kerned.Italic {
		t.Errorf("fonts = %q, %q (bold %v, italic %v)", title.Font, kerned.Font, kerned.Bold, kerned.Italic)
	}
	if gap := words.X - (kerned.X + kerned.Width); gap < 2.5 || gap > 3.5 {
		t.Errorf("gap between runs = %g, want about 3", gap)
	}
	if !near(next.Y, kerned.Y+14) || !near(next.X, 72) {
		t.Errorf("second line at %g, %g; first at %g, %g", next.X, next.Y, kerned.X, kerned.Y)
	}
}

// TestCompressedLayout reads a file that keeps its objects in an object
// stream and its cross references in a stream.
func TestCompressedLayout(t *testing.T) {
	doc := open(t, twoFonts().BytesCompressed(""))
	defer doc.Close()
	if got := strings.Join(texts(doc, 0), "|"); got != "A Title|Kerned|words|next (line)" {
		t.Errorf("runs = %q", got)
	}
}

// TestBrokenCrossReferences reads files whose cross reference table
// cannot be used.
func TestBrokenCrossReferences(t *testing.T) {
	plain := twoFonts().Bytes("")
	damaged := map[string][]byte{
		"wrong startxref":    bytes.Replace(plain, []byte("startxref\n"), []byte("startxref\n1"), 1),
		"no startxref":       plain[:bytes.LastIndex(plain, []byte("startxref"))],
		"shifted offsets":    bytes.Replace(plain, []byte("%PDF-1.4\n"), []byte("%PDF-1.4\n% padding\n"), 1),
		"compressed, broken": bytes.Replace(twoFonts().BytesCompressed(""), []byte("startxref\n"), []byte("startxref\n9"), 1),
	}
	for name, data := range damaged {
		doc, err := pdf.Parse(data)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := strings.Join(texts(doc, 0), "|"); got != "A Title|Kerned|words|next (line)" {
			t.Errorf("%s: runs = %q", name, got)
		}
	}
}

// TestIncrementalUpdate prefers the objects of the newest section of a
// file that was saved more than once.
func TestIncrementalUpdate(t *testing.T) {
	b := pdftest.New()
	font := b.Font("Helvetica")
	b.Document([]pdftest.Page{{Content: pdftest.Text("F1", 12, 72, 700, "old"), Fonts: map[string]int{"F1": font}}}, "")
	data := b.Bytes("")
	var previous int
	fmt.Sscanf(string(data[bytes.LastIndex(data, []byte("startxref"))+10:]), "%d", &previous)
	content := []byte(pdftest.Text("F1", 12, 72, 700, "new"))
	var buf bytes.Buffer
	buf.Write(data)
	offset := buf.Len()
	fmt.Fprintf(&buf, "3 0 obj\n<< /Length %d >>\nstream\n%s\nendstream\nendobj\n", len(content), content)
	start := buf.Len()
	fmt.Fprintf(&buf, "xref\n3 1\n%010d 00000 n \ntrailer\n<< /Size 6 /Root %d 0 R /Prev %d >>\nstartxref\n%d\n%%%%EOF\n",
		offset, b.Root, previous, start)
	doc := open(t, buf.Bytes())
	if got := strings.Join(texts(doc, 0), "|"); got != "new" {
		t.Errorf("runs = %q, want the updated content", got)
	}
}

// TestDifferencesEncoding decodes a simple font whose encoding renames
// some codes and declares widths.
func TestDifferencesEncoding(t *testing.T) {
	b := pdftest.New()
	font := b.Add(`<< /Type /Font /Subtype /TrueType /BaseFont /ABCDEF+Garamond-Italic /FirstChar 1 /LastChar 3
		/Widths [500 600 1000] /Encoding << /BaseEncoding /MacRomanEncoding
		/Differences [1 /eacute /f_i 3 /uni20AC] >> >>`)
	content := "BT /F1 10 Tf 100 100 Td (caf\\001 \\002n \\003 \\212) Tj ET\n"
	b.Document([]pdftest.Page{{Content: content, Fonts: map[string]int{"F1": font}}}, "")
	doc := open(t, b.Bytes(""))
	runs := doc.Page(0).Content().Runs
	if len(runs) != 1 || runs[0].Text != "café fin € ä" {
		t.Fatalf("runs = %+v", runs)
	}
	if runs[0].Font != "Garamond-Italic" || !runs[0].Italic {
		t.Errorf("font = %q, italic %v", runs[0].Font, runs[0].Italic)
	}
}

// TestCompositeFont decodes two byte codes through a ToUnicode map.
func TestCompositeFont(t *testing.T) {
	b := pdftest.New()
	toUnicode := b.Flate("", []byte(`/CIDInit /ProcSet findresource begin begincmap
1 begincodespacerange <0000> <FFFF> endcodespacerange
2 beginbfchar <0003> <0020> <0010> <00660069> endbfchar
2 beginbfrange <0024> <0026> <0041> <0030> <0031> [<00E9> <D83DDE00>] endbfrange
endcmap`))
	descendant := b.Add("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /AAAAAA+Noto /DW 500 /W [3 [250] 36 38 700] >>")
	font := b.Add(fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /AAAAAA+Noto /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>",
		descendant, toUnicode))
	content := "BT /F1 10 Tf 50 500 Td <0024002500260003001000300031> Tj <0099> Tj ET\n"
	b.Document([]pdftest.Page{{Content: content, Fonts: map[string]int{"F1": font}}}, "")
	doc := open(t, b.Bytes(""))
	page := doc.Page(0).Content()
	if len(page.Runs) != 1 || page.Runs[0].Text != "ABC fié😀" {
		t.Fatalf("runs = %+v", page.Runs)
	}
	if want := (3*700 + 250 + 3*500) / 100.0; !near(page.Runs[0].Width, want) {
		t.Errorf("width = %g, want %g", page.Runs[0].Width, want)
	}
	if len(page.Undecoded) != 1 || page.Undecoded[0] != "Noto" {
		t.Errorf("undecoded fonts = %q, want the one with an unmapped code", page.Undecoded)
	}
}

// TestFilters decodes streams stored with each supported filter.
func TestFilters(t *testing.T) {
	text := pdftest.Text("F1", 12, 72, 700, "filtered")
	var packed bytes.Buffer
	w := lzw.NewWriter(&packed, lzw.MSB, 8)
	w.Write([]byte(text))
	w.Close()
	var hex strings.Builder
	for _, c := range []byte(text) {
		fmt.Fprintf(&hex, "%02x ", c)
	}
	runs := []byte{byte(len(text) - 1)}
	runs = append(append(runs, text...), 128)
	streams := map[string][]byte{
		"/Filter /ASCIIHexDecode":                []byte(hex.String() + ">"),
		"/Filter /LZWDecode":                     packed.Bytes(),
		"/Filter /RunLengthDecode":               runs,
		"/Filter [/ASCIIHexDecode /FlateDecode]": []byte(fmt.Sprintf("%x>", pdftest.Deflate([]byte(text)))),
		"/Filter /ASCII85Decode":                 []byte("<~" + ascii85(text) + "~>"),
		"/Filter /FlateDecode /DecodeParms << /Predictor 12 /Columns 4 >>": pdftest.Deflate(pngUp([]byte(text), 4)),
	}
	for entries, data := range streams {
		b := pdftest.New()
		font := b.Font("Helvetica")
		contents := b.Stream(entries, data)
		tree := b.Reserve()
		page := b.Add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /Contents [%d 0 R] >>", tree, contents))
		b.Set(tree, fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 /MediaBox [0 0 612 792] /Resources << /Font << /F1 %d 0 R >> >> >>",
			page, font))
		b.Root = b.Add(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", tree))
		doc := open(t, b.Bytes(""))
		if got := strings.Join(texts(doc, 0), "|"); got != "filtered" {
			t.Errorf("%s: runs = %q", entries, got)
		}
	}
}

// ascii85 encodes s in ASCII base 85 without the framing.
func ascii85(s string) string {
	var out strings.Builder
	data := []byte(s)
	for len(data) > 0 {
		var chunk [4]byte
		n := copy(chunk[:], data)
		data = data[n:]
		v := uint32(chunk[0])<<24 | uint32(chunk[1])<<16 | uint32(chunk[2])<<8 | uint32(chunk[3])
		var digits [5]byte
		for i := 4; i >= 0; i-- {
			digits[i] = byte(v%85) + '!'
			v /= 85
		}
		out.Write(digits[:n+1])
	}
	return out.String()
}

// pngUp applies the PNG "up" row filter to data for rows of the given
// width, padding the last row.
func pngUp(data []byte, columns int) []byte {
	for len(data)%columns != 0 {
		data = append(data, ' ')
	}
	var out []byte
	prev := make([]byte, columns)
	for start := 0; start < len(data); start += columns {
		row := data[start : start+columns]
		out = append(out, 2)
		for i, c := range row {
			out = append(out, c-prev[i])
		}
		prev = row
	}
	return out
}

// imagePage builds a one page document that draws the image described
// by entries and data, 200 by 100 points with its lower left corner at
// 50, 600.
func imagePage(b *pdftest.Builder, entries string, data []byte) {
	img := b.Stream("/Type /XObject /Subtype /Image "+entries, data)
	content := "q 200 0 0 100 50 600 cm /Im1 Do Q\nBI /W 1 /H 1 /BPC 8 /CS /G ID \x00 EI\n0 0 10 10 re f\n"
	b.Document([]pdftest.Page{{Content: content, XObjects: map[string]int{"Im1": img}}}, "")
}

// TestImages extracts images in the supported encodings.
func TestImages(t *testing.T) {
	var photo bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 8, 4))
	jpeg.Encode(&photo, picture, nil)
	b := pdftest.New()
	imagePage(b, "/Width 8 /Height 4 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode", photo.Bytes())
	doc := open(t, b.Bytes(""))
	content := doc.Page(0).Content()
	if len(content.Images) != 1 || content.Inline != 1 || content.Paths != 1 {
		t.Fatalf("images %d, inline %d, paths %d", len(content.Images), content.Inline, content.Paths)
	}
	placed := content.Images[0]
	if !near(placed.X, 50) || !near(placed.Y, 92) || !near(placed.Width, 200) || !near(placed.Height, 100) {
		t.Errorf("placement = %+v", placed)
	}
	data, ext, err := placed.Image.Encode()
	if err != nil || ext != "jpg" || !bytes.Equal(data, photo.Bytes()) {
		t.Errorf("JPEG image: ext %q, %d bytes, %v", ext, len(data), err)
	}

	b = pdftest.New()
	mask := b.Flate("/Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8", []byte{255, 64})
	imagePage(b, fmt.Sprintf("/Width 2 /Height 1 /ColorSpace /DeviceRGB /BitsPerComponent 8 /SMask %d 0 R /Filter /FlateDecode", mask),
		pdftest.Deflate([]byte{255, 0, 0, 0, 0, 255}))
	checkPixels(t, open(t, b.Bytes("")), color.NRGBA{R: 255, A: 255}, color.NRGBA{B: 255, A: 64})

	b = pdftest.New()
	imagePage(b, "/Width 2 /Height 1 /ColorSpace [/Indexed /DeviceRGB 1 <00FF00 102030>] /BitsPerComponent 1", []byte{0b01000000})
	checkPixels(t, open(t, b.Bytes("")), color.NRGBA{G: 255, A: 255}, color.NRGBA{R: 0x10, G: 0x20, B: 0x30, A: 255})

	b = pdftest.New()
	imagePage(b, "/Width 2 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 1 /Decode [1 0]", []byte{0b10000000})
	checkPixels(t, open(t, b.Bytes("")), color.NRGBA{A: 255}, color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	b = pdftest.New()
	imagePage(b, "/Width 2 /Height 1 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /JPXDecode", []byte("jp2"))
	doc = open(t, b.Bytes(""))
	if _, _, err := doc.Page(0).Content().Images[0].Image.Encode(); !errors.Is(err, pdf.ErrUnsupportedImage) {
		t.Errorf("JPX image error = %v, want ErrUnsupportedImage", err)
	}
}

// checkPixels compares the two pixels of the only image on the first
// page of doc with the colours given.
func checkPixels(t *testing.T, doc *pdf.Document, left, right color.NRGBA) {
	t.Helper()
	data, ext, err := doc.Page(0).Content().Images[0].Image.Encode()
	if err != nil || ext != "png" {
		t.Fatalf("Encode: ext %q, %v", ext, err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for x, want := range []color.NRGBA{left, right} {
		if got := color.NRGBAModel.Convert(img.At(x, 0)); got != want {
			t.Errorf("pixel %d = %v, want %v", x, got, want)
		}
	}
}

// TestOutlineAndInfo reads bookmarks, named destinations and the
// information dictionary.
func TestOutlineAndInfo(t *testing.T) {
	b := pdftest.New()
	font := b.Font("Helvetica")
	outline := b.Reserve()
	first, child, second := b.Reserve(), b.Reserve(), b.Reserve()
	pages := []pdftest.Page{
		{Content: pdftest.Text("F1", 12, 72, 700, "one"), Fonts: map[string]int{"F1": font}},
		{Content: pdftest.Text("F1", 12, 72, 700, "two"), Fonts: map[string]int{"F1": font}},
	}
	dests := b.Reserve()
	b.Document(pages, fmt.Sprintf("/Lang (en-GB) /Outlines %d 0 R /Names << /Dests %d 0 R >>", outline, dests))
	b.Set(dests, fmt.Sprintf("<< /Kids [<< /Names [(ch2) [%d 0 R /FitH 500]] >>] >>", b.PageNums[1]))
	b.Set(outline, fmt.Sprintf("<< /Type /Outlines /First %d 0 R /Last %d 0 R >>", first, second))
	b.Set(first, fmt.Sprintf("<< /Title (Chapter \\(1\\)) /Dest [%d 0 R /XYZ 0 792 0] /First %d 0 R /Next %d 0 R >>",
		b.PageNums[0], child, second))
	b.Set(child, fmt.Sprintf("<< /Title <FEFF00530065006300740069006F006E00A000E9> /A << /S /GoTo /D [%d 0 R /Fit] >> >>", b.PageNums[0]))
	b.Set(second, "<< /Title (Chapter 2) /Dest (ch2) >>")
	b.Info = b.Add("<< /Title <FEFF004C00650073002000430068006F007300650073> /Author (Georges Perec) /Keywords (a, b) /CreationDate (D:19650901120000Z) >>")
	doc := open(t, b.Bytes(""))
	info := doc.Info()
	if info.Title != "Les Choses" || info.Author != "Georges Perec" || info.Language != "en-GB" || info.Created != "D:19650901120000Z" {
		t.Errorf("info = %+v", info)
	}
	want := []pdf.Bookmark{
		{Title: "Chapter (1)", Level: 0, Page: 0, Top: 0},
		{Title: "Section é", Level: 1, Page: 0, Top: -1},
		{Title: "Chapter 2", Level: 0, Page: 1, Top: 292},
	}
	if got := doc.Outline(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("outline = %+v, want %+v", got, want)
	}
}

// TestEncryptedAndInvalid rejects files that cannot be read.
func TestEncryptedAndInvalid(t *testing.T) {
	b := twoFonts()
	if _, err := pdf.Parse(b.Bytes("/Encrypt << /Filter /Standard /V 1 >>")); !errors.Is(err, pdf.ErrEncrypted) {
		t.Errorf("encrypted file error = %v, want ErrEncrypted", err)
	}
	for _, data := range []string{"", "plain text", "%PDF-1.4\nnothing else"} {
		if _, err := pdf.Parse([]byte(data)); !errors.Is(err, pdf.ErrNotPDF) {
			t.Errorf("Parse(%q) error = %v, want ErrNotPDF", data, err)
		}
	}
}

// TestRotationAndForms reads text on a rotated page and inside a form
// XObject.
func TestRotationAndForms(t *testing.T) {
	b := pdftest.New()
	font := b.Font("Helvetica")
	form := b.Flate(fmt.Sprintf("/Type /XObject /Subtype /Form /BBox [0 0 100 100] /Matrix [1 0 0 1 10 20] /Resources << /Font << /FX %d 0 R >> >>", font),
		[]byte(pdftest.Text("FX", 10, 0, 0, "inside")))
	content := pdftest.Text("F1", 12, 100, 300, "turned") + "q 2 0 0 2 0 0 cm /Fm1 Do Q\n" +
		"BT /F1 12 Tf 0 1 -1 0 300 300 Tm (sideways) Tj ET\n"
	b.Document([]pdftest.Page{{Content: content, Fonts: map[string]int{"F1": font}, XObjects: map[string]int{"Fm1": form},
		Width: 400, Height: 600, Rotate: 90}}, "")
	doc := open(t, b.Bytes(""))
	page := doc.Page(0).Content()
	if page.Width != 600 || page.Height != 400 || len(page.Runs) != 3 {
		t.Fatalf("page %g by %g with runs %+v", page.Width, page.Height, page.Runs)
	}
	turned, inside, sideways := page.Runs[0], page.Runs[1], page.Runs[2]
	if !near(turned.X, 300) || !near(turned.Y, 100) || turned.Upright {
		t.Errorf("text on a rotated page = %+v", turned)
	}
	if inside.Text != "inside" || !near(inside.Size, 20) || !near(inside.X, 40) || !near(inside.Y, 20) {
		t.Errorf("text in a form = %+v", inside)
	}
	if !sideways.Upright || !near(sideways.X, 300) || !near(sideways.Y, 300) {
		t.Errorf("text turned against the page = %+v", sideways)
	}
}

// TestSpacing separates characters that are set far apart with the
// character spacing operator, and drops a space that the following text
// is set on top of.
func TestSpacing(t *testing.T) {
	b := pdftest.New()
	font := b.Font("Helvetica")
	content := "BT /F1 10 Tf 100 700 Td 3 Tc (ab) Tj 0 Tc (c) Tj ET\n" +
		"BT /F1 10 Tf 100 650 Td 0.5 Tc (tight) Tj ET\n" +
		"BT /F1 10 Tf 100 600 Td 0 Tc (inv ) Tj 13 0 Td (oke) Tj ET\n" +
		"BT /F1 10 Tf 100 550 Td (two ) Tj 30 0 Td (words) Tj ET\n"
	b.Document([]pdftest.Page{{Content: content, Fonts: map[string]int{"F1": font}}}, "")
	doc := open(t, b.Bytes(""))
	if got := strings.Join(texts(doc, 0), "|"); got != "a|b|c|tight|inv|oke|two |words" {
		t.Fatalf("runs = %q", got)
	}
	runs := doc.Page(0).Content().Runs
	if gap := runs[1].X - (runs[0].X + runs[0].Width); !near(gap, 3) {
		t.Errorf("gap after a widely spaced character = %g, want 3", gap)
	}
	if overlap := runs[5].X - (runs[4].X + runs[4].Width); overlap > 0.1 || overlap < -1 {
		t.Errorf("text after an overprinted space starts %g after the run before it", overlap)
	}
}

// compactFont returns a CFF font program with five glyphs: .notdef, A,
// two glyphs with names of their own and the fi ligature. Its built in
// encoding gives them the codes 0x41, 1, 2 and 3, and the code 4 to the
// bullet.
func compactFont() []byte {
	names := "f_iuni20AC"
	number := func(v int) []byte { return []byte{29, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)} }
	head := []byte{1, 0, 4, 1}
	head = append(head, 0, 1, 1, 1, 5, 'T', 'e', 's', 't')
	const dictLength = 18
	rest := []byte{0, 2, 1, 1, 4, byte(1 + len(names))}
	rest = append(rest, names...)
	rest = append(rest, 0, 0)
	charset := len(head) + 5 + dictLength + len(rest)
	encoding := charset + 9
	glyphs := encoding + 10
	dict := append(number(charset), 15)
	dict = append(append(dict, number(encoding)...), 16)
	dict = append(append(dict, number(glyphs)...), 17)
	out := append(head, 0, 1, 1, 1, byte(1+len(dict)))
	out = append(append(out, dict...), rest...)
	out = append(out, 0, 0, 34, 1, 135, 1, 136, 0, 109)
	out = append(out, 0x80, 4, 0x41, 1, 2, 3, 1, 4, 0, 116)
	return append(out, 0, 5, 1, 1, 2, 3, 4, 5, 6, 14, 14, 14, 14, 14)
}

// TestBuiltInEncodings decodes fonts that name no encoding with the one
// built into the font program, or into the typeface.
func TestBuiltInEncodings(t *testing.T) {
	b := pdftest.New()
	compact := b.Stream("/Subtype /Type1C", compactFont())
	clear := "%!PS-AdobeFont-1.0\n/Encoding 256 array\n0 1 255 {1 index exch /.notdef put} for\n" +
		"dup 65 /B put\ndup 66 /germandbls put\nreadonly def\ncurrentfile eexec\n"
	program := b.Stream(fmt.Sprintf("/Length1 %d", len(clear)), []byte(clear+"dup 65 /Z put (in the binary part)"))
	font := func(base, file string, stream int) int {
		descriptor := b.Add(fmt.Sprintf("<< /Type /FontDescriptor /FontName /%s /Flags 4 /%s %d 0 R >>", base, file, stream))
		return b.Add(fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /%s /FontDescriptor %d 0 R >>", base, descriptor))
	}
	fonts := map[string]int{
		"F1": font("AAAAAA+Compact", "FontFile3", compact),
		"F2": font("BBBBBB+Classic", "FontFile", program),
		"F3": b.Add("<< /Type /Font /Subtype /Type1 /BaseFont /Symbol >>"),
		"F4": b.Add("<< /Type /Font /Subtype /Type1 /BaseFont /Times-Roman >>"),
	}
	content := "BT /F1 10 Tf 50 700 Td (A\\001\\002\\003\\004) Tj ET\nBT /F2 10 Tf 50 650 Td (AB) Tj ET\n" +
		"BT /F3 10 Tf 50 600 Td (abc\\267) Tj ET\nBT /F4 10 Tf 50 550 Td (it's \\256ne) Tj ET\n"
	b.Document([]pdftest.Page{{Content: content, Fonts: fonts}}, "")
	doc := open(t, b.Bytes(""))
	if got := strings.Join(texts(doc, 0), "|"); got != "Afi€fi•|Bß|αβχ•|it’s fine" {
		t.Errorf("runs = %q", got)
	}
	if undecoded := doc.Page(0).Content().Undecoded; len(undecoded) != 0 {
		t.Errorf("undecoded fonts = %q", undecoded)
	}
}

// exercise reads everything this package can read from data. It reports
// a panic as a test failure and ignores errors, which damaged files are
// allowed to produce.
func exercise(t *testing.T, name string, data []byte) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: panic: %v", name, r)
		}
	}()
	doc, err := pdf.Parse(data)
	if err != nil {
		return
	}
	doc.Info()
	doc.Outline()
	for i := range doc.NumPages() {
		for _, placed := range doc.Page(i).Content().Images {
			placed.Image.Encode()
		}
	}
}

// TestDamagedFiles reads files with bytes changed, removed and cut off,
// none of which may make the reader panic or hang.
func TestDamagedFiles(t *testing.T) {
	b := pdftest.New()
	mask := b.Flate("/Type /XObject /Subtype /Image /Width 2 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8", []byte{255, 64})
	imagePage(b, fmt.Sprintf("/Width 2 /Height 1 /ColorSpace [/Indexed /DeviceRGB 1 <00FF00102030>] /BitsPerComponent 8 /SMask %d 0 R", mask),
		[]byte{0, 1})
	originals := [][]byte{twoFonts().Bytes(""), twoFonts().BytesCompressed(""), b.Bytes("")}
	random := rand.New(rand.NewPCG(1, 2))
	for n, original := range originals {
		for i := range 400 {
			data := bytes.Clone(original)
			switch i % 4 {
			case 0:
				for range 1 + random.IntN(8) {
					data[random.IntN(len(data))] = byte(random.IntN(256))
				}
			case 1:
				data = data[:random.IntN(len(data))]
			case 2:
				at := random.IntN(len(data))
				data = append(data[:at], data[min(len(data), at+1+random.IntN(40)):]...)
			case 3:
				at := random.IntN(len(data))
				data = append(data[:at], append([]byte(" 99999999999 0 R << [ ( "), data[at:]...)...)
			}
			exercise(t, fmt.Sprintf("file %d, change %d", n, i), data)
		}
	}
}
