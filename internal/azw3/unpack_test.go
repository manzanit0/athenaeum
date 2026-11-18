package azw3

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/kf8"
	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/preview"
)

// unpacked is an EPUB made by Unpack, read back for inspection.
type unpacked struct {
	// path is where the EPUB was written.
	path string
	// report is what Unpack returned.
	report *Report
	// files maps the names of the files of the EPUB to their content.
	files map[string]string
	// outline is the reading order and table of contents.
	outline *preview.Outline
}

// unpack converts the Kindle book at src to an EPUB and reads it back.
func unpack(t *testing.T, src string, opts Options) *unpacked {
	t.Helper()
	u := &unpacked{path: filepath.Join(t.TempDir(), "book.epub"), files: map[string]string{}}
	var err error
	if u.report, err = Unpack(context.Background(), src, u.path, opts); err != nil {
		t.Fatalf("Unpack(%s): %v", src, err)
	}
	zr, err := zip.OpenReader(u.path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(r)
		r.Close()
		u.files[f.Name] = string(data)
	}
	if u.outline, err = preview.ReadOutline(u.path); err != nil {
		t.Fatalf("the EPUB does not open: %v", err)
	}
	return u
}

// documents returns the documents of the EPUB in reading order.
func (u *unpacked) documents() []string {
	var out []string
	for _, section := range u.outline.Sections {
		out = append(out, u.files[section.Href])
	}
	return out
}

// hrefPattern matches the address of a link.
var hrefPattern = regexp.MustCompile(`href="([^"]*)"`)

// check fails the test unless the EPUB is sound: its documents are well
// formed, nothing of the Kindle format is left and every link between
// documents leads to an element that exists.
func (u *unpacked) check(t *testing.T) {
	t.Helper()
	for name, content := range u.files {
		if strings.Contains(content, "kindle:") || strings.Contains(content, " aid=") {
			t.Errorf("%s still has Kindle addresses or identifiers", name)
		}
		if strings.HasSuffix(name, ".xhtml") || strings.HasSuffix(name, ".opf") || strings.HasSuffix(name, ".ncx") {
			wellFormed(t, name, []byte(content))
		}
	}
	for _, section := range u.outline.Sections {
		for _, match := range hrefPattern.FindAllStringSubmatch(u.files[section.Href], -1) {
			u.follow(t, section.Href, match[1])
		}
	}
	for _, entry := range u.outline.Contents {
		u.follow(t, "OEBPS/nav.xhtml", strings.TrimPrefix(entry.Href, "OEBPS/"))
	}
}

// follow fails the test unless the address found in the file from leads
// to a file of the EPUB and, when it names one, an element in it.
func (u *unpacked) follow(t *testing.T, from, address string) string {
	t.Helper()
	if strings.Contains(address, "://") || strings.HasPrefix(address, "mailto:") {
		return ""
	}
	name, fragment, _ := strings.Cut(address, "#")
	target := from
	if name != "" {
		target = filepath.ToSlash(filepath.Join(filepath.Dir(from), name))
	}
	content, ok := u.files[target]
	if !ok {
		t.Errorf("%s links to %s, which is not in the EPUB", from, address)
		return ""
	}
	if fragment != "" && !strings.Contains(content, `id="`+fragment+`"`) {
		t.Errorf("%s links to %s, but %s has no such element", from, address, target)
	}
	return target
}

// TestUnpackRoundTrip converts the sample EPUB to AZW3 and back and
// checks that the book survives.
func TestUnpackRoundTrip(t *testing.T) {
	azw := filepath.Join(t.TempDir(), "almanac.azw3")
	if _, err := Convert(context.Background(), sample(t, nil), azw, Options{}); err != nil {
		t.Fatal(err)
	}
	u := unpack(t, azw, Options{})
	u.check(t)
	docs := u.documents()
	if len(docs) != 3 || u.report.Files != 3 || len(u.report.Warnings) != 0 {
		t.Fatalf("EPUB has %d documents; report %+v", len(docs), u.report)
	}
	if n := strings.Count(docs[1], "<p id="); n != paragraphs {
		t.Errorf("long chapter has %d paragraphs, want %d", n, paragraphs)
	}
	for _, want := range []string{
		`<a href="part0001.xhtml#p300">three hundredth</a>`, `<a href="part0002.xhtml">last chapter</a>`,
		`<a href="#top" epub:type="noteref">the top</a>`, `href="https://example.com/tides?a=1&amp;b=2"`,
		`<img src="../images/image0001.png" alt="The moon"/>`, `href="../styles/style0001.css"`,
		`<body class="chapter" id="start">`, "Café &amp; crème",
	} {
		if !strings.Contains(docs[0], want) {
			t.Errorf("first chapter lacks %s", want)
		}
	}
	if css := u.files["OEBPS/styles/style0001.css"]; !strings.Contains(css, "url(../images/image0001.png) no-repeat") {
		t.Errorf("stylesheet = %s", css)
	}
	var lines []string
	for _, entry := range u.outline.Contents {
		lines = append(lines, strings.Repeat(">", entry.Level)+entry.Title+" "+entry.Href)
	}
	want := "Spring Tides OEBPS/text/part0000.xhtml#top|Neap Tides OEBPS/text/part0001.xhtml|" +
		">The Three Hundredth OEBPS/text/part0001.xhtml#p300|Storm Surge OEBPS/text/part0002.xhtml"
	if got := strings.Join(lines, "|"); got != want {
		t.Errorf("contents = %s", got)
	}
	m, cover := meta.Read(u.path, "book.epub")
	if m.Title != "The Tidewater Almanac" || m.Authors[0] != "Ines Marlow" || m.Publisher != "Saltmarsh Press" || m.Language != "en" {
		t.Errorf("metadata = %+v", m)
	}
	if cover == nil || cover.Ext != "png" {
		t.Errorf("cover = %v, want the PNG cover of the book", cover)
	}
	if _, ok := u.files["OEBPS/images/image0003.jpg"]; ok {
		t.Error("the cover thumbnail was written as an image")
	}
}

