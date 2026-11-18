package pdf

import (
	"bytes"
	"math"
	"strings"
)

// maxFormDepth bounds how deeply form XObjects may nest.
const maxFormDepth = 16

// maxOperands bounds how many operands are kept for one operator.
const maxOperands = 64

// breakGap is the distance, as a share of the font size, beyond which
// text that does not continue where the previous text ended starts a
// new run.
const breakGap = 0.1

// matrix is a PDF transformation matrix [a b c d e f].
type matrix [6]float64

// identity is the matrix that changes nothing.
var identity = matrix{1, 0, 0, 1, 0, 0}

// mul returns the matrix that applies m and then n.
func (m matrix) mul(n matrix) matrix {
	return matrix{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// apply transforms the point x, y.
func (m matrix) apply(x, y float64) (float64, float64) {
	return x*m[0] + y*m[2] + m[4], x*m[1] + y*m[3] + m[5]
}

// Run is a stretch of text set in one font that continues without a
// break. Positions are in display space: points from the top left corner
// of the page, with the vertical axis pointing down.
type Run struct {
	// Text is the decoded text.
	Text string
	// X is the horizontal position where the text starts.
	X float64
	// Y is the vertical position of the baseline.
	Y float64
	// Width is the horizontal extent of the text.
	Width float64
	// Size is the font size as displayed.
	Size float64
	// Font is the name of the typeface.
	Font string
	// Bold reports whether the typeface is bold.
	Bold bool
	// Italic reports whether the typeface is italic.
	Italic bool
	// Upright reports whether the text runs left to right along a
	// horizontal baseline.
	Upright bool
}

// Placement is an image drawn on a page, with its bounding box in
// display space.
type Placement struct {
	// X is the left edge.
	X float64
	// Y is the top edge.
	Y float64
	// Width is the horizontal extent.
	Width float64
	// Height is the vertical extent.
	Height float64
	// Image is the image that is drawn.
	Image *Image
}

// Content is what a page shows.
type Content struct {
	// Width is the width of the page as displayed.
	Width float64
	// Height is the height of the page as displayed.
	Height float64
	// Runs holds the text in the order it is drawn.
	Runs []Run
	// Images holds the images in the order they are drawn.
	Images []Placement
	// Inline counts the images embedded in the content itself, which
	// are not extracted.
	Inline int
	// Paths counts the vector shapes that are painted.
	Paths int
	// Undecoded names the fonts whose text could not be decoded.
	Undecoded []string
}

// textState holds the text parameters of the graphics state.
type textState struct {
	// font is the current font.
	font *font
	// size is the font size.
	size float64
	// charSpace is added after every character.
	charSpace float64
	// wordSpace is added after every single byte space.
	wordSpace float64
	// scale is the horizontal scaling.
	scale float64
	// leading is the distance between baselines.
	leading float64
	// rise moves the baseline up.
	rise float64
}

// gstate is the part of the graphics state that matters for text.
type gstate struct {
	// ctm maps user space to display space.
	ctm matrix
	// text holds the text parameters.
	text textState
}

// pending is the run being collected.
type pending struct {
	// text collects the decoded text.
	text bytes.Buffer
	// spaced reports whether the text ends with spaces.
	spaced bool
	// keep is the length of the text before those spaces.
	keep int
	// spaceX and spaceY are where those spaces start.
	spaceX, spaceY float64
	// font is the font of the run.
	font *font
	// x and y are where the run starts.
	x, y float64
	// endX and endY are where the next character would start.
	endX, endY float64
	// size is the font size as displayed.
	size float64
	// upright reports whether the run is horizontal.
	upright bool
	// active reports whether a run is being collected.
	active bool
}

// interp interprets content streams.
type interp struct {
	// doc is the document being read.
	doc *Document
	// out receives what the content shows.
	out *Content
	// state is the current graphics state.
	state gstate
	// stack holds the saved graphics states.
	stack []gstate
	// tm is the text matrix.
	tm matrix
	// tlm is the text line matrix.
	tlm matrix
	// resources is the resource dictionary in effect.
	resources Dict
	// run is the run being collected.
	run pending
	// forms holds the object numbers of the forms being drawn.
	forms map[int]bool
	// undecoded holds the fonts whose text could not be decoded.
	undecoded map[string]bool
}

// Content interprets the content of the page.
func (p *Page) Content() *Content {
	w, h := p.Size()
	in := &interp{doc: p.doc, out: &Content{Width: w, Height: h}, tm: identity, tlm: identity}
	in.forms, in.undecoded = map[int]bool{}, map[string]bool{}
	in.state.ctm = p.base()
	in.state.text.scale = 1
	in.exec(p.contentData(), p.resources)
	in.flush()
	for name := range in.undecoded {
		in.out.Undecoded = append(in.out.Undecoded, name)
	}
	return in.out
}

// exec runs the operators in data with the given resources.
func (in *interp) exec(data []byte, resources Dict) {
	saved := in.resources
	in.resources = resources
	defer func() { in.resources = saved }()
	l := lexer{data: data}
	var operands []any
	for {
		obj, ok := l.object()
		if !ok {
			return
		}
		op, isOperator := obj.(keyword)
		if !isOperator {
			if len(operands) < maxOperands {
				operands = append(operands, obj)
			}
			continue
		}
		if op == "BI" {
			skipInline(&l)
			in.out.Inline++
		} else if handler := operators[op]; handler != nil {
			handler(in, operands)
		}
		operands = operands[:0]
	}
}

// skipInline moves l past an inline image.
func skipInline(l *lexer) {
	for {
		obj, ok := l.object()
		if !ok {
			return
		}
		if obj == keyword("ID") {
			break
		}
	}
	data := l.data
	for i := l.pos + 1; i+2 <= len(data); i++ {
		if data[i] == 'E' && data[i+1] == 'I' && isSpace(data[i-1]) &&
			(i+2 == len(data) || isSpace(data[i+2])) {
			l.pos = i + 2
			return
		}
	}
	l.pos = len(data)
}

// operand returns the operand with index i as a number, or zero.
func operand(operands []any, i int) float64 {
	if i >= len(operands) {
		return 0
	}
	switch v := operands[i].(type) {
	case int64:
		return float64(v)
	case float64:
		return v
	}
	return 0
}

// operandMatrix returns the six operands as a matrix.
func operandMatrix(operands []any) matrix {
	var m matrix
	for i := range m {
		m[i] = operand(operands, i)
	}
	return m
}

// operators maps the operators that affect text and images to their
// handlers.
var operators map[keyword]func(*interp, []any)

// init fills operators, which cannot be initialised in place because the
// handlers refer back to it.
func init() {
	operators = textOperators()
}

// textOperators returns the handlers of the supported operators.
func textOperators() map[keyword]func(*interp, []any) {
	return map[keyword]func(*interp, []any){
		"q":  func(in *interp, _ []any) { in.stack = append(in.stack, in.state) },
		"Q":  (*interp).restore,
		"cm": func(in *interp, o []any) { in.state.ctm = operandMatrix(o).mul(in.state.ctm) },
		"BT": func(in *interp, _ []any) { in.tm, in.tlm = identity, identity },
		"ET": func(in *interp, _ []any) { in.flush() },
		"Tf": (*interp).setFont,
		"Td": func(in *interp, o []any) { in.move(operand(o, 0), operand(o, 1)) },
		"TD": func(in *interp, o []any) {
			in.state.text.leading = -operand(o, 1)
			in.move(operand(o, 0), operand(o, 1))
		},
		"Tm": func(in *interp, o []any) { in.tm, in.tlm = operandMatrix(o), operandMatrix(o) },
		"T*": func(in *interp, _ []any) { in.move(0, -in.state.text.leading) },
		"Tj": func(in *interp, o []any) { in.showOperand(o, 0) },
		"TJ": (*interp).showArray,
		"'": func(in *interp, o []any) {
			in.move(0, -in.state.text.leading)
			in.showOperand(o, 0)
		},
		"\"": func(in *interp, o []any) {
			in.state.text.wordSpace, in.state.text.charSpace = operand(o, 0), operand(o, 1)
			in.move(0, -in.state.text.leading)
			in.showOperand(o, 2)
		},
		"Tc": func(in *interp, o []any) { in.state.text.charSpace = operand(o, 0) },
		"Tw": func(in *interp, o []any) { in.state.text.wordSpace = operand(o, 0) },
		"Tz": func(in *interp, o []any) { in.state.text.scale = operand(o, 0) / 100 },
		"TL": func(in *interp, o []any) { in.state.text.leading = operand(o, 0) },
		"Ts": func(in *interp, o []any) { in.state.text.rise = operand(o, 0) },
		"Do": (*interp).draw,
		"S":  (*interp).paint, "s": (*interp).paint, "f": (*interp).paint, "F": (*interp).paint,
		"f*": (*interp).paint, "B": (*interp).paint, "B*": (*interp).paint, "b": (*interp).paint,
		"b*": (*interp).paint,
	}
}

// restore pops the graphics state.
func (in *interp) restore(_ []any) {
	if n := len(in.stack); n > 0 {
		in.state = in.stack[n-1]
		in.stack = in.stack[:n-1]
	}
}

// paint counts a painted vector shape.
func (in *interp) paint(_ []any) {
	in.out.Paths++
}

// setFont selects the font and size named by the operands.
func (in *interp) setFont(operands []any) {
	if len(operands) < 2 {
		return
	}
	name, _ := operands[0].(Name)
	fonts := in.doc.dict(in.resources["Font"])
	in.state.text.font = in.doc.font(fonts[name])
	in.state.text.size = operand(operands, 1)
}

// move starts a new line offset from the start of the current one.
func (in *interp) move(tx, ty float64) {
	in.tlm = matrix{1, 0, 0, 1, tx, ty}.mul(in.tlm)
	in.tm = in.tlm
}

// showOperand shows the string operand with index i.
func (in *interp) showOperand(operands []any, i int) {
	if i < len(operands) {
		if s, ok := operands[i].(String); ok {
			in.show(s)
		}
	}
}

// showArray shows the strings of a TJ array, moving by the numbers
// between them.
func (in *interp) showArray(operands []any) {
	if len(operands) == 0 {
		return
	}
	items, _ := operands[0].(Array)
	ts := in.state.text
	for i, item := range items {
		if s, ok := item.(String); ok {
			in.show(s)
			continue
		}
		shift := -operand(items, i) / 1000 * ts.size * ts.scale
		in.tm = matrix{1, 0, 0, 1, shift, 0}.mul(in.tm)
	}
}

// show adds the characters of s to the page. The extra space set with
// the character and word spacing operators comes after each character
// and does not count towards its extent, so the text after a widely
// spaced character is seen to stand apart from it.
func (in *interp) show(s String) {
	ts := in.state.text
	if ts.font == nil {
		return
	}
	ts.font.each(s, func(text string, advance float64, space bool) {
		start := in.rendering()
		in.tm = matrix{1, 0, 0, 1, advance * ts.size * ts.scale, 0}.mul(in.tm)
		end := in.rendering()
		extra := ts.charSpace
		if space {
			extra += ts.wordSpace
		}
		in.tm = matrix{1, 0, 0, 1, extra * ts.scale, 0}.mul(in.tm)
		if text == "" {
			in.undecoded[ts.font.name] = true
			return
		}
		in.add(text, start, end)
	})
}

// rendering returns the matrix that maps glyph space, scaled to the font
// size, to display space at the current text position.
func (in *interp) rendering() matrix {
	ts := in.state.text
	return matrix{ts.size * ts.scale, 0, 0, ts.size, 0, ts.rise}.mul(in.tm).mul(in.state.ctm)
}

// add appends text drawn from the position of start to that of end to
// the run being collected, starting a new run when the text does not
// continue it.
func (in *interp) add(text string, start, end matrix) {
	run := &in.run
	size := math.Hypot(start[2], start[3])
	font := in.state.text.font
	if run.active {
		gap := math.Hypot(start[4]-run.endX, start[5]-run.endY)
		if run.font != font || math.Abs(run.size-size) > 0.01*size || gap > breakGap*size {
			in.dropOverprinted(start)
			in.flush()
		}
	}
	if !run.active {
		run.active, run.font, run.size, run.spaced = true, font, size, false
		run.x, run.y = start[4], start[5]
		run.upright = start[0] > 0 && math.Abs(start[1]) < 0.3*start[0]
	}
	if blank := strings.TrimSpace(text) == ""; blank && !run.spaced {
		run.spaced, run.keep, run.spaceX, run.spaceY = true, run.text.Len(), start[4], start[5]
	} else if !blank {
		run.spaced = false
	}
	run.text.WriteString(text)
	run.endX, run.endY = end[4], end[5]
}

// dropOverprinted removes the spaces at the end of the run being
// collected when the text that follows, which starts at the position of
// next, is set on top of them. Some programs write a space and then step
// back over it, which shows as no space at all.
func (in *interp) dropOverprinted(next matrix) {
	run := &in.run
	if !run.spaced {
		return
	}
	toSpaces := math.Hypot(next[4]-run.spaceX, next[5]-run.spaceY)
	if toSpaces < math.Hypot(next[4]-run.endX, next[5]-run.endY) {
		run.text.Truncate(run.keep)
		run.endX, run.endY = run.spaceX, run.spaceY
	}
}

// flush finishes the run being collected.
func (in *interp) flush() {
	run := &in.run
	if !run.active {
		return
	}
	run.active = false
	text := run.text.String()
	run.text.Reset()
	if text == "" {
		return
	}
	in.out.Runs = append(in.out.Runs, Run{
		Text: text, X: run.x, Y: run.y, Width: math.Hypot(run.endX-run.x, run.endY-run.y),
		Size: run.size, Font: run.font.name, Bold: run.font.bold, Italic: run.font.italic,
		Upright: run.upright,
	})
}

// draw handles the Do operator: it records images and runs the content
// of forms.
func (in *interp) draw(operands []any) {
	if len(operands) == 0 {
		return
	}
	name, _ := operands[0].(Name)
	objects := in.doc.dict(in.resources["XObject"])
	stream := in.doc.stream(objects[name])
	if stream == nil {
		return
	}
	switch in.doc.name(stream.Dict["Subtype"]) {
	case "Image":
		in.place(stream)
	case "Form":
		in.form(stream)
	}
}

// place records the image stream drawn into the unit square of the
// current coordinate system.
func (in *interp) place(stream *Stream) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, corner := range [][2]float64{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		x, y := in.state.ctm.apply(corner[0], corner[1])
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	in.out.Images = append(in.out.Images, Placement{
		X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY, Image: in.doc.image(stream),
	})
}

// form runs the content of a form XObject.
func (in *interp) form(stream *Stream) {
	if len(in.forms) >= maxFormDepth || in.forms[stream.num] {
		return
	}
	data, err := stream.Data()
	if err != nil {
		return
	}
	in.flush()
	in.forms[stream.num] = true
	saved, savedTm, savedTlm := in.state, in.tm, in.tlm
	if m := in.doc.array(stream.Dict["Matrix"]); len(m) == 6 {
		var local matrix
		for i := range local {
			local[i] = in.doc.float(m[i], identity[i])
		}
		in.state.ctm = local.mul(in.state.ctm)
	}
	resources := in.doc.dict(stream.Dict["Resources"])
	if resources == nil {
		resources = in.resources
	}
	depth := len(in.stack)
	in.exec(data, resources)
	in.flush()
	in.stack = in.stack[:min(depth, len(in.stack))]
	in.state, in.tm, in.tlm = saved, savedTm, savedTlm
	delete(in.forms, stream.num)
}
