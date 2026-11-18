package reflow

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Thresholds of the paragraph analysis, as shares of the font size
// unless noted otherwise.
const (
	// headingRatio is how much larger than the body text a heading is.
	headingRatio = 1.15
	// paragraphPitch is how much farther apart than the usual line
	// pitch two lines are when a new paragraph starts between them.
	paragraphPitch = 1.4
	// indent is the first line indent that starts a paragraph.
	indent = 0.8
	// flush is how close to the left edge a line counts as not
	// indented.
	flush = 0.4
	// shortLine is the share of the block width a line falls short by
	// when it ends a paragraph.
	shortLine = 0.2
	// footnoteRatio is how much smaller than the body text notes are.
	footnoteRatio = 0.92
	// maxHeading is the longest text, in characters, of a heading that
	// is recognised by being bold rather than large.
	maxHeading = 90
	// defaultPitch is the line pitch assumed when it cannot be measured.
	defaultPitch = 1.2
	// stubLine is the share of the block width a line falls short by
	// when it cannot be a line of running text, whatever it ends with.
	stubLine = 0.4
	// fullLine is how close to the right edge a line ends when it runs
	// the full width.
	fullLine = 1.5
)

// kind says what an element is.
type kind int

// The kinds of element.
const (
	// paragraph is a paragraph of body text.
	paragraph kind = iota
	// heading is a heading.
	heading
	// bullet is an item of an unordered list.
	bullet
	// figure is an image.
	figure
)

// element is one block of the reconstructed document.
type element struct {
	// kind says what the element is.
	kind kind
	// level is the level of a heading, from 1 to 3.
	level int
	// spans holds the text.
	spans []span
	// image is the file name of a figure.
	image string
	// page is the index of the page the element starts on.
	page int
	// y is the top of the element on that page.
	y float64
	// size is the font size of the text.
	size float64
	// id is the anchor of the element, when something links to it.
	id string
	// flows reports whether text from the previous column or page may
	// continue in the element.
	flows bool
	// x0 is the left edge of the element on its page.
	x0 float64
	// x1 is the right edge of the element on its page.
	x1 float64
	// numbered reports whether the element starts with a number or a
	// letter that marks it as an item of a list.
	numbered bool
	// indented reports whether the first line is indented.
	indented bool
	// full reports whether the last line runs the full width, as a line
	// in the middle of a paragraph does.
	full bool
}

// text returns the text of the element.
func (e *element) text() string {
	var sb strings.Builder
	for _, s := range e.spans {
		sb.WriteString(s.text)
	}
	return strings.TrimSpace(sb.String())
}

// bulletMarker matches the marker of an unordered list item.
var bulletMarker = regexp.MustCompile(
	`^\s*[\x{2022}\x{2023}\x{25E6}\x{25AA}\x{25CF}\x{25CB}\x{2043}\x{00B7}\x{2013}*-]\s+`)

// numberMarker matches the marker of an ordered list item.
var numberMarker = regexp.MustCompile(`^\s*\(?(\d{1,3}|[a-zA-Z]|[ivx]{1,4})[.)]\s+`)

// leader matches the dots and the page number that end a line of a
// printed table of contents.
var leader = regexp.MustCompile(`(\s*\.){5,}\s*[\divxlc]+\s*$`)

// terminal lists the characters that can end a sentence or a paragraph.
const terminal = ".!?:;…\"'”’)]»"

// style holds the measurements of a document that paragraphs and
// headings are judged against.
type style struct {
	// body is the font size of the body text.
	body float64
	// pitch is the usual distance between baselines, as a multiple of
	// the font size.
	pitch float64
	// headings holds the font sizes used by headings, largest first.
	headings []float64
	// words holds the words of the document.
	words vocabulary
}

// measure derives the style of the document from its pages.
func measure(pages []layout) style {
	weights := map[float64]int{}
	words := vocabulary{}
	var pitches []float64
	for _, page := range pages {
		var prev *line
		for _, b := range page.blocks {
			if b.flows {
				prev = nil
			}
			for i, ln := range b.lines {
				weights[ln.size] += utf8.RuneCountInString(ln.text())
				words.add(ln.text())
				if prev != nil && prev.size == ln.size && ln.y > prev.y {
					pitches = append(pitches, (ln.y-prev.y)/ln.size)
				}
				prev = &b.lines[i]
			}
		}
	}
	st := style{body: heaviest(weights), pitch: defaultPitch, words: words}
	if len(pitches) > 0 {
		sort.Float64s(pitches)
		st.pitch = math.Max(pitches[len(pitches)/2], 1)
	}
	for size := range weights {
		if size >= headingRatio*st.body {
			st.headings = append(st.headings, size)
		}
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(st.headings)))
	return st
}

