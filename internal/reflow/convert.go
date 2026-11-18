// Package reflow turns PDF files into reflowable EPUB books. A PDF only
// says where each character sits on a page, so the package rebuilds the
// reading order, paragraphs, headings and lists from those positions,
// leaves out running headers, footers and page numbers, and writes the
// result as an EPUB.
package reflow

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/pdf"
)

// ErrNoText is returned for PDFs without text that can be read: scans,
// whose pages are pictures, and files whose fonts do not say which
// characters their glyphs stand for. Recognising text in images is not
// supported.
var ErrNoText = errors.New("the PDF has no text that can be read")

// ErrEncrypted is returned for encrypted PDFs.
var ErrEncrypted = errors.New("the PDF is encrypted")

// Limits of a conversion.
const (
	// minCharacters is the least number of letters and digits per page,
	// on average, a document with a text layer has.
	minCharacters = 20
	// minFigure is the smallest extent, in points, of an image that is
	// kept.
	minFigure = 24
	// backgroundShare is the share of the page an image covers when it
	// is a page background rather than a figure.
	backgroundShare = 0.8
	// backgroundText is how many characters a page holds when an image
	// that covers it is a background.
	backgroundText = 50
	// repeatedFigure is how many pages show the same image when it is
	// decoration rather than content.
	repeatedFigure = 3
	// drawingPaths is how many shapes a page paints when it holds a
	// drawing.
	drawingPaths = 200
	// defaultModified is the modification time of books whose PDF does
	// not carry one.
	defaultModified = "2000-01-01T00:00:00Z"
)

// languageTag matches the language tags EPUB accepts.
var languageTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// pdfDate matches the dates of the PDF information dictionary.
var pdfDate = regexp.MustCompile(`^D:(\d{4})(\d{2})?(\d{2})?(\d{2})?(\d{2})?(\d{2})?`)

// Options controls a conversion.
type Options struct {
	// Metadata overrides what the PDF declares. Zero fields fall back
	// to the information dictionary of the PDF, then to its file name.
	Metadata meta.Metadata
	// Cover is the path of a cover image to embed; empty means none.
	Cover string
}

// Report says what a conversion did and what it had to leave out.
type Report struct {
	// Pages is the number of pages read.
	Pages int `json:"pages"`
	// Sections is the number of sections the book is divided into.
	Sections int `json:"sections"`
	// Images is the number of images placed in the book.
	Images int `json:"images"`
	// Warnings lists what could not be converted.
	Warnings []string `json:"warnings"`
}

// conversion holds the state of one conversion.
type conversion struct {
	// doc is the PDF being read.
	doc *pdf.Document
	// out receives the files of the EPUB.
	out *archive
	// pages holds the lines of the pages laid out so far.
	pages []layout
	// waiting holds the runs of the pages read but not yet laid out,
	// which have to wait until the pages after them are read so their
	// running headers and footers can be told from their text.
	waiting [][]pdf.Run
	// sizes holds the width and height of every page read.
	sizes [][2]float64
	// margins holds the text in the margins of every page read.
	margins [][]segment
	// figures holds the images of every page.
	figures [][]element
	// names maps image identifiers to file names; images that cannot be
	// converted map to the empty string.
	names map[int]string
	// uses counts the pages every image file is shown on.
	uses map[string]int
	// images lists the image files written.
	images []asset
	// characters counts the letters and digits read.
	characters int
	// notes counts what was left out, by kind.
	notes map[string]int
}

// Convert turns the PDF at src into an EPUB at dst.
func Convert(ctx context.Context, src, dst string, opts Options) (*Report, error) {
	doc, err := pdf.Open(src)
	if errors.Is(err, pdf.ErrEncrypted) {
		return nil, ErrEncrypted
	}
	if err != nil {
		return nil, err
	}
	defer doc.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".reflow-*.epub")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	c := &conversion{doc: doc, out: newArchive(tmp), names: map[int]string{}, uses: map[string]int{}}
	c.notes = map[string]int{}
	report, err := c.attempt(ctx, src, opts)
	if closeErr := c.out.close(); err == nil {
		err = closeErr
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err != nil {
		return nil, err
	}
	return report, os.Rename(tmp.Name(), dst)
}

// attempt runs the conversion and turns a failure deep inside a damaged
// file into an error, so one bad PDF cannot take the program down.
func (c *conversion) attempt(ctx context.Context, src string, opts Options) (report *Report, err error) {
	defer func() {
		if r := recover(); r != nil {
			report, err = nil, fmt.Errorf("the PDF is damaged and could not be read: %v", r)
		}
	}()
	return c.run(ctx, src, opts)
}

