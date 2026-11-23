package docx

import (
	"regexp"
	"strconv"
	"strings"
)

// maxStyleChain bounds how many styles a style may be based on, one
// after another.
const maxStyleChain = 24

// bodyOutline is the outline level Word gives text that is no heading.
const bodyOutline = 9

// headingName matches the names Word gives its heading styles.
var headingName = regexp.MustCompile(`(?i)^heading\s*([1-9])$`)

// quoteNames lists the names of the styles Word offers for quotations,
// in lower case.
var quoteNames = map[string]bool{"quote": true, "intense quote": true, "block text": true}

// look is how text looks: what a run of text, a style or the document
// as a whole says about weight, slant and size.
type look struct {
	// bold says whether the text is bold.
	bold toggle
	// italic says whether the text is slanted.
	italic toggle
	// size is the size of the text in half points; zero means not said.
	size int
}

// over returns l with what it does not say taken from base.
func (l look) over(base look) look {
	if l.size == 0 {
		l.size = base.size
	}
	return look{bold: l.bold.or(base.bold), italic: l.italic.or(base.italic), size: l.size}
}

// lookOf reads how text looks from a run properties element, which may
// be nil.
func lookOf(rPr *node) look {
	size, _ := strconv.Atoi(rPr.val("sz"))
	return look{bold: toggleOf(rPr, "b"), italic: toggleOf(rPr, "i"), size: size}
}

// style is one style of the document, with what it takes from the
// styles it is based on already filled in.
type style struct {
	// name is the name of the style as Word shows it, in lower case.
	name string
	// basedOn is the identifier of the style this one starts from.
	basedOn string
	// outline is the heading level the style gives a paragraph, from
	// zero for the highest; -1 means it gives none.
	outline int
	// look is how the style makes text look.
	look look
	// numID is the list the style puts a paragraph in; empty for none.
	numID string
	// listLevel is the nesting depth in that list.
	listLevel int
}

// styles holds the styles of a document by identifier.
type styles struct {
	// byID maps identifiers to styles, as written.
	byID map[string]*style
	// defaults is how text looks when nothing says otherwise.
	defaults look
	// language is the language the document declares for its text.
	language string
	// normal is the identifier of the style paragraphs have by default.
	normal string
}

// readStyles interprets the styles part of a document, which may be
// nil when the document has none.
func readStyles(root *node) *styles {
	s := &styles{byID: map[string]*style{}}
	if root == nil {
		return s
	}
	defaults := root.child("docDefaults").child("rPrDefault").child("rPr")
	s.defaults = lookOf(defaults)
	s.language = defaults.val("lang")
	for _, n := range root.children {
		if n.name != "style" {
			continue
		}
		id := n.attr("styleId")
		s.byID[id] = styleOf(n)
		if n.attr("type") == "paragraph" && truthy(n.attr("default")) {
			s.normal = id
		}
	}
	return s
}

// truthy reports whether a written value means yes.
func truthy(value string) bool {
	switch strings.ToLower(value) {
	case "1", "true", "on":
		return true
	}
	return false
}

// styleOf reads one style element, leaving out what it inherits.
func styleOf(n *node) *style {
	pPr := n.child("pPr")
	st := &style{
		name:    strings.ToLower(n.val("name")),
		basedOn: n.val("basedOn"),
		outline: -1,
		look:    lookOf(n.child("rPr")),
	}
	if level, err := strconv.Atoi(pPr.val("outlineLvl")); err == nil {
		st.outline = level
	}
	if numPr := pPr.child("numPr"); numPr != nil {
		st.numID = numPr.val("numId")
		st.listLevel, _ = strconv.Atoi(numPr.val("ilvl"))
	}
	return st
}

// resolve returns the style with the given identifier with everything
// it inherits filled in, or an empty style when there is no such style.
func (s *styles) resolve(id string) style {
	out := style{outline: -1}
	named := false
	for range maxStyleChain {
		st, ok := s.byID[id]
		if !ok {
			break
		}
		if !named {
			out.name, named = st.name, true
		}
		if out.outline < 0 {
			out.outline = st.outline
		}
		if out.numID == "" {
			out.numID, out.listLevel = st.numID, st.listLevel
		}
		out.look = out.look.over(st.look)
		id = st.basedOn
	}
	out.look = out.look.over(s.defaults)
	return out
}

// headingLevel returns the heading level, from one, that a paragraph
// has through its own outline level, or else through its style: by the
// style's outline level or by its name. Zero means it is no heading.
func headingLevel(own string, st style, id string) int {
	if level, err := strconv.Atoi(own); err == nil {
		return levelOf(level)
	}
	if st.outline >= 0 {
		return levelOf(st.outline)
	}
	for _, name := range []string{st.name, id} {
		if match := headingName.FindStringSubmatch(name); match != nil {
			level, _ := strconv.Atoi(match[1])
			return min(level, maxHeading)
		}
	}
	if st.name == "title" {
		return 1
	}
	return 0
}

// levelOf turns an outline level, which counts from zero and uses nine
// for ordinary text, into a heading level.
func levelOf(outline int) int {
	if outline < 0 || outline >= bodyOutline {
		return 0
	}
	return min(outline+1, maxHeading)
}

// numbering says which lists of a document are numbered.
type numbering struct {
	// abstracts maps the identifier of a list to that of its definition.
	abstracts map[string]string
	// formats maps a definition and a nesting depth to the way items
	// are marked there, such as "bullet" or "decimal".
	formats map[string]string
}

// readNumbering interprets the numbering part of a document, which may
// be nil when the document has no lists.
func readNumbering(root *node) *numbering {
	n := &numbering{abstracts: map[string]string{}, formats: map[string]string{}}
	if root == nil {
		return n
	}
	for _, c := range root.children {
		switch c.name {
		case "abstractNum":
			for _, lvl := range c.children {
				if lvl.name == "lvl" {
					n.formats[c.attr("abstractNumId")+"/"+lvl.attr("ilvl")] = lvl.val("numFmt")
				}
			}
		case "num":
			n.abstracts[c.attr("numId")] = c.val("abstractNumId")
		}
	}
	return n
}

// ordered reports whether the items of the list numID are numbered at
// the given nesting depth. Lists the document does not define are
// taken for bulleted ones.
func (n *numbering) ordered(numID string, level int) bool {
	switch n.formats[n.abstracts[numID]+"/"+strconv.Itoa(level)] {
	case "", "bullet", "none":
		return false
	}
	return true
}