// headingLevel returns the heading level of a line, or zero when the
// line is body text. alone reports whether the line stands by itself.
func (st style) headingLevel(ln line, alone bool) int {
	if ln.size >= headingRatio*st.body {
		rank := 1
		for _, size := range st.headings {
			if size > ln.size+0.5 && rank < 3 {
				rank++
			}
		}
		return rank
	}
	text := strings.TrimSpace(ln.text())
	if !ln.bold || !alone || ln.size < footnoteRatio*st.body || utf8.RuneCountInString(text) > maxHeading {
		return 0
	}
	if last, _ := utf8.DecodeLastRuneInString(text); strings.ContainsRune(".,;:", last) || !hasLetter(text) {
		return 0
	}
	return 3
}

// hasLetter reports whether text holds at least one letter.
func hasLetter(text string) bool {
	return strings.IndexFunc(text, unicode.IsLetter) >= 0
}

// builder turns the lines of a document into elements.
type builder struct {
	// st is the style of the document.
	st style
	// out collects the elements.
	out []element
	// open reports whether the last element can take further lines.
	open bool
	// last is the line added most recently.
	last *line
	// left is the left edge of the column being added.
	left float64
	// right is the right edge of the column being added.
	right float64
}

// elements returns the elements of the given pages in reading order.
func (st style) elements(pages []layout) []element {
	b := builder{st: st}
	for _, page := range pages {
		extents := columnExtents(page.blocks)
		for _, blk := range page.blocks {
			b.left, b.right = extents[blk.column][0], extents[blk.column][1]
			b.addBlock(blk)
		}
	}
	return b.out
}

// columnExtents returns the left and right edge of every column of a
// page, which the lines of its blocks are measured against.
func columnExtents(blocks []block) map[int][2]float64 {
	extents := map[int][2]float64{}
	for _, blk := range blocks {
		edge, known := extents[blk.column]
		if !known {
			edge = [2]float64{math.Inf(1), math.Inf(-1)}
		}
		for _, ln := range blk.lines {
			edge[0], edge[1] = math.Min(edge[0], ln.x0), math.Max(edge[1], ln.x1)
		}
		extents[blk.column] = edge
	}
	return extents
}

// before returns the line that precedes the line with index i of blk in
// the same column, which is the last line of the block above when the
// line is the first of its block, or nil.
func (b *builder) before(blk block, i int) *line {
	if i > 0 {
		return &blk.lines[i-1]
	}
	ln := blk.lines[0]
	if blk.flows || b.last == nil || b.last.page != ln.page || b.last.y >= ln.y {
		return nil
	}
	return b.last
}

// addBlock adds the lines of one block.
func (b *builder) addBlock(blk block) {
	if len(blk.lines) == 0 {
		return
	}
	for i, ln := range blk.lines {
		prev := b.before(blk, i)
		flows := i == 0 && blk.flows
		b.open = b.open && prev != nil
		starts := !b.open || b.startsParagraph(*prev, ln)
		text := ln.text()
		switch level := b.st.headingLevel(ln, b.standsAlone(blk.lines, i)); {
		case leader.MatchString(text):
			b.addContentsLine(ln)
		case level > 0:
			b.addHeading(ln, level, prev != nil && b.continuesHeading(*prev, ln, level))
		case bulletMarker.MatchString(text) && (starts || b.inList(bullet, false)):
			b.start(ln, bullet, flows)
		case numberMarker.MatchString(text) && (starts || b.inList(paragraph, true)):
			b.start(ln, paragraph, flows)
			b.out[len(b.out)-1].numbered = true
		case !starts:
			b.extend(ln)
		default:
			b.start(ln, paragraph, flows)
		}
	}
	b.last = &blk.lines[len(blk.lines)-1]
}

// inList reports whether the last element is a list item of the given
// kind.
func (b *builder) inList(k kind, numbered bool) bool {
	if len(b.out) == 0 {
		return false
	}
	last := b.out[len(b.out)-1]
	return b.open && last.kind == k && last.numbered == numbered
}

// addContentsLine adds a line of a printed table of contents as a
// paragraph of its own, without its dots and page number.
func (b *builder) addContentsLine(ln line) {
	title := strings.TrimSpace(leader.ReplaceAllString(ln.text(), ""))
	ln.spans = []span{{text: title}}
	b.start(ln, paragraph, false)
	b.open = false
}