// run reads the pages, rebuilds the document and writes the book.
func (c *conversion) run(ctx context.Context, src string, opts Options) (*Report, error) {
	count := c.doc.NumPages()
	for i := range count {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		c.readPage(i)
		c.layOut(i - neighbourPages)
	}
	c.layOut(count - 1)
	if count == 0 || c.characters < minCharacters*count {
		return nil, c.noText()
	}
	st := measure(c.pages)
	elements := c.placeFigures(st.mergeFlows(st.elements(c.pages)))
	b, err := c.describe(src, opts)
	if err != nil {
		return nil, err
	}
	marks := outlineMarks(elements, c.doc.Outline())
	if len(marks) == 0 {
		marks = headingMarks(elements)
	}
	if len(marks) == 0 || tooFine(marks, count) {
		marks = pageMarks(elements)
	}
	b.sections, b.entries = split(elements, marks, b.meta.Title)
	b.images = c.images
	b.write(c.out)
	report := &Report{Pages: count, Sections: len(b.sections), Warnings: c.warnings()}
	for _, e := range elements {
		if e.kind == figure {
			report.Images++
		}
	}
	return report, nil
}

// noText explains why the document has no text that can be read.
func (c *conversion) noText() error {
	var fonts []string
	for key := range c.notes {
		if name, found := strings.CutPrefix(key, "font "); found {
			fonts = append(fonts, name)
		}
	}
	if len(fonts) == 0 {
		return fmt.Errorf("%w: its pages look like scans, and recognising text in images is not supported", ErrNoText)
	}
	sort.Strings(fonts)
	return fmt.Errorf("%w: its fonts (%s) do not say which characters they hold", ErrNoText, strings.Join(fonts, ", "))
}

// readPage reads the text and the images of the page with index i.
func (c *conversion) readPage(i int) {
	content := c.doc.Page(i).Content()
	runs, turned := usable(content)
	text := 0
	for _, run := range runs {
		for _, r := range run.Text {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				text++
			}
		}
	}
	c.characters += text
	c.waiting = append(c.waiting, runs)
	c.sizes = append(c.sizes, [2]float64{content.Width, content.Height})
	c.margins = append(c.margins, marginSegments(runs, content.Height))
	c.figures = append(c.figures, c.readImages(i, content, text))
	if turned > 0 {
		c.notes["turned"]++
	}
	if content.Paths >= drawingPaths {
		c.notes["drawing"]++
	}
	c.notes["inline"] += content.Inline
	for _, name := range content.Undecoded {
		c.notes["font "+name] = 1
	}
}

// layOut arranges the text of the pages up to the one with index last
// into lines, after taking out their headers, footers and page numbers.
func (c *conversion) layOut(last int) {
	for i := len(c.pages); i <= last && i < len(c.waiting); i++ {
		runs := withoutFurniture(c.waiting[i], i, c.margins)
		c.waiting[i] = nil
		c.pages = append(c.pages, layout{blocks: arrange(runs, i), width: c.sizes[i][0], height: c.sizes[i][1]})
	}
}

// readImages writes the images of a page to the book and returns them
// as figures. text is the number of characters on the page.
func (c *conversion) readImages(page int, content *pdf.Content, text int) []element {
	var figures []element
	shown := map[string]bool{}
	for _, placed := range content.Images {
		if placed.Width < minFigure || placed.Height < minFigure {
			continue
		}
		covers := placed.Width * placed.Height / (content.Width * content.Height)
		if covers >= backgroundShare && text >= backgroundText {
			c.notes["background"]++
			continue
		}
		name := c.imageFile(placed.Image)
		if name == "" || shown[name] {
			continue
		}
		shown[name] = true
		c.uses[name]++
		figures = append(figures, element{
			kind: figure, image: name, page: page, y: placed.Y, x0: placed.X, x1: placed.X + placed.Width,
		})
	}
	return figures
}

// imageFile returns the name of the file holding the image, writing it
// to the book on first use. It returns the empty string for images that
// cannot be converted.
func (c *conversion) imageFile(image *pdf.Image) string {
	if name, known := c.names[image.ID]; known {
		return name
	}
	data, ext, err := image.Encode()
	if err != nil {
		reason := strings.TrimPrefix(err.Error(), pdf.ErrUnsupportedImage.Error()+": ")
		c.notes["image "+reason]++
		c.names[image.ID] = ""
		return ""
	}
	name := fmt.Sprintf("images/image%04d.%s", len(c.images)+1, ext)
	c.out.add(root+name, data, zip.Store)
	c.images = append(c.images, asset{name: name, ext: ext})
	c.names[image.ID] = name
	return name
}

