package restore

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"

	// The decoders of the images a page is stored as.
	_ "image/png"

	"github.com/manzanit0/athenaeum/internal/pdf"
)

// Limits of what a page is taken for and how it is shown to the model.
const (
	// minLetters is the least number of letters and digits a page with
	// a text layer holds.
	minLetters = 20
	// scanShare is the share of the page an image covers when the page
	// is a scan.
	scanShare = 0.5
	// maxImageBytes is the largest image sent as it is stored.
	maxImageBytes = 3 << 20
	// maxImageEdge is the longest edge, in pixels, of an image sent as
	// it is stored.
	maxImageEdge = 3000
	// sentImageEdge is the longest edge of an image that is made
	// smaller before it is sent.
	sentImageEdge = 2000
	// lineGap is how far apart, in font sizes, two lines stand when
	// the page leaves a gap between them.
	lineGap = 1.6
	// largeText is how much larger than the ordinary text of its page a
	// line is set when its size is pointed out to the model.
	largeText = 1.15
)

// ErrEncrypted is returned for encrypted PDFs.
var ErrEncrypted = errors.New("the PDF is encrypted")

// kind says what a page holds.
type kind int

// The kinds of pages.
const (
	// blankPage is a page with nothing to read.
	blankPage kind = iota
	// textPage is a page with a text layer.
	textPage
	// scanPage is a page that is a picture of text.
	scanPage
	// unreadablePage is a scan stored in a way that cannot be shown.
	unreadablePage
)

// Survey counts the pages of a book by what they hold.
type Survey struct {
	// Pages is the number of pages.
	Pages int `json:"pages"`
	// Scans is the number of pages that are pictures of text.
	Scans int `json:"scans"`
	// Text is the number of pages with a text layer.
	Text int `json:"text"`
	// Blank is the number of pages with nothing to read.
	Blank int `json:"blank"`
	// Unreadable is the number of scans stored in a way that cannot be
	// shown to the model.
	Unreadable int `json:"unreadable"`
}

// source is the PDF being restored. The PDF reader is not made for use
// from several goroutines, so every method takes the lock.
type source struct {
	// mu guards doc.
	mu sync.Mutex
	// doc is the open PDF.
	doc *pdf.Document
	// kinds holds what every page was found to be.
	kinds []kind
	// body is the font size most of the text of the book is set in.
	body float64
}

// openSource opens the PDF at path and finds out what its pages hold.
func openSource(path string) (*source, error) {
	doc, err := pdf.Open(path)
	if errors.Is(err, pdf.ErrEncrypted) {
		return nil, ErrEncrypted
	}
	if err != nil {
		return nil, err
	}
	s := &source{doc: doc}
	if err := s.classify(); err != nil {
		doc.Close()
		return nil, err
	}
	return s, nil
}

// close releases the PDF.
func (s *source) close() error {
	return s.doc.Close()
}

// classify finds out what every page holds. A failure deep inside a
// damaged file becomes an error.
func (s *source) classify() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the PDF is damaged and could not be read: %v", r)
		}
	}()
	s.kinds = make([]kind, s.doc.NumPages())
	sizes := map[float64]int{}
	for i := range s.kinds {
		content := s.doc.Page(i).Content()
		s.kinds[i], _ = classifyPage(content)
		for _, run := range content.Runs {
			sizes[run.Size] += len(strings.TrimSpace(run.Text))
		}
	}
	s.body = commonest(sizes)
	return nil
}

// classifyPage says what a page with the given content holds and, for a
// scan, which of its images is the page.
func classifyPage(c *pdf.Content) (kind, *pdf.Image) {
	letters := 0
	for _, run := range c.Runs {
		for _, r := range run.Text {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				letters++
			}
		}
	}
	if letters >= minLetters {
		return textPage, nil
	}
	var largest *pdf.Placement
	for i := range c.Images {
		p := &c.Images[i]
		if largest == nil || p.Width*p.Height > largest.Width*largest.Height {
			largest = p
		}
	}
	if largest == nil || largest.Width*largest.Height < scanShare*c.Width*c.Height {
		return blankPage, nil
	}
	if !largest.Image.Encodable() {
		return unreadablePage, nil
	}
	return scanPage, largest.Image
}

// survey counts the pages by what they hold.
func (s *source) survey() Survey {
	out := Survey{Pages: len(s.kinds)}
	for _, k := range s.kinds {
		switch k {
		case scanPage:
			out.Scans++
		case textPage:
			out.Text++
		case unreadablePage:
			out.Unreadable++
		default:
			out.Blank++
		}
	}
	return out
}

// info returns what the PDF says about itself.
func (s *source) info() pdf.Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.doc.Info()
}

// shown is a page as it is handed to the model.
type shown struct {
	// kind says what the page holds.
	kind kind
	// text holds the lines of a page with a text layer.
	text string
	// image holds the picture of a scan.
	image []byte
	// mime is the media type of image.
	mime string
}

// show returns the page with the given number, counted from one, the
// way the model gets to see it.
func (s *source) show(number int) (out shown, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			out, err = shown{}, fmt.Errorf("page %d is damaged and could not be read: %v", number, r)
		}
	}()
	content := s.doc.Page(number - 1).Content()
	k, img := classifyPage(content)
	out.kind = k
	switch k {
	case textPage:
		out.text = pageText(content.Runs, s.body)
	case scanPage:
		if out.image, out.mime, err = pageImage(img); err != nil {
			out, err = shown{kind: unreadablePage}, nil
		}
	}
	return out, err
}