// standsAlone reports whether the line with index i is set apart from
// the lines around it, by space or by a change of weight.
func (b *builder) standsAlone(lines []line, i int) bool {
	apart := func(a, c line) bool {
		return !a.bold || !c.bold || math.Abs(c.y-a.y) > paragraphPitch*b.st.pitch*c.size
	}
	before := i == 0 || apart(lines[i-1], lines[i])
	after := i == len(lines)-1 || apart(lines[i], lines[i+1])
	return before && after
}

// continuesHeading reports whether ln is a further line of the heading
// that prev belongs to.
func (b *builder) continuesHeading(prev, ln line, level int) bool {
	if len(b.out) == 0 {
		return false
	}
	last := &b.out[len(b.out)-1]
	return last.kind == heading && last.level == level && math.Abs(prev.size-ln.size) < 0.5 &&
		ln.y-prev.y < 1.6*b.st.pitch*ln.size
}

// addHeading adds a heading line, either to the heading before it or as
// a new one. Body text never continues a heading.
func (b *builder) addHeading(ln line, level int, continues bool) {
	b.open = false
	if continues {
		b.extend(ln)
		return
	}
	b.out = append(b.out, element{
		kind: heading, level: level, spans: plain(ln.spans), page: ln.page, y: ln.y - ln.size, size: ln.size,
		x0: ln.x0, x1: ln.x1,
	})
}

// plain returns spans as one span without any style.
func plain(spans []span) []span {
	var sb strings.Builder
	for _, s := range spans {
		sb.WriteString(s.text)
	}
	return []span{{text: sb.String()}}
}

// start begins a new element of the given kind with the line ln, after
// removing the marker of a bullet.
func (b *builder) start(ln line, k kind, flows bool) {
	spans := append([]span(nil), ln.spans...)
	if k == bullet {
		spans = trimMarker(spans, bulletMarker)
	}
	b.out = append(b.out, element{
		kind: k, spans: spans, page: ln.page, y: ln.y - ln.size, size: ln.size, flows: flows, x0: ln.x0, x1: ln.x1,
		indented: ln.x0-b.left > indent*ln.size, full: b.right-ln.x1 < fullLine*ln.size,
	})
	b.open = true
}

// trimMarker removes the list marker matched by marker from the start
// of spans.
func trimMarker(spans []span, marker *regexp.Regexp) []span {
	var sb strings.Builder
	for _, s := range spans {
		sb.WriteString(s.text)
	}
	cut := len(marker.FindString(sb.String()))
	for len(spans) > 0 && cut > 0 {
		if cut < len(spans[0].text) {
			spans[0].text = spans[0].text[cut:]
			break
		}
		cut -= len(spans[0].text)
		spans = spans[1:]
	}
	return spans
}

// startsParagraph reports whether ln starts a new paragraph after prev
// in the block being added.
func (b *builder) startsParagraph(prev, ln line) bool {
	last := &b.out[len(b.out)-1]
	left, right := b.left, b.right
	if math.Abs(ln.size-prev.size) > 0.1*prev.size {
		return true
	}
	if ln.y-prev.y > paragraphPitch*b.st.pitch*ln.size {
		return true
	}
	if last.kind != paragraph || last.numbered {
		return ln.x0 < prev.x0-flush*ln.size && ln.x0-left < flush*ln.size && len(last.spans) > 0 &&
			ln.y-prev.y > 1.15*b.st.pitch*ln.size
	}
	if ln.x0-left > indent*ln.size && prev.x0-left < flush*ln.size {
		return true
	}
	missing := right - prev.x1
	if right-left > 10*ln.size && missing > stubLine*(right-left) {
		return true
	}
	return missing > math.Max(shortLine*(right-left), 3*ln.size) && endsSentence(prev.text())
}

// endsSentence reports whether text ends with a character that can end
// a sentence.
func endsSentence(text string) bool {
	last, _ := utf8.DecodeLastRuneInString(strings.TrimSpace(text))
	return strings.ContainsRune(terminal, last)
}

// extend adds the line ln to the last element.
func (b *builder) extend(ln line) {
	last := &b.out[len(b.out)-1]
	last.full = b.right-ln.x1 < fullLine*ln.size
	if last.kind == heading {
		last.spans = plain(joinSpans(last.spans, ln.spans, b.st.words))
		return
	}
	last.spans = joinSpans(last.spans, ln.spans, b.st.words)
}

// hyphens lists the characters that hyphenate a word at a line end.
const hyphens = "-\u2010\u00ad"

