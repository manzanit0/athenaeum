package azw3

import (
	"bytes"
	"context"
	"image"
	"path/filepath"
	"strings"
	"testing"

	typeface "golang.org/x/image/font"

	"github.com/manzanit0/athenaeum/internal/kf8"
	"github.com/manzanit0/athenaeum/internal/meta"
)

// coverless returns the sample EPUB without its cover.
func coverless(t *testing.T) string {
	t.Helper()
	doc := packageDoc
	for _, line := range []string{
		`<meta name="cover" content="cover-image"/>`,
		`<item id="cover-image" href="images/cover.png" media-type="image/png"/>`,
	} {
		if !strings.Contains(doc, line) {
			t.Fatalf("the sample package document has no %s", line)
		}
		doc = strings.Replace(doc, line, "", 1)
	}
	return sample(t, map[string]string{"OEBPS/content.opf": doc})
}

// decode returns the JPEG image in data, failing the test unless it
// starts the way Kindles expect.
func decode(t *testing.T, name string, data []byte) image.Image {
	t.Helper()
	if !bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF, 0xE0}) || !bytes.Contains(data[:12], []byte("JFIF")) {
		t.Fatalf("%s starts % x, want a JFIF JPEG", name, data[:12])
	}
	img, kind, err := image.Decode(bytes.NewReader(data))
	if err != nil || kind != "jpeg" {
		t.Fatalf("%s does not decode as a JPEG: %s, %v", name, kind, err)
	}
	return img
}

// ink returns the share of the pixels of img inside r that are dark.
func ink(img image.Image, r image.Rectangle) float64 {
	dark := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if red, green, blue, _ := img.At(x, y).RGBA(); red+green+blue < 3*0x8000 {
				dark++
			}
		}
	}
	return float64(dark) / float64(r.Dx()*r.Dy())
}

// TestTitleCover checks the cover drawn for a book that has none.
func TestTitleCover(t *testing.T) {
	data := titleCover("Firme en la  brecha\n", []string{"Thomas J. Olmsted"})
	img := decode(t, "the cover", data)
	if size := img.Bounds().Size(); size.X != coverWidth || size.Y != coverHeight {
		t.Fatalf("the cover is %v, want %d by %d", size, coverWidth, coverHeight)
	}
	text := img.Bounds().Inset(coverMargin)
	if got := ink(img, text); got < 0.01 || got > 0.2 {
		t.Errorf("%.1f%% of the cover is ink, want some text on a white page", 100*got)
	}
	inside := frameInset + frameWidth + 2
	if got := ink(img, image.Rect(inside, inside, coverMargin-2, coverHeight-inside)); got != 0 {
		t.Errorf("%.1f%% of the margin is ink, want the text kept out of it", 100*got)
	}
	if !bytes.Equal(data, titleCover("Firme en la brecha", []string{"Thomas J. Olmsted"})) {
		t.Error("white space in the title changes the cover")
	}
	if bytes.Equal(data, titleCover("Firme en la brecha", nil)) {
		t.Error("the authors are not on the cover")
	}
	for _, title := range []string{"", "   ", "東京物語"} {
		if titleCover(title, []string{"An Author"}) != nil {
			t.Errorf("a cover was drawn for the title %q, which cannot be written", title)
		}
	}
	if titleCover("Tides", []string{"東京"}) == nil {
		t.Error("no cover was drawn for a book whose authors cannot be written")
	}
}

