package docx

import (
	"regexp"
	"strconv"
	"strings"
)

// maxHeading is the deepest heading level a book has.
const maxHeading = 6

// The kinds of things a paragraph is made of.
const (
	// spanText is a run of text that looks one way.
	spanText = iota
	// spanBreak is a line break.
	spanBreak
	// spanImage is a picture.
	spanImage
	// spanNote is a reference to a footnote or an endnote.
	spanNote
	// spanAnchor is a place a link can lead to.
	spanAnchor
)

// span is one thing in a paragraph: text, a break, a picture, a note
// reference or an anchor.
type span struct {
	// kind says which of those it is.
	kind int
	// text is the text of a run.
	text string
	// bold, italic, underline and strike say how the text looks.
	bold, italic, underline, strike bool
	// vert is "sup" or "sub" for raised or lowered text.
	vert string
	// size is the size of the text in half points; zero means unknown.
	size int
	// href is where the text links to: an address, or "#" and the
	// name of an anchor in the document.
	href string
	// ref is the identifier of the picture relationship, the key of
	// the note or the name of the anchor.
	ref string
	// alt describes a picture in words.
	alt string
}

// block is a paragraph or a table.
type block struct {
	// spans holds what a paragraph is made of.
	spans []span
	// rows holds the cells of a table; nil for a paragraph.
	rows [][]cell
	// level is the heading level, from one; zero for ordinary text.
	level int
	// listed reports whether the paragraph is an item of a list.
	listed bool
	// numID identifies the list the item belongs to.
	numID string
	// depth is the nesting depth of the item, from zero.
	depth int
	// ordered reports whether the items of that list are numbered.
	ordered bool
	// align is "center" or "right" for text set that way.
	align string
	// quote reports whether the paragraph is a quotation.
	quote bool
	// fresh reports whether the paragraph starts on a new page.
	fresh bool
}

// cell is one cell of a table.
type cell struct {
	// blocks holds the content of the cell.
	blocks []block
	// span is the number of columns the cell covers.
	span int
}

// field is a place where Word works something out, such as a table of
// contents or a page number, and keeps the result as text.
type field struct {
	// instruction says what is worked out.
	instruction string
	// result reports whether the text that follows is the result, as
	// opposed to the instruction.
	result bool
}

// droppedFields lists the fields whose results mean nothing in a book
// without pages: tables of contents, indexes and page numbers.
var droppedFields = map[string]bool{
	"TOC": true, "INDEX": true, "PAGE": true, "NUMPAGES": true, "PAGEREF": true, "SECTIONPAGES": true,
}

// fieldLink matches the target of a link written as a field.
var fieldLink = regexp.MustCompile(`"([^"]*)"`)

// reader turns the elements of a document into blocks.
type reader struct {
	// styles holds the styles of the document.
	styles *styles
	// numbering says which of its lists are numbered.
	numbering *numbering
	// links maps relationship identifiers to the addresses they name.
	links map[string]string
	// fields holds the fields that are open, innermost last. A field
	// can start in one paragraph and end in another.
	fields []field
	// anchors holds anchors met in a paragraph without text, which the
	// next paragraph with text takes over.
	anchors []span
	// fresh reports whether a page break was met since the last block.
	fresh bool
	// contents reports whether a table of contents was met since it
	// was last asked.
	contents bool
	// started reports whether the paragraph being read has shown
	// anything yet.
	started bool
	// early reports whether the paragraph being read has a page break
	// before anything it shows, which makes the paragraph itself start
	// the page.
	early bool
}

// blocks interprets the elements directly inside a body, a table cell
// or a note.
func (r *reader) blocks(children []*node) []block {
	var out []block
	for _, n := range children {
		switch n.name {
		case "p":
			if b, ok := r.paragraph(n); ok {
				out = append(out, b)
			}
		case "tbl":
			out = append(out, block{rows: r.table(n), fresh: r.takeFresh()})
		case "sdt":
			out = append(out, r.blocks(n.child("sdtContent").childrenOf())...)
		case "ins", "moveTo", "customXml", "smartTag":
			out = append(out, r.blocks(n.children)...)
		}
	}
	return out
}

// childrenOf returns the elements inside n; it is safe on a nil element.
func (n *node) childrenOf() []*node {
	if n == nil {
		return nil
	}
	return n.children
}

// takeContents reports whether a table of contents was met since the
// last time it was asked.
func (r *reader) takeContents() bool {
	met := r.contents
	r.contents = false
	return met
}

// takeFresh reports whether a page break is pending and forgets it.
func (r *reader) takeFresh() bool {
	fresh := r.fresh
	r.fresh = false
	return fresh
}