// placeFigures puts the figures of every page between the elements:
// before the first element of the page that lies below the figure in
// the same column, or after the last element of the page. Images shown
// on many pages are decoration and are left out.
func (c *conversion) placeFigures(elements []element) []element {
	limit := max(repeatedFigure, len(c.pages)/3)
	out := make([]element, 0, len(elements))
	page := 0
	flush := func(upTo int, before *element) {
		for ; page <= upTo && page < len(c.figures); page++ {
			kept := c.figures[page][:0]
			for _, f := range c.figures[page] {
				switch {
				case c.uses[f.image] >= limit:
					c.notes["repeated"] = 1
				case before != nil && page == upTo && !(f.y < before.y && f.x0 < before.x1 && before.x0 < f.x1):
					kept = append(kept, f)
				default:
					out = append(out, f)
				}
			}
			c.figures[page] = kept
			if page == upTo && before != nil {
				return
			}
		}
	}
	for i := range elements {
		e := &elements[i]
		flush(e.page-1, nil)
		flush(e.page, e)
		out = append(out, *e)
	}
	flush(len(c.figures)-1, nil)
	return out
}

// describe gathers the metadata of the book.
func (c *conversion) describe(src string, opts Options) (*book, error) {
	info := c.doc.Info()
	b := &book{meta: opts.Metadata, modified: defaultModified, language: "und"}
	if strings.TrimSpace(b.meta.Title) == "" {
		b.meta.Title = strings.TrimSpace(info.Title)
	}
	if b.meta.Title == "" {
		b.meta.Title = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	if len(b.meta.Authors) == 0 && strings.TrimSpace(info.Author) != "" {
		b.meta.Authors = []string{strings.TrimSpace(info.Author)}
	}
	if len(b.meta.Tags) == 0 {
		separator := func(r rune) bool { return r == ',' || r == ';' }
		b.meta.Tags = meta.CleanList(strings.FieldsFunc(info.Keywords, separator))
	}
	for _, candidate := range []string{b.meta.Language, info.Language} {
		if languageTag.MatchString(strings.TrimSpace(candidate)) {
			b.language = strings.TrimSpace(candidate)
			break
		}
	}
	for _, candidate := range []string{info.Modified, info.Created} {
		if m := pdfDate.FindStringSubmatch(candidate); m != nil {
			b.modified = timestamp(m)
			break
		}
	}
	id, err := identifier(src)
	if err != nil {
		return nil, err
	}
	b.identifier = id
	b.cover = c.cover(opts.Cover)
	return b, nil
}

// timestamp formats the parts of a PDF date as an EPUB timestamp.
func timestamp(parts []string) string {
	fill := func(value, fallback string) string {
		if value == "" {
			return fallback
		}
		return value
	}
	return fmt.Sprintf("%s-%s-%sT%s:%s:%sZ", parts[1], fill(parts[2], "01"), fill(parts[3], "01"),
		fill(parts[4], "00"), fill(parts[5], "00"), fill(parts[6], "00"))
}

// identifier derives a stable identifier for the book from the content
// of the PDF, so converting the same file twice gives the same book.
func identifier(src string) (string, error) {
	f, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sum := h.Sum(nil)
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16]), nil
}

// cover adds the cover image at path to the book. It returns nil when
// there is no cover or it cannot be used.
func (c *conversion) cover(path string) *asset {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	ext := meta.ImageExt(data)
	if err != nil || mediaTypes[ext] == "" {
		c.notes["cover"] = 1
		return nil
	}
	cover := &asset{name: "images/cover." + ext, ext: ext}
	c.out.add(root+cover.name, data, zip.Store)
	return cover
}

// plural returns one when n is one and many otherwise.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// warnings lists what the conversion left out, in a stable order.
func (c *conversion) warnings() []string {
	keys := make([]string, 0, len(c.notes))
	for key, n := range c.notes {
		if n > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := []string{}
	for _, key := range keys {
		out = append(out, c.warning(key, c.notes[key]))
	}
	return out
}

// warning words the note with the given key and count.
func (c *conversion) warning(key string, n int) string {
	kind, detail, _ := strings.Cut(key, " ")
	switch kind {
	case "background":
		return fmt.Sprintf("%d page-sized background %s left out.", n, plural(n, "image was", "images were"))
	case "cover":
		return "The cover image is not a JPEG, PNG or GIF file and was left out."
	case "drawing":
		return fmt.Sprintf("%d %s vector drawings, such as charts, diagrams or table rules, "+
			"which are not converted.", n, plural(n, "page holds", "pages hold"))
	case "font":
		return fmt.Sprintf("Text set in the font %s could not be decoded and was left out.", detail)
	case "image":
		return fmt.Sprintf("%d %s left out because of how %s stored (%s).", n,
			plural(n, "image was", "images were"), plural(n, "it is", "they are"), detail)
	case "inline":
		return fmt.Sprintf("%d small inline %s left out.", n, plural(n, "image was", "images were"))
	case "repeated":
		return "Images repeated on many pages, such as logos, were left out."
	}
	return fmt.Sprintf("Text that is turned on its side was left out on %d %s.", n, plural(n, "page", "pages"))
}