// line is the text drawn along one baseline of a page.
type line struct {
	// y is the vertical position of the baseline.
	y float64
	// size is the largest font size on the line.
	size float64
	// runs holds the stretches of text, in any order.
	runs []pdf.Run
}

// spaces matches white space of any kind and length.
var spaces = regexp.MustCompile(`[\s\p{Zs}]+`)

// mark returns the mark that text in the given style is wrapped in:
// one asterisk for italics, two for bold, three for both.
func mark(bold, italic bool) string {
	switch {
	case bold && italic:
		return "***"
	case bold:
		return "**"
	case italic:
		return "*"
	}
	return ""
}

// styled returns the text of a line with its bold and italic stretches
// marked. A space comes between two runs that stand apart, and the
// spaces at the ends of a marked stretch are kept outside its marks.
func styled(runs []pdf.Run) string {
	var sb, part strings.Builder
	current := ""
	flush := func() {
		text := part.String()
		part.Reset()
		inner := strings.TrimSpace(text)
		if inner == "" || current == "" {
			sb.WriteString(text)
			return
		}
		lead := text[:strings.Index(text, inner)]
		sb.WriteString(lead + current + inner + current + text[len(lead)+len(inner):])
	}
	end := 0.0
	for i, run := range runs {
		if m := mark(run.Bold, run.Italic); m != current {
			flush()
			current = m
		}
		if i > 0 && run.X-end > run.Size*0.15 {
			part.WriteByte(' ')
		}
		part.WriteString(run.Text)
		end = run.X + run.Width
	}
	flush()
	return strings.TrimSpace(spaces.ReplaceAllString(sb.String(), " "))
}

// commonest returns the font size that most of the text counted in
// sizes is set in, the smaller one of two that are level.
func commonest(sizes map[float64]int) float64 {
	body, most := 0.0, 0
	for size, n := range sizes {
		if n > most || n == most && size < body {
			body, most = size, n
		}
	}
	return body
}

// pageText lays the runs of a page out as lines of text, top to bottom.
// Bold and italic stretches are marked the way the model is to mark
// them. A line set larger than body, the size of the ordinary text of
// the book, starts with a note of its size, which is what the model tells titles and
// headings by, and an empty line stands where the page leaves a gap
// between lines, which is what it tells groups and paragraphs by. White
// space of any kind and length comes out as one space.
func pageText(runs []pdf.Run, body float64) string {
	var lines []*line
	for _, run := range runs {
		if !run.Upright || strings.TrimSpace(spaces.ReplaceAllString(run.Text, " ")) == "" {
			continue
		}
		var on *line
		for _, l := range lines {
			if diff := l.y - run.Y; diff < run.Size/2 && diff > -run.Size/2 {
				on = l
				break
			}
		}
		if on == nil {
			on = &line{y: run.Y}
			lines = append(lines, on)
		}
		on.runs = append(on.runs, run)
		on.size = max(on.size, run.Size)
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].y < lines[j].y })
	var sb strings.Builder
	for i, l := range lines {
		sort.SliceStable(l.runs, func(i, j int) bool { return l.runs[i].X < l.runs[j].X })
		if i > 0 && l.y-lines[i-1].y > lineGap*max(l.size, lines[i-1].size) {
			sb.WriteByte('\n')
		}
		if l.size > body*largeText {
			fmt.Fprintf(&sb, "(size %.0f) ", l.size)
		}
		sb.WriteString(styled(l.runs))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// pageImage returns the picture of a scanned page as a JPEG or PNG file
// with its media type. A picture larger than a model makes use of is
// made smaller first.
func pageImage(im *pdf.Image) ([]byte, string, error) {
	data, ext, err := im.Encode()
	if err != nil {
		return nil, "", err
	}
	mime := "image/jpeg"
	if ext == "png" {
		mime = "image/png"
	}
	if len(data) <= maxImageBytes && im.Width <= maxImageEdge && im.Height <= maxImageEdge {
		return data, mime, nil
	}
	full, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, shrink(full, sentImageEdge), &jpeg.Options{Quality: 80}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/jpeg", nil
}

// shrink returns img scaled down so that its longest edge is edge
// pixels, averaging the pixels that fall together so thin strokes of
// text survive. An image that is small enough comes back as it is.
func shrink(img image.Image, edge int) image.Image {
	b := img.Bounds()
	longest := max(b.Dx(), b.Dy())
	if longest <= edge {
		return img
	}
	w, h := max(b.Dx()*edge/longest, 1), max(b.Dy()*edge/longest, 1)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+max((y+1)*b.Dy()/h, y*b.Dy()/h+1)
		for x := range w {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+max((x+1)*b.Dx()/w, x*b.Dx()/w+1)
			var r, g, bl, n uint32
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					pr, pg, pb, _ := img.At(sx, sy).RGBA()
					r, g, bl, n = r+pr>>8, g+pg>>8, bl+pb>>8, n+1
				}
			}
			at := out.PixOffset(x, y)
			out.Pix[at], out.Pix[at+1], out.Pix[at+2], out.Pix[at+3] = uint8(r/n), uint8(g/n), uint8(bl/n), 255
		}
	}
	return out
}
