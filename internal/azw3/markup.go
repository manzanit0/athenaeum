package azw3

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/manzanit0/athenaeum/internal/meta"
)

// Namespaces that matter when reading EPUB markup.
const (
	// svgSpace is the namespace of SVG elements.
	svgSpace = "http://www.w3.org/2000/svg"
	// mathSpace is the namespace of MathML elements.
	mathSpace = "http://www.w3.org/1998/Math/MathML"
	// xlinkSpace is the namespace of the links inside SVG.
	xlinkSpace = "http://www.w3.org/1999/xlink"
	// xmlSpace is the namespace of the xml: attributes.
	xmlSpace = "http://www.w3.org/XML/1998/namespace"
	// epubSpace is the namespace of the epub: attributes.
	epubSpace = "http://www.idpf.org/2007/ops"
)

// attributePrefixes maps the namespaces of the attributes worth keeping,
// in their declared and undeclared forms, to the prefix to write.
var attributePrefixes = map[string]string{
	"":         "",
	xmlSpace:   "xml:",
	"xml":      "xml:",
	xlinkSpace: "xlink:",
	"xlink":    "xlink:",
	epubSpace:  "epub:",
	"epub":     "epub:",
}

// voidElements lists the elements that never have content.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true,
	"input": true, "link": true, "meta": true, "param": true, "source": true, "track": true,
	"wbr": true,
}

// attr is one attribute of an element.
type attr struct {
	// name is the attribute name, with its prefix.
	name string
	// value is the unescaped value.
	value string
}

// node is an element or a run of text in a parsed document.
type node struct {
	// name is the element name; empty for text.
	name string
	// space is the namespace of the element.
	space string
	// attrs holds the attributes of the element.
	attrs []attr
	// text is the unescaped content of a text node.
	text string
	// children holds the content of the element.
	children []*node
	// aid is the Kindle identifier given to the element, if any.
	aid string
}

// get returns the value of the attribute name.
func (n *node) get(name string) string {
	for _, a := range n.attrs {
		if a.name == name {
			return a.value
		}
	}
	return ""
}

// set replaces the value of the attribute name, adding it when missing.
func (n *node) set(name, value string) {
	for i := range n.attrs {
		if n.attrs[i].name == name {
			n.attrs[i].value = value
			return
		}
	}
	n.attrs = append(n.attrs, attr{name, value})
}

// remove deletes the attribute name.
func (n *node) remove(name string) {
	for i := range n.attrs {
		if n.attrs[i].name == name {
			n.attrs = append(n.attrs[:i], n.attrs[i+1:]...)
			return
		}
	}
}

// find returns the first element called name among the descendants of n,
// searching depth first.
func (n *node) find(name string) *node {
	for _, child := range n.children {
		if child.name == name {
			return child
		}
		if found := child.find(name); found != nil {
			return found
		}
	}
	return nil
}

// content returns the text inside n with the markup removed.
func (n *node) content() string {
	if n.name == "" {
		return n.text
	}
	var sb strings.Builder
	for _, child := range n.children {
		sb.WriteString(child.content())
	}
	return sb.String()
}

// attributes converts the attributes of an element, dropping namespace
// declarations and attributes from namespaces the Kindle has no use for.
func attributes(in []xml.Attr) []attr {
	var out []attr
	for _, a := range in {
		prefix, known := attributePrefixes[a.Name.Space]
		if !known || a.Name.Local == "xmlns" {
			continue
		}
		out = append(out, attr{prefix + a.Name.Local, a.Value})
	}
	return out
}

// element converts a start tag into a node. Elements that open an SVG
// or MathML island get the namespace declarations they need.
func element(t xml.StartElement, parent *node) *node {
	n := &node{name: t.Name.Local, space: t.Name.Space, attrs: attributes(t.Attr)}
	foreign := n.space == svgSpace || n.space == mathSpace
	if foreign && (parent == nil || parent.space != n.space) {
		n.attrs = append(n.attrs, attr{"xmlns", n.space})
		if n.space == svgSpace {
			n.attrs = append(n.attrs, attr{"xmlns:xlink", xlinkSpace})
		}
	}
	return n
}

// parse reads an XHTML document into a tree and returns its root.
func parse(data []byte) (*node, error) {
	d := meta.NewXMLDecoder(bytes.NewReader(data))
	d.AutoClose = xml.HTMLAutoClose
	root := &node{name: "#document"}
	stack := []*node{root}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := element(t, top)
			top.children = append(top.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if len(stack) > 1 {
				top.children = append(top.children, &node{text: string(t)})
			}
		}
	}
	if len(root.children) == 0 {
		return nil, fmt.Errorf("the document is empty")
	}
	return root, nil
}

// textEscaper escapes text content.
var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// valueEscaper escapes attribute values.
var valueEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", `"`, "&quot;")

// mark records where an element with a Kindle identifier starts.
type mark struct {
	// n is the element.
	n *node
	// offset is the position of its start tag in the rendered markup.
	offset int
}

// writer renders nodes as markup.
type writer struct {
	bytes.Buffer
	// marks lists the identified elements rendered so far.
	marks []mark
}

// open writes the start tag of n. Void elements are closed at once.
func (w *writer) open(n *node) {
	if n.aid != "" {
		w.marks = append(w.marks, mark{n, w.Len()})
	}
	w.WriteByte('<')
	w.WriteString(n.name)
	for _, a := range n.attrs {
		fmt.Fprintf(w, ` %s="%s"`, a.name, valueEscaper.Replace(a.value))
	}
	if n.aid != "" {
		fmt.Fprintf(w, ` aid="%s"`, n.aid)
	}
	if voidElements[n.name] {
		w.WriteByte('/')
	}
	w.WriteByte('>')
}

// close writes the end tag of n, which void elements do not have.
func (w *writer) close(n *node) {
	if !voidElements[n.name] {
		fmt.Fprintf(w, "</%s>", n.name)
	}
}

// node writes n and everything inside it.
func (w *writer) node(n *node) {
	if n.name == "" {
		w.WriteString(textEscaper.Replace(n.text))
		return
	}
	w.open(n)
	for _, child := range n.children {
		w.node(child)
	}
	w.close(n)
}
