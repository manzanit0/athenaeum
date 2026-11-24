package azw3

import (
	"image"
	"image/draw"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	typeface "golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// Layout of a generated cover, in pixels.
const (
	// coverWidth is the width of a generated cover.
	coverWidth = 1200
	// coverHeight is the height of a generated cover, which gives it the
	// proportions Amazon recommends for covers.
	coverHeight = 1920
	// coverMargin is the room left on both sides of the text.
	coverMargin = 120
	// frameInset is the distance of the frame from the edges.
	frameInset = 48
	// frameWidth is the thickness of the frame.
	frameWidth = 6
	// ruleWidth is the length of the rule between title and authors.
	ruleWidth = 240
	// ruleHeight is the thickness of that rule.
	ruleHeight = 6
	// ruleGap is the room left above and below the rule.
	ruleGap = 72
	// ellipsis ends a text that had to be cut short.
	ellipsis = "…"
)

// textStyle says how a piece of text of a generated cover is set.
type textStyle struct {
	// font is the typeface.
	font *sfnt.Font
	// largest is the size tried first.
	largest int
	// smallest is the size below which the text is cut instead.
	smallest int
	// step is how much the size goes down between tries.
	step int
	// maxLines is the most lines the text may take.
	maxLines int
}

// textBlock is a piece of text laid out as centred lines.
type textBlock struct {
	// face is the typeface at the size of the block.
	face typeface.Face
	// lines are the lines of the block.
	lines []string
	// lineHeight is the distance between two baselines.
	lineHeight int
}

// coverFonts returns the typefaces of generated covers: a bold one for
// the title and a regular one for the authors. They are nil when the
// fonts built into the program cannot be read.
var coverFonts = sync.OnceValues(func() (*sfnt.Font, *sfnt.Font) {
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, nil
	}
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, nil
	}
	return bold, regular
})

// drawable returns s with its white space collapsed and without the
// characters f has no glyph for.
func drawable(f *sfnt.Font, s string) string {
	var buf sfnt.Buffer
	kept := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if glyph, err := f.GlyphIndex(&buf, r); err != nil || glyph == 0 {
			return -1
		}
		return r
	}, s)
	return strings.Join(strings.Fields(kept), " ")
}

// fitting returns the length in bytes of the longest start of s that is
// no wider than limit, and of its first character when even that is.
func fitting(face typeface.Face, s string, limit fixed.Int26_6) int {
	cut := 0
	for i, r := range s {
		end := i + utf8.RuneLen(r)
		if i > 0 && typeface.MeasureString(face, s[:end]) > limit {
			break
		}
		cut = end
	}
	return cut
}

// wrap breaks text into lines no wider than width. A word wider than a
// whole line is broken wherever it has to be.
func wrap(face typeface.Face, text string, width int) []string {
	limit := fixed.I(width)
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if joined := strings.TrimSpace(line + " " + word); typeface.MeasureString(face, joined) <= limit {
			line = joined
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
		for line = word; typeface.MeasureString(face, line) > limit; {
			cut := fitting(face, line, limit)
			lines, line = append(lines, line[:cut]), line[cut:]
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// clip returns at most the given number of lines, ending the last one
// with an ellipsis when lines were dropped.
func clip(face typeface.Face, lines []string, most, width int) []string {
	if len(lines) <= most {
		return lines
	}
	last := lines[most-1]
	for last != "" && typeface.MeasureString(face, last+ellipsis) > fixed.I(width) {
		_, size := utf8.DecodeLastRuneInString(last)
		last = strings.TrimSpace(last[:len(last)-size])
	}
	return append(lines[:most-1:most-1], last+ellipsis)
}

// fit lays text out in lines no wider than width, in the largest size
// that takes no more lines than allowed and breaks no word, which shows
// as the lines holding more words than the text. Text that does not fit
// even in the smallest size is broken and cut short.
func (s textStyle) fit(text string, width int) (textBlock, error) {
	words := len(strings.Fields(text))
	for size := s.largest; ; size -= s.step {
		face, err := opentype.NewFace(s.font, &opentype.FaceOptions{Size: float64(size), DPI: 72})
		if err != nil {
			return textBlock{}, err
		}
		lines := wrap(face, text, width)
		block := textBlock{face: face, lineHeight: size * 6 / 5}
		if size <= s.smallest {
			block.lines = clip(face, lines, s.maxLines, width)
			return block, nil
		}
		if len(lines) <= s.maxLines && len(strings.Fields(strings.Join(lines, " "))) == words {
			block.lines = lines
			return block, nil
		}
	}
}

// height returns the height of the block.
func (b textBlock) height() int {
	return len(b.lines) * b.lineHeight
}

// draw writes the block on dst, centred, with its first line starting
// at top.
func (b textBlock) draw(dst draw.Image, top int) {
	metrics := b.face.Metrics()
	baseline := (fixed.I(b.lineHeight) + metrics.Ascent - metrics.Descent) / 2
	d := typeface.Drawer{Dst: dst, Src: image.Black, Face: b.face}
	for i, line := range b.lines {
		d.Dot = fixed.Point26_6{
			X: (fixed.I(dst.Bounds().Dx()) - d.MeasureString(line)) / 2,
			Y: fixed.I(top+i*b.lineHeight) + baseline,
		}
		d.DrawString(line)
	}
}

// frame draws the border of a generated cover on dst.
func frame(dst draw.Image) {
	outer := dst.Bounds().Inset(frameInset)
	draw.Draw(dst, outer, image.Black, image.Point{}, draw.Src)
	draw.Draw(dst, outer.Inset(frameWidth), image.White, image.Point{}, draw.Src)
}

// titleCover returns a cover made of the title and the authors of a
// book, as a JPEG, for books that come without one. It returns nil when
// the title cannot be written, which is the case for titles in scripts
// the built in font does not have.
func titleCover(title string, authors []string) []byte {
	bold, regular := coverFonts()
	if bold == nil || regular == nil {
		return nil
	}
	if title = drawable(bold, title); title == "" {
		return nil
	}
	width := coverWidth - 2*coverMargin
	heading, err := textStyle{font: bold, largest: 136, smallest: 64, step: 8, maxLines: 6}.fit(title, width)
	if err != nil {
		return nil
	}
	names, err := textStyle{font: regular, largest: 64, smallest: 44, step: 4, maxLines: 3}.
		fit(drawable(regular, strings.Join(authors, ", ")), width)
	if err != nil {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, coverWidth, coverHeight))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	frame(img)
	height := heading.height()
	if len(names.lines) > 0 {
		height += 2*ruleGap + ruleHeight + names.height()
	}
	top := (coverHeight - height) * 2 / 5
	heading.draw(img, top)
	if len(names.lines) > 0 {
		rule := top + heading.height() + ruleGap
		draw.Draw(img, image.Rect((coverWidth-ruleWidth)/2, rule, (coverWidth+ruleWidth)/2, rule+ruleHeight),
			image.Black, image.Point{}, draw.Src)
		names.draw(img, rule+ruleHeight+ruleGap)
	}
	data, err := encodeJPEG(img)
	if err != nil {
		return nil
	}
	return data
}