// TestCoverLayout checks how the text of a generated cover is fitted.
func TestCoverLayout(t *testing.T) {
	bold, _ := coverFonts()
	style := textStyle{font: bold, largest: 136, smallest: 64, step: 8, maxLines: 6}
	width := coverWidth - 2*coverMargin
	short, err := style.fit("Tides", width)
	if err != nil || len(short.lines) != 1 || short.lineHeight != 136*6/5 {
		t.Errorf("a short title is set as %q with lines %d apart: %v", short.lines, short.lineHeight, err)
	}
	medium, _ := style.fit("An Almanac of the Tides of the Northern Seas", width)
	if got := strings.Join(medium.lines, " "); got != "An Almanac of the Tides of the Northern Seas" || len(medium.lines) < 2 {
		t.Errorf("a longer title is set as %q, want it wrapped between words", medium.lines)
	}
	word, _ := style.fit("Donaudampfschifffahrtsgesellschaftskapitänsmütze", width)
	if got := strings.Join(word.lines, ""); got != "Donaudampfschifffahrtsgesellschaftskapitänsmütze" || len(word.lines) != 2 || word.lineHeight != 64*6/5 {
		t.Errorf("a word wider than the cover is set as %q with lines %d apart", word.lines, word.lineHeight)
	}
	long, _ := style.fit(strings.Repeat("tides and currents ", 60), width)
	if len(long.lines) != 6 || !strings.HasSuffix(long.lines[5], ellipsis) {
		t.Errorf("a title too long for the cover is set as %q, want six lines ending in an ellipsis", long.lines)
	}
	for _, block := range []textBlock{short, medium, word, long} {
		for _, line := range block.lines {
			if got := typeface.MeasureString(block.face, line).Ceil(); got > width {
				t.Errorf("the line %q is %d wide, over the %d of the cover", line, got, width)
			}
		}
	}
	if got := drawable(bold, " Ύμνος \t& 東京 Война\n"); got != "Ύμνος & Война" {
		t.Errorf("drawable = %q", got)
	}
}

// TestGeneratedCover checks that a book without a cover is written with
// a generated one and its thumbnail, and that a book with a cover keeps
// its own.
func TestGeneratedCover(t *testing.T) {
	ctx := context.Background()
	book, report, err := build(ctx, coverless(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Images != 1 || len(book.Resources) != 3 || book.Cover != 1 || book.Thumbnail != 2 {
		t.Fatalf("book has %d content images, %d resources, cover %d and thumbnail %d, want 1, 3, 1 and 2",
			report.Images, len(book.Resources), book.Cover, book.Thumbnail)
	}
	if !bytes.Equal(book.Resources[book.Cover], titleCover("The Tidewater Almanac", []string{"Ines Marlow"})) {
		t.Error("the cover is not the one drawn from the title and the author of the EPUB")
	}
	thumb := decode(t, "the thumbnail", book.Resources[book.Thumbnail])
	if size := thumb.Bounds().Size(); size.X > thumbWidth || size.Y != thumbHeight {
		t.Errorf("the thumbnail is %v, want it to fill a box of %d by %d", size, thumbWidth, thumbHeight)
	}
	if got := ink(thumb, thumb.Bounds()); got < 0.01 {
		t.Errorf("%.1f%% of the thumbnail is ink, want the title to show", 100*got)
	}

	out := filepath.Join(t.TempDir(), "book.azw3")
	opts := Options{Metadata: meta.Metadata{Title: "Tides, Revised", Authors: []string{"I. Marlow", "A. Editor"}}}
	if _, err := Convert(ctx, coverless(t), out, opts); err != nil {
		t.Fatal(err)
	}
	want := titleCover("Tides, Revised", []string{"I. Marlow", "A. Editor"})
	if _, read := meta.Read(out, "book.azw3"); read == nil || !bytes.Equal(read.Data, want) {
		t.Error("the cover of the file is not the one drawn from the details given in the options")
	}
	written, err := kf8.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if written.Cover != 1 || written.Thumbnail != 2 {
		t.Errorf("the file says its cover is resource %d and its thumbnail %d, want 1 and 2", written.Cover, written.Thumbnail)
	}
	if data, err := written.Resource(written.Thumbnail); err != nil || len(data) == 0 {
		t.Errorf("the thumbnail of the file cannot be read: %v", err)
	} else {
		decode(t, "the thumbnail of the file", data)
	}

	kept, _, err := build(ctx, sample(t, nil), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(kept.Resources[kept.Cover], []byte{0xFF, 0xD8}) {
		t.Error("a book with a cover of its own got a generated one")
	}
}
