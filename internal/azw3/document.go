package azw3

import (
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"strings"

	"github.com/manzanit0/athenaeum/internal/kf8"
)

// target is a place in the EPUB a link points at.
type target struct {
	// name is the path of the document inside the EPUB.
	name string
	// fragment is the identifier within the document, if any.
	fragment string
}

// key returns the name the target is filed under.
func (t target) key() string {
	return t.name + "#" + t.fragment
}

// resolve splits ref, a reference found in the file base, into the path
// inside the EPUB it points at and its fragment. It reports false for
// references that leave the EPUB, such as web addresses.
func resolve(base, ref string) (name, fragment string, local bool) {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "", "", false
	}
	if u.Path == "" {
		return base, u.Fragment, true
	}
	name = path.Join(path.Dir(base), u.Path)
	if !fs.ValidPath(name) {
		return "", "", false
	}
	return name, u.Fragment, true
}

// newAid returns the next Kindle identifier.
func (c *converter) newAid() string {
	c.aids++
	return kf8.Base32(c.aids-1, 4)
}

// link points the href of the anchor n, found in the document doc, at
// its place in the book. Links to the web are kept; links to anything
// else in the EPUB that is not part of the reading order are removed.
func (c *converter) link(n *node, doc string) {
	href := n.get("href")
	if href == "" {
		return
	}
	name, fragment, local := resolve(doc, href)
	if !local {
		return
	}
	if _, known := c.files[name]; !known {
		n.remove("href")
		return
	}
	n.set("href", kf8.LinkPlaceholder(len(c.links)))
	c.links = append(c.links, target{name, fragment})
}

// picture points the image reference in the attribute name of n at the
// image stored in the book. It reports false when the image is unusable.
func (c *converter) picture(n *node, name, doc string) bool {
	address, ok := c.imageAddress(n.get(name), doc)
	if ok {
		n.set(name, address)
	}
	return ok
}

// adapt prepares the element n of the document doc for the Kindle. It
// reports false when the element has to be left out.
func (c *converter) adapt(n *node, doc string) bool {
	switch n.name {
	case "script":
		return false
	case "img":
		n.remove("srcset")
		if !c.picture(n, "src", doc) {
			return false
		}
	case "image":
		if !c.picture(n, "xlink:href", doc) && !c.picture(n, "href", doc) {
			return false
		}
	case "a":
		c.link(n, doc)
	}
	if style := n.get("style"); strings.Contains(style, "url(") {
		n.set("style", c.styleURLs(style, doc))
	}
	if splittable[n.name] || n.get("id") != "" || (n.name == "a" && n.get("name") != "") {
		n.aid = c.newAid()
	}
	c.adaptChildren(n, doc)
	return true
}

// adaptChildren prepares the content of n. An image that has to be left
// out is replaced by its alternative text.
func (c *converter) adaptChildren(n *node, doc string) {
	kept := n.children[:0]
	for _, child := range n.children {
		switch {
		case child.name == "" || c.adapt(child, doc):
			kept = append(kept, child)
		case child.name == "img" && child.get("alt") != "":
			kept = append(kept, &node{text: child.get("alt")})
		}
	}
	n.children = kept
}

// head returns the start of the skeleton of a document: everything up to
// its body tag. Linked stylesheets become flows of the book.
func (c *converter) head(html, head *node, doc string) *writer {
	w := &writer{}
	w.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(w, `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="%s"`, epubSpace)
	lang := html.get("lang")
	if lang == "" {
		lang = html.get("xml:lang")
	}
	if lang != "" {
		fmt.Fprintf(w, ` lang="%s"`, valueEscaper.Replace(lang))
	}
	w.WriteString(">\n<head>\n")
	w.WriteString(`<meta http-equiv="Content-Type" content="text/html; charset=UTF-8"/>` + "\n")
	title := c.book.Title
	if head != nil {
		if found := head.find("title"); found != nil && strings.TrimSpace(found.content()) != "" {
			title = strings.TrimSpace(found.content())
		}
		c.styles(w, head, doc)
	}
	fmt.Fprintf(w, "<title>%s</title>\n</head>\n", textEscaper.Replace(title))
	return w
}

// styles writes the stylesheets of the head of a document: links to the
// flows of the linked ones and the embedded ones in place.
func (c *converter) styles(w *writer, head *node, doc string) {
	for _, n := range head.children {
		switch {
		case n.name == "link" && strings.Contains(strings.ToLower(n.get("rel")), "stylesheet"):
			name, _, local := resolve(doc, n.get("href"))
			if number, ok := c.flow(name); local && ok {
				fmt.Fprintf(w, `<link rel="stylesheet" type="text/css" href="%s"/>`+"\n", flowAddress(number))
			}
		case n.name == "style":
			css := cssFontFace.ReplaceAllString(n.content(), "")
			css = cssDirective.ReplaceAllString(css, "")
			fmt.Fprintf(w, "<style type=\"text/css\">%s</style>\n", textEscaper.Replace(c.styleURLs(css, doc)))
		}
	}
}

// parts returns the html, head and body elements of a parsed document.
// A document without a body gets one holding all of its content.
func parts(root *node) (html, head, body *node) {
	html = root.find("html")
	if html == nil {
		html = root
	}
	head, body = html.find("head"), html.find("body")
	if body == nil {
		body = &node{name: "body"}
		for _, child := range html.children {
			if child != head {
				body.children = append(body.children, child)
			}
		}
	}
	return html, head, body
}

// document turns the document at name in the EPUB into the file with the
// given index, recording where its identified elements end up.
func (c *converter) document(index int, name string) (kf8.File, error) {
	data, err := c.read(name)
	if err != nil {
		return kf8.File{}, err
	}
	root, err := parse(data)
	if err != nil {
		return kf8.File{}, fmt.Errorf("%s is not well formed: %w", name, err)
	}
	html, head, body := parts(root)
	body.aid = c.newAid()
	c.adaptChildren(body, name)
	if len(body.children) == 0 {
		body.children = []*node{{name: "div", aid: c.newAid()}}
	}
	fb := &fileBuilder{located: func(n *node, offset int) {
		c.locate(index, name, n, offset)
	}}
	start := c.head(html, head, name)
	start.open(body)
	fb.structure(start)
	fb.fill(body)
	end := &writer{}
	end.close(body)
	end.WriteString("\n</html>")
	fb.structure(end)
	return fb.file(), nil
}

// locate records where the element n of the document name ended up, so
// links to its identifiers can be resolved.
func (c *converter) locate(index int, name string, n *node, offset int) {
	position := kf8.Position{File: index, Offset: offset}
	ids := []string{n.get("id")}
	if n.name == "a" {
		ids = append(ids, n.get("name"))
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, taken := c.anchors[target{name, id}.key()]; !taken {
			c.anchors[target{name, id}.key()] = position
		}
	}
}
