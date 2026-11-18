package preview

import (
	"archive/zip"
	"encoding/xml"
	"slices"
	"strings"

	"github.com/manzanit0/athenaeum/internal/meta"
)

// navReader collects the lines of the table of contents while walking an
// EPUB 3 navigation document.
type navReader struct {
	// base is the path of the navigation document inside the EPUB.
	base string
	// entries holds the lines found so far.
	entries []Entry
	// inside reports whether the walk is within the table of contents.
	inside bool
	// depth counts the lists open around the current position.
	depth int
	// href is the target of the link being read; empty outside links.
	href string
	// text collects the text of the link being read.
	text strings.Builder
}

// readNavigation returns the lines of the EPUB 3 table of contents at
// href, a reference found in the package document at opfPath.
func readNavigation(zr *zip.Reader, opfPath, href string) []Entry {
	name, ok := archivePath(opfPath, href)
	if !ok {
		return nil
	}
	f, err := zr.Open(name)
	if err != nil {
		return nil
	}
	defer f.Close()
	reader := navReader{base: name}
	d := meta.NewXMLDecoder(f)
	for !reader.done() {
		tok, err := d.Token()
		if err != nil {
			break
		}
		reader.read(tok)
	}
	return reader.entries
}

// done reports whether the table of contents has been read to its end.
func (n *navReader) done() bool {
	return n.inside && n.depth < 0
}

// read handles one token of the navigation document.
func (n *navReader) read(tok xml.Token) {
	switch t := tok.(type) {
	case xml.StartElement:
		n.start(t)
	case xml.EndElement:
		n.end(t.Name.Local)
	case xml.CharData:
		if n.href != "" {
			n.text.Write(t)
		}
	}
}

// start handles an opening tag.
func (n *navReader) start(t xml.StartElement) {
	switch {
	case t.Name.Local == "nav" && !n.inside:
		n.inside = slices.Contains(strings.Fields(attribute(t, "type")), "toc")
	case !n.inside:
	case t.Name.Local == "ol":
		n.depth++
	case t.Name.Local == "a":
		n.href = attribute(t, "href")
		n.text.Reset()
	}
}

// end handles a closing tag.
func (n *navReader) end(name string) {
	switch {
	case !n.inside:
	case name == "nav":
		n.depth = -1
	case name == "ol":
		n.depth--
	case name == "a" && n.href != "":
		if href, ok := resolve(n.base, n.href); ok {
			level := max(n.depth-1, 0)
			n.entries = append(n.entries, Entry{Title: clean(n.text.String()), Href: href, Level: level})
		}
		n.href = ""
	}
}

// attribute returns the value of the attribute of t with the local name
// name, or the empty string.
func attribute(t xml.StartElement, name string) string {
	for _, attr := range t.Attr {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}