// table interprets a table as rows of cells.
func (r *reader) table(tbl *node) [][]cell {
	var rows [][]cell
	for _, tr := range tbl.children {
		if tr.name != "tr" {
			continue
		}
		var row []cell
		for _, tc := range tr.children {
			if tc.name != "tc" {
				continue
			}
			span, _ := strconv.Atoi(tc.child("tcPr").val("gridSpan"))
			row = append(row, cell{blocks: r.blocks(tc.children), span: max(span, 1)})
		}
		rows = append(rows, row)
	}
	return rows
}

// paragraph interprets a paragraph. It reports false for one that shows
// nothing: an empty one, or one that belongs to a table of contents.
func (r *reader) paragraph(p *node) (block, bool) {
	pPr := p.child("pPr")
	id := pPr.val("pStyle")
	if id == "" {
		id = r.styles.normal
	}
	st := r.styles.resolve(id)
	if toggleOf(pPr, "pageBreakBefore") == on {
		r.fresh = true
	}
	b := block{
		level: headingLevel(pPr.val("outlineLvl"), st, id),
		quote: quoteNames[st.name],
		align: alignment(pPr.val("jc")),
	}
	r.list(&b, pPr.child("numPr"), st)
	fresh := r.takeFresh()
	r.started, r.early = false, false
	base := st.look
	if b.level > 0 {
		base.bold = off
	}
	b.spans = append(r.anchors, r.inline(p.children, base, "")...)
	if strings.HasPrefix(st.name, "toc") {
		r.contents = true
		b.spans = anchorsOf(b.spans)
	}
	if len(plain(b.spans)) > maxHeadingLength {
		b.level = 0
	}
	if !visible(b.spans) {
		r.anchors = anchorsOf(b.spans)
		r.fresh = r.fresh || fresh || r.early
		return block{}, false
	}
	r.anchors = nil
	b.fresh = fresh || r.early
	return b, true
}

// list notes on b the list a paragraph belongs to, which its own
// properties or its style may say.
func (r *reader) list(b *block, numPr *node, st style) {
	numID, depth := st.numID, st.listLevel
	if numPr != nil {
		numID = numPr.val("numId")
		depth, _ = strconv.Atoi(numPr.val("ilvl"))
	}
	if numID == "" || numID == "0" || b.level > 0 {
		return
	}
	b.listed, b.numID, b.depth = true, numID, min(max(depth, 0), maxListDepth)
	b.ordered = r.numbering.ordered(numID, depth)
}

// alignment turns the way Word says how a paragraph is set into the
// class the book uses for it; ordinary text has none.
func alignment(jc string) string {
	switch jc {
	case "center":
		return "center"
	case "right", "end":
		return "right"
	}
	return ""
}

// visible reports whether any of the spans shows something.
func visible(spans []span) bool {
	for _, s := range spans {
		switch s.kind {
		case spanImage, spanNote:
			return true
		case spanText:
			if strings.TrimSpace(s.text) != "" {
				return true
			}
		}
	}
	return false
}

// anchorsOf returns the anchors among the spans.
func anchorsOf(spans []span) []span {
	var out []span
	for _, s := range spans {
		if s.kind == spanAnchor {
			out = append(out, s)
		}
	}
	return out
}

// inline interprets what is inside a paragraph, a link or another
// element that only groups runs. base is how text looks unless a run
// says otherwise, and href is the link the runs are part of.
func (r *reader) inline(children []*node, base look, href string) []span {
	var out []span
	for _, n := range children {
		switch n.name {
		case "r":
			out = append(out, r.run(n, base, href)...)
		case "hyperlink":
			out = append(out, r.inline(n.children, base, r.target(n))...)
		case "fldSimple":
			if !droppedFields[fieldName(n.attr("instr"))] {
				out = append(out, r.inline(n.children, base, href)...)
			}
		case "bookmarkStart":
			if name := n.attr("name"); name != "" && name != "_GoBack" {
				out = append(out, span{kind: spanAnchor, ref: name})
			}
		case "ins", "moveTo", "smartTag", "customXml":
			out = append(out, r.inline(n.children, base, href)...)
		case "sdt":
			out = append(out, r.inline(n.child("sdtContent").childrenOf(), base, href)...)
		}
	}
	return out
}

// target returns where a hyperlink element leads: the address its
// relationship names, or an anchor of the document.
func (r *reader) target(link *node) string {
	if address := r.links[link.attr("r:id")]; address != "" {
		return address
	}
	if anchor := link.attr("anchor"); anchor != "" {
		return "#" + anchor
	}
	return ""
}