// word matches a word, with the hyphens inside it.
var word = regexp.MustCompile(`\p{L}+(?:-\p{L}+)*`)

// lastWord matches the letters before a hyphen at the end of a line.
var lastWord = regexp.MustCompile(`(\p{L}+)[-\x{2010}\x{00ad}]$`)

// firstWord matches the letters at the start of a line.
var firstWord = regexp.MustCompile(`^\p{L}+`)

// vocabulary holds the words of a document in lower case, including the
// compounds written with a hyphen, as they appear within lines.
type vocabulary map[string]bool

// add records the words of text.
func (v vocabulary) add(text string) {
	for _, w := range word.FindAllString(text, -1) {
		v[strings.ToLower(w)] = true
	}
}

// joinSpans appends the spans of a following line to spans. A hyphen
// that splits a word across the two lines is removed, unless words says
// the document writes that word with a hyphen; otherwise a space goes
// between the lines.
func joinSpans(spans, next []span, words vocabulary) []span {
	if len(spans) == 0 {
		return append(spans, next...)
	}
	if len(next) == 0 {
		return spans
	}
	last := &spans[len(spans)-1]
	last.text = strings.TrimRight(last.text, " ")
	first := strings.TrimLeft(next[0].text, " ")
	glue := " "
	if splitsWord(last.text, first) {
		glue = ""
		if !words.compound(last.text, first) {
			_, size := utf8.DecodeLastRuneInString(last.text)
			last.text = last.text[:len(last.text)-size]
		}
	}
	spans = appendSpan(spans, span{text: glue + first, bold: next[0].bold, italic: next[0].italic})
	for _, s := range next[1:] {
		spans = appendSpan(spans, s)
	}
	return spans
}

// compound reports whether the word split between before and after is
// one the document writes with a hyphen elsewhere, and never without.
func (v vocabulary) compound(before, after string) bool {
	head := lastWord.FindStringSubmatch(before)
	tail := firstWord.FindString(after)
	if head == nil || tail == "" {
		return false
	}
	left, right := strings.ToLower(head[1]), strings.ToLower(tail)
	return v[left+"-"+right] && !v[left+right]
}

// splitsWord reports whether before ends with a hyphen that continues a
// word starting after. Only a lower case letter on both sides counts,
// so dashes and ranges of numbers are left alone.
func splitsWord(before, after string) bool {
	hyphen, size := utf8.DecodeLastRuneInString(before)
	if !strings.ContainsRune(hyphens, hyphen) || len(before) == size {
		return false
	}
	previous, _ := utf8.DecodeLastRuneInString(before[:len(before)-size])
	next, _ := utf8.DecodeRuneInString(after)
	return unicode.IsLower(previous) && unicode.IsLower(next)
}

// mergeFlows joins paragraphs that were cut by the end of a column or a
// page. A paragraph continues in the first paragraph of the next column
// or page when it does not end a sentence, or when its last line runs
// the full width and what follows does not look like a new paragraph.
// Notes set in a smaller size at the foot of the page do not get in the
// way.
func (st style) mergeFlows(elements []element) []element {
	body := st.body
	indented, paragraphs := 0, 0
	for _, e := range elements {
		if e.kind == paragraph && !e.flows {
			paragraphs++
			if e.indented {
				indented++
			}
		}
	}
	indents := 2*indented > paragraphs
	var out []element
	for _, e := range elements {
		target := -1
		if e.flows && e.kind == paragraph && !e.numbered && e.size >= footnoteRatio*body {
			target = lastBody(out, body)
		}
		if target < 0 || !continues(out[target], e, indents) {
			out = append(out, e)
			continue
		}
		out[target].spans = joinSpans(out[target].spans, e.spans, st.words)
		out[target].full = e.full
	}
	return out
}

// continues reports whether next, the first paragraph of a column or a
// page, carries on the paragraph prev. indents tells whether paragraphs
// of the document start with an indent.
func continues(prev, next element, indents bool) bool {
	if !endsSentence(prev.text()) {
		return true
	}
	if !prev.full {
		return false
	}
	first, _ := utf8.DecodeRuneInString(next.text())
	return unicode.IsLower(first) || (indents && !next.indented)
}

// lastBody returns the index of the last element when it is a paragraph
// of body text, looking past any notes after it, or -1.
func lastBody(elements []element, body float64) int {
	for i := len(elements) - 1; i >= 0; i-- {
		e := elements[i]
		if e.kind == paragraph && e.size < footnoteRatio*body {
			continue
		}
		if e.kind != paragraph {
			return -1
		}
		return i
	}
	return -1
}