// TestUnpackCalibre reads books written by calibre 8.5 from the same
// EPUB: an AZW3, and a MOBI file carrying both the old format and KF8.
func TestUnpackCalibre(t *testing.T) {
	for _, name := range []string{"calibre.azw3", "joint.mobi"} {
		u := unpack(t, filepath.Join("testdata", name), Options{})
		u.check(t)
		text := strings.Join(u.documents(), "\n")
		if n := strings.Count(text, "Paragraph "); n != 400 {
			t.Errorf("%s: %d paragraphs, want 400", name, n)
		}
		for _, want := range []string{"three hundredth paragraph</a>", "naïve café 日本語", "Café &amp; crème", "<img src=\"../images/"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: text lacks %q", name, want)
			}
		}
		link := regexp.MustCompile(`<a href="([^"]+)"[^>]*>three hundredth paragraph</a>`).FindStringSubmatch(text)
		if link == nil || !strings.HasSuffix(link[1], "#p300") {
			t.Errorf("%s: the link to paragraph 300 is %v", name, link)
		}
		var titles []string
		for _, entry := range u.outline.Contents {
			titles = append(titles, strings.Repeat(">", entry.Level)+entry.Title)
		}
		got := strings.Join(titles, "|")
		if got != "Spring Tides|Neap Tides|>The Hundredth Paragraph|>The Three Hundredth|Storm Surge" {
			t.Errorf("%s: contents = %s", name, got)
		}
		if !strings.HasSuffix(u.outline.Contents[3].Href, "#p300") {
			t.Errorf("%s: nested contents line leads to %s", name, u.outline.Contents[3].Href)
		}
		m, cover := meta.Read(u.path, "book.epub")
		if m.Title != "The Tidewater Almanac" || len(m.Authors) != 1 || cover == nil {
			t.Errorf("%s: metadata = %+v, cover %v", name, m, cover != nil)
		}
		if len(u.report.Warnings) != 0 {
			t.Errorf("%s: warnings = %q", name, u.report.Warnings)
		}
	}
}

// TestUnpackRefusals checks the Kindle files that cannot be unpacked.
func TestUnpackRefusals(t *testing.T) {
	out := filepath.Join(t.TempDir(), "book.epub")
	if _, err := Unpack(context.Background(), filepath.Join("testdata", "old.mobi"), out, Options{}); !errors.Is(err, kf8.ErrNotKF8) {
		t.Errorf("Unpack of an old MOBI file = %v, want ErrNotKF8", err)
	}
	if _, err := Unpack(context.Background(), sample(t, nil), out, Options{}); !errors.Is(err, kf8.ErrNotKF8) {
		t.Errorf("Unpack of an EPUB = %v, want ErrNotKF8", err)
	}
}

// TestFont checks that scrambled and compressed fonts are restored.
func TestFont(t *testing.T) {
	plain := append([]byte("OTTO"), bytes.Repeat([]byte("glyphs"), 300)...)
	var packed bytes.Buffer
	zw := zlib.NewWriter(&packed)
	zw.Write(plain)
	zw.Close()
	key := []byte{0x5A, 0xA5, 0x3C}
	body := bytes.Clone(packed.Bytes())
	for i := range min(len(body), 1040) {
		body[i] ^= key[i%len(key)]
	}
	record := append([]byte("FONT"), 0, 0, 0, 0, 0, 0, 0, 3, 0, 0, 0, 27, 0, 0, 0, 3, 0, 0, 0, 24)
	record = append(append(record, key...), body...)
	got, ok := font(record)
	if !ok || !bytes.Equal(got, plain) {
		t.Errorf("font read back %d bytes, ok %v; want %d", len(got), ok, len(plain))
	}
	if _, ok := font([]byte("FONT")); ok {
		t.Error("font accepted a record that is cut short")
	}
}