// fieldName returns the first word of a field instruction, which says
// what the field works out.
func fieldName(instruction string) string {
	words := strings.Fields(instruction)
	if len(words) == 0 {
		return ""
	}
	return strings.ToUpper(words[0])
}

// hidden reports whether text met now belongs to a field that is left
// out, or to the instruction of any field.
func (r *reader) hidden() bool {
	for _, f := range r.fields {
		if !f.result || droppedFields[fieldName(f.instruction)] {
			return true
		}
	}
	return false
}

// fieldHref returns the link the innermost open field makes of its
// result, or an empty string.
func (r *reader) fieldHref() string {
	if len(r.fields) == 0 {
		return ""
	}
	instruction := r.fields[len(r.fields)-1].instruction
	if fieldName(instruction) != "HYPERLINK" {
		return ""
	}
	match := fieldLink.FindStringSubmatch(instruction)
	if match == nil {
		return ""
	}
	if strings.Contains(instruction, `\l`) {
		return "#" + match[1]
	}
	return match[1]
}

// fieldMark follows the marks that open a field, separate its
// instruction from its result, and close it.
func (r *reader) fieldMark(kind string) {
	switch kind {
	case "begin":
		r.fields = append(r.fields, field{})
	case "separate":
		if len(r.fields) > 0 {
			r.fields[len(r.fields)-1].result = true
		}
	case "end":
		if len(r.fields) > 0 {
			r.fields = r.fields[:len(r.fields)-1]
		}
	}
}

// run interprets one run of a paragraph.
func (r *reader) run(n *node, base look, href string) []span {
	rPr := n.child("rPr")
	look := lookOf(rPr).over(r.styles.resolve(rPr.val("rStyle")).look.over(base))
	shape := span{
		bold: look.bold == on, italic: look.italic == on, size: look.size,
		underline: underlined(rPr), strike: toggleOf(rPr, "strike") == on,
		vert: vertical(rPr.val("vertAlign")), href: href,
	}
	if shape.href == "" {
		shape.href = r.fieldHref()
	}
	var out []span
	for _, c := range n.children {
		out = append(out, r.piece(c, shape)...)
	}
	return out
}

// underlined reports whether run properties underline the text.
func underlined(rPr *node) bool {
	u := rPr.child("u")
	return u != nil && u.attr("val") != "" && u.attr("val") != "none"
}

// vertical turns the way Word says text is raised or lowered into the
// name of the element that does it.
func vertical(value string) string {
	switch value {
	case "superscript":
		return "sup"
	case "subscript":
		return "sub"
	}
	return ""
}

// piece interprets one element of a run, giving text the shape of the
// run.
func (r *reader) piece(c *node, shape span) []span {
	switch c.name {
	case "fldChar":
		r.fieldMark(c.attr("fldCharType"))
		return nil
	case "instrText":
		if n := len(r.fields); n > 0 && !r.fields[n-1].result {
			r.fields[n-1].instruction += c.text
			r.contents = r.contents || fieldName(r.fields[n-1].instruction) == "TOC"
		}
		return nil
	}
	if r.hidden() {
		return nil
	}
	switch c.name {
	case "t":
		r.started = r.started || strings.TrimSpace(c.text) != ""
		return []span{textSpan(shape, c.text)}
	case "tab":
		return []span{textSpan(shape, " ")}
	case "noBreakHyphen":
		return []span{textSpan(shape, "‑")}
	case "br", "cr":
		if c.attr("type") == "page" {
			r.fresh, r.early = r.fresh || r.started, r.early || !r.started
			return nil
		}
		return []span{{kind: spanBreak}}
	case "drawing", "pict":
		r.started = true
		return picture(c)
	case "footnoteReference":
		return []span{{kind: spanNote, ref: footnoteKey + c.attr("id")}}
	case "endnoteReference":
		return []span{{kind: spanNote, ref: endnoteKey + c.attr("id")}}
	}
	return nil
}

// textSpan returns shape as a run of the given text.
func textSpan(shape span, text string) span {
	shape.kind, shape.text = spanText, text
	return shape
}

// picture returns the picture a drawing shows, if it shows one stored
// in the document.
func picture(drawing *node) []span {
	id := drawing.find("blip").attr("r:embed")
	if id == "" {
		id = drawing.find("imagedata").attr("r:id")
	}
	if id == "" {
		return nil
	}
	return []span{{kind: spanImage, ref: id, alt: drawing.find("docPr").attr("descr")}}
}
