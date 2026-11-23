package docx

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

// maxDepth bounds how deeply the elements of a document may be nested.
const maxDepth = 96

// errTooDeep is returned for a document nested beyond maxDepth.
var errTooDeep = errors.New("docx: the document is nested too deeply")

// relationshipSpace ends the namespace of the attributes that refer to
// other parts of the document, such as the picture a drawing shows.
const relationshipSpace = "/relationships"

// unread lists the elements whose content is never read: what a newer
// element already says in another way, text that was deleted with
// changes tracked, and the formatting a change replaced.
var unread = map[string]bool{
	"Fallback": true, "del": true, "moveFrom": true,
	"rPrChange": true, "pPrChange": true, "tblPrChange": true, "sectPrChange": true,
}

// leftOut maps the elements that cannot be converted to the kind of
// thing they hold, which the report counts.
var leftOut = map[string]string{
	"txbxContent": noteTextBox, "object": noteObject, "oMath": noteEquation, "oMathPara": noteEquation,
}

// textual lists the elements whose character data is kept.
var textual = map[string]bool{"t": true, "instrText": true}

// node is an element of a part of a Word document, read whole so that
// it can be looked through more than once.
type node struct {
	// name is the name of the element without its namespace.
	name string
	// attrs holds the attributes by name. Those that refer to other
	// parts are kept under their name after "r:".
	attrs map[string]string
	// children holds the elements inside this one, in order.
	children []*node
	// text is the character data of an element that carries text.
	text string
}

// readNode reads the element that start opens, with everything inside
// it. It returns nil for an element that is not read, counting in notes
// the ones that cannot be converted.
func readNode(dec *xml.Decoder, start xml.StartElement, depth int, notes map[string]int) (*node, error) {
	name := start.Name.Local
	if kind, lost := leftOut[name]; lost || unread[name] {
		if lost {
			notes[kind]++
		}
		return nil, dec.Skip()
	}
	if depth > maxDepth {
		return nil, errTooDeep
	}
	n := &node{name: name, attrs: attributes(start)}
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child, err := readNode(dec, t, depth+1, notes)
			if err != nil {
				return nil, err
			}
			if child != nil {
				n.children = append(n.children, child)
			}
		case xml.CharData:
			if textual[name] {
				n.text += string(t)
			}
		case xml.EndElement:
			return n, nil
		}
	}
}

// attributes returns the attributes of an element by name.
func attributes(start xml.StartElement) map[string]string {
	if len(start.Attr) == 0 {
		return nil
	}
	attrs := make(map[string]string, len(start.Attr))
	for _, a := range start.Attr {
		key := a.Name.Local
		if strings.HasSuffix(a.Name.Space, relationshipSpace) {
			key = "r:" + key
		}
		attrs[key] = a.Value
	}
	return attrs
}

// readRoot reads the whole of the XML document on r as one element.
func readRoot(r io.Reader, notes map[string]int) (*node, error) {
	dec := newDecoder(r)
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := tok.(xml.StartElement); ok {
			return readNode(dec, start, 0, notes)
		}
	}
}

// newDecoder returns a decoder for the parts of a Word document, which
// reads names with their namespaces resolved.
func newDecoder(r io.Reader) *xml.Decoder {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	return dec
}

// attr returns the attribute of n called name, or an empty string. It
// is safe to call on a nil element.
func (n *node) attr(name string) string {
	if n == nil {
		return ""
	}
	return n.attrs[name]
}

// child returns the first element directly inside n called name, or
// nil. It is safe to call on a nil element.
func (n *node) child(name string) *node {
	if n == nil {
		return nil
	}
	for _, c := range n.children {
		if c.name == name {
			return c
		}
	}
	return nil
}

// val returns the "val" attribute of the element called name directly
// inside n, which is how Word writes most properties.
func (n *node) val(name string) string {
	return n.child(name).attr("val")
}

// find returns the first element called name anywhere inside n, or nil.
func (n *node) find(name string) *node {
	if n == nil {
		return nil
	}
	for _, c := range n.children {
		if c.name == name {
			return c
		}
		if found := c.find(name); found != nil {
			return found
		}
	}
	return nil
}

// toggle is a property that is on, off or not said.
type toggle int

const (
	// unsaid is a property the element does not mention.
	unsaid toggle = iota
	// on is a property that is set.
	on
	// off is a property that is explicitly cleared.
	off
)

// toggleOf reads the on-or-off property called name directly inside n:
// present without a value means on, and so does any value that is not
// one of the ways Word writes "no".
func toggleOf(n *node, name string) toggle {
	c := n.child(name)
	if c == nil {
		return unsaid
	}
	switch strings.ToLower(c.attr("val")) {
	case "0", "false", "off", "none":
		return off
	}
	return on
}

// or returns t, or fallback when t is not said.
func (t toggle) or(fallback toggle) toggle {
	if t == unsaid {
		return fallback
	}
	return t
}
