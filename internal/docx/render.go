package docx

import (
	"context"
	"fmt"
	"html"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/manzanit0/athenaeum/internal/epub"
	"github.com/manzanit0/athenaeum/internal/meta"
)

// maxListDepth is the deepest a list is nested in the book.
const maxListDepth = 8

// contentsDepth is how many levels of headings below the highest one
// the table of contents lists.
const contentsDepth = 2

// The sizes that tell headings from text in a document that marks
// neither with styles.
const (
	// headingRatio is how much larger than ordinary text a paragraph
	// has to be set to be taken for a heading.
	headingRatio = 1.25
	// maxHeadingLength is the longest a paragraph taken for a heading
	// may be, in bytes.
	maxHeadingLength = 160
	// inferredLevels is how many levels of headings are told apart by
	// size.
	inferredLevels = 3
)

// notesFile is the file of the book that holds its notes.
const notesFile = "notes.xhtml"

// imageTypes maps the extensions of the pictures a book can show to
// their media types.
var imageTypes = map[string]string{"jpg": "image/jpeg", "png": "image/png", "gif": "image/gif"}

// languageTag matches the language tags EPUB accepts.
var languageTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// unsafeID matches what an identifier in XHTML may not contain.
var unsafeID = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// stylesheet is the style sheet shared by every section.
const stylesheet = `body { margin: 0 5%; line-height: 1.5; }
h1, h2, h3, h4, h5, h6 { line-height: 1.25; margin: 1.6em 0 0.6em; text-align: left; }
p { margin: 0 0 0.8em; }
ul, ol { margin: 0 0 0.8em 1.5em; padding: 0; }
blockquote { margin: 0 0 0.8em 1.5em; }
table { border-collapse: collapse; margin: 0 0 0.8em; }
td { border: 1px solid; padding: 0.2em 0.5em; vertical-align: top; }
td p { margin: 0; }
.center { text-align: center; }
.right { text-align: right; }
div.figure { margin: 1em 0; text-align: center; }
img { max-width: 100%; height: auto; }
div.note { margin: 0 0 0.8em; }
`

// pageHead is the start of every XHTML file, up to the title.
const pageHead = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="%[1]s" lang="%[1]s">
<head>
<meta charset="UTF-8"/>
<title>%[2]s</title>
<link rel="stylesheet" type="text/css" href="style.css"/>
</head>
<body>
`

// pageTail is the end of every XHTML file.
const pageTail = "</body>\n</html>\n"

// languageOf returns the first of the candidates that is a language
// tag a book can declare, or the tag for an undetermined language.
func languageOf(candidates ...string) string {
	for _, candidate := range candidates {
		if languageTag.MatchString(candidate) {
			return candidate
		}
	}
	return "und"
}

// escape prepares text for use in XML: it drops the characters XML
// cannot hold and escapes the ones that have a meaning.
func escape(text string) string {
	text = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0xFFFE || r == 0xFFFF {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, ""))
	return html.EscapeString(text)
}

// plain returns the text of the spans without its formatting.
func plain(spans []span) string {
	var sb strings.Builder
	for _, s := range spans {
		if s.kind == spanText {
			sb.WriteString(s.text)
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}

// sizeOf returns the size the text of a paragraph is set in when all of
// it has one size, and zero otherwise.
func sizeOf(b block) int {
	size := 0
	for _, s := range b.spans {
		if s.kind != spanText || strings.TrimSpace(s.text) == "" {
			continue
		}
		if s.size == 0 || size != 0 && s.size != size {
			return 0
		}
		size = s.size
	}
	return size
}

// inferHeadings gives heading levels to the paragraphs of a document
// that marks no headings with styles: short paragraphs set clearly
// larger than most of the text, the largest size first. A document
// that marks its headings is returned as it is.
func inferHeadings(blocks []block) []block {
	weight := map[int]int{}
	for _, b := range blocks {
		if b.level > 0 {
			return blocks
		}
		if size := sizeOf(b); size > 0 {
			weight[size] += len(plain(b.spans))
		}
	}
	body := 0
	for size, n := range weight {
		if n > weight[body] || n == weight[body] && size < body {
			body = size
		}
	}
	var larger []int
	for size := range weight {
		if float64(size) >= headingRatio*float64(body) {
			larger = append(larger, size)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(larger)))
	levels := map[int]int{}
	for i, size := range larger[:min(len(larger), inferredLevels)] {
		levels[size] = i + 1
	}
	for i := range blocks {
		b := &blocks[i]
		level := levels[sizeOf(*b)]
		if level == 0 || b.rows != nil || b.listed || len(plain(b.spans)) > maxHeadingLength {
			continue
		}
		b.level = level
		for j := range b.spans {
			b.spans[j].bold = false
		}
	}
	return blocks
}

// divide splits the blocks of a document into the sections of a book:
// a new one starts at every heading of the highest level the document
// uses and at every paragraph that starts a page.
func divide(blocks []block) [][]block {
	top := 0
	for _, b := range blocks {
		if b.level > 0 && (top == 0 || b.level < top) {
			top = b.level
		}
	}
	var sections [][]block
	var current []block
	for _, b := range blocks {
		if (b.fresh || b.level > 0 && b.level == top) && len(current) > 0 {
			sections = append(sections, current)
			current = nil
		}
		current = append(current, b)
	}
	if len(current) > 0 || len(sections) == 0 {
		sections = append(sections, current)
	}
	return sections
}

// sectionFile returns the file name of the section with index i.
func sectionFile(i int) string {
	return fmt.Sprintf("section%04d.xhtml", i+1)
}

// anchorID returns the identifier an anchor of the document has in the
// book.
func anchorID(name string) string {
	return "bm-" + unsafeID.ReplaceAllString(name, "_")
}

// write adds the style sheet, the sections and the notes to the book
// and returns its table of contents.
func (c *conversion) write(ctx context.Context, sections [][]block, title string) ([]epub.Entry, error) {
	c.out.Add("style.css", "text/css", []byte(stylesheet))
	for i, blocks := range sections {
		c.locate(blocks, sectionFile(i))
	}
	top := topLevel(sections)
	var contents []epub.Entry
	for i, blocks := range sections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p := &page{c: c, file: sectionFile(i), top: top}
		p.blocks(blocks)
		c.out.AddDocument(p.file, p.bytes(title))
		contents = append(contents, p.entries...)
	}
	if len(c.cited) > 0 {
		p := &page{c: c, file: notesFile, top: top}
		p.notes()
		c.out.AddDocument(notesFile, p.bytes("Notes"))
		contents = append(contents, epub.Entry{Title: "Notes", Href: notesFile})
	}
	if len(contents) == 0 {
		contents = []epub.Entry{{Title: title, Href: sectionFile(0)}}
	}
	return contents, nil
}

// topLevel returns the highest heading level the sections use, or one.
func topLevel(sections [][]block) int {
	top := 0
	for _, blocks := range sections {
		for _, b := range blocks {
			if b.level > 0 && (top == 0 || b.level < top) {
				top = b.level
			}
		}
	}
	return max(top, 1)
}

// locate notes which file every anchor in the blocks ends up in, the
// anchors inside tables included.
func (c *conversion) locate(blocks []block, file string) {
	for _, b := range blocks {
		for _, s := range b.spans {
			if s.kind == spanAnchor {
				c.anchors[s.ref] = file
			}
		}
		for _, row := range b.rows {
			for _, cell := range row {
				c.locate(cell.blocks, file)
			}
		}
	}
}

// image returns the name in the book of the picture a reference of the
// document points to, writing the picture the first time it is asked
// for. Pictures a book cannot show give an empty string and are
// counted.
func (c *conversion) image(id string) string {
	if name, seen := c.images[id]; seen {
		return name
	}
	c.images[id] = ""
	rel, ok := c.rels[id]
	if !ok || rel.external {
		c.notes[noteImage]++
		return ""
	}
	f, err := c.pack.open(rel.target, maxImageBytes)
	if err != nil {
		c.notes[noteImage]++
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	ext := meta.ImageExt(data)
	if err != nil || imageTypes[ext] == "" {
		c.notes[noteImage]++
		return ""
	}
	name := fmt.Sprintf("images/image%04d.%s", len(c.images), ext)
	c.out.Add(name, imageTypes[ext], data)
	c.images[id] = name
	return name
}

// cite returns the number of the note with the given key, counting it
// among the cited ones the first time, and remembers the file that
// cites it. It returns zero for a note the document does not have.
func (c *conversion) cite(key, file string) int {
	if _, known := c.footnotes[key]; !known {
		return 0
	}
	for i, cited := range c.cited {
		if cited == key {
			return i + 1
		}
	}
	c.cited = append(c.cited, key)
	c.origin[key] = file
	return len(c.cited)
}

// list is a list that is open while a page is written.
type list struct {
	// tag is "ul" or "ol".
	tag string
	// numID identifies the list in the document.
	numID string
}

// page writes one XHTML file of the book.
type page struct {
	// c is the conversion the page belongs to.
	c *conversion
	// file is the name of the file.
	file string
	// top is the highest heading level of the book.
	top int
	// sb receives the body of the file.
	sb strings.Builder
	// lists holds the lists that are open, outermost first.
	lists []list
	// entries holds the lines the page adds to the table of contents.
	entries []epub.Entry
	// plainNotes reports whether note references are written as plain
	// numbers, as they are inside the notes themselves.
	plainNotes bool
}

// bytes returns the finished file, under the given title.
func (p *page) bytes(title string) []byte {
	return []byte(fmt.Sprintf(pageHead, p.c.language, escape(title)) + p.sb.String() + pageTail)
}

// blocks writes paragraphs and tables, closing the lists left open.
func (p *page) blocks(blocks []block) {
	for _, b := range blocks {
		p.block(b)
	}
	p.closeLists(0)
}

// block writes one paragraph or table.
func (p *page) block(b block) {
	switch {
	case b.rows != nil:
		p.closeLists(0)
		p.table(b.rows)
	case b.listed:
		p.item(b)
	case b.level > 0:
		p.closeLists(0)
		p.heading(b)
	default:
		p.closeLists(0)
		p.paragraph(b)
	}
}

// closeLists closes the open lists down to the given number of them.
func (p *page) closeLists(keep int) {
	for len(p.lists) > keep {
		fmt.Fprintf(&p.sb, "</li>\n</%s>\n", p.lists[len(p.lists)-1].tag)
		p.lists = p.lists[:len(p.lists)-1]
	}
}

// item writes a paragraph as an item of a list, opening and closing
// lists so that the item sits at its depth in the right list.
func (p *page) item(b block) {
	want := list{tag: "ul", numID: b.numID}
	if b.ordered {
		want.tag = "ol"
	}
	p.closeLists(b.depth + 1)
	switch {
	case len(p.lists) == b.depth+1 && p.lists[b.depth] == want:
		p.sb.WriteString("</li>\n<li>")
	case len(p.lists) == b.depth+1:
		p.closeLists(b.depth)
	}
	for len(p.lists) < b.depth+1 {
		fmt.Fprintf(&p.sb, "<%s>\n<li>", want.tag)
		p.lists = append(p.lists, want)
	}
	p.inline(b.spans)
}

// heading writes a heading with an identifier and, when it is high
// enough, a line in the table of contents.
func (p *page) heading(b block) {
	p.c.headings++
	id := "heading-" + strconv.Itoa(p.c.headings)
	level := min(max(b.level-p.top+1, 1), maxHeading)
	fmt.Fprintf(&p.sb, "<h%d id=\"%s\"%s>", level, id, class(b.align))
	p.inline(b.spans)
	fmt.Fprintf(&p.sb, "</h%d>\n", level)
	if title := plain(b.spans); title != "" && level <= contentsDepth+1 {
		p.entries = append(p.entries, epub.Entry{Title: title, Href: p.file + "#" + id, Level: level - 1})
	}
}

// class returns the class attribute for a way of setting text, or an
// empty string for the ordinary way.
func class(align string) string {
	if align == "" {
		return ""
	}
	return ` class="` + align + `"`
}

// paragraph writes an ordinary paragraph: as a figure when it only
// shows a picture, as a quotation when its style says so.
func (p *page) paragraph(b block) {
	switch {
	case onlyPictures(b.spans):
		p.sb.WriteString("<div class=\"figure\">")
		p.inline(b.spans)
		p.sb.WriteString("</div>\n")
	case b.quote:
		p.sb.WriteString("<blockquote><p>")
		p.inline(b.spans)
		p.sb.WriteString("</p></blockquote>\n")
	default:
		fmt.Fprintf(&p.sb, "<p%s>", class(b.align))
		p.inline(b.spans)
		p.sb.WriteString("</p>\n")
	}
}

// onlyPictures reports whether the spans show pictures and no text.
func onlyPictures(spans []span) bool {
	pictures := false
	for _, s := range spans {
		switch {
		case s.kind == spanImage:
			pictures = true
		case s.kind == spanText && strings.TrimSpace(s.text) != "", s.kind == spanNote:
			return false
		}
	}
	return pictures
}

// table writes a table, cell by cell.
func (p *page) table(rows [][]cell) {
	p.sb.WriteString("<table>\n")
	for _, row := range rows {
		p.sb.WriteString("<tr>")
		for _, cell := range row {
			if cell.span > 1 {
				fmt.Fprintf(&p.sb, "<td colspan=\"%d\">", cell.span)
			} else {
				p.sb.WriteString("<td>")
			}
			inner := &page{c: p.c, file: p.file, top: p.top, plainNotes: p.plainNotes}
			for _, b := range cell.blocks {
				b.level = 0
				inner.block(b)
			}
			inner.closeLists(0)
			p.sb.WriteString(inner.sb.String())
			p.sb.WriteString("</td>")
		}
		p.sb.WriteString("</tr>\n")
	}
	p.sb.WriteString("</table>\n")
}

// inline writes the spans of a paragraph.
func (p *page) inline(spans []span) {
	for _, s := range spans {
		switch s.kind {
		case spanText:
			p.text(s)
		case spanBreak:
			p.sb.WriteString("<br/>")
		case spanImage:
			if name := p.c.image(s.ref); name != "" {
				fmt.Fprintf(&p.sb, "<img src=\"%s\" alt=\"%s\"/>", name, escape(s.alt))
			}
		case spanNote:
			p.reference(s.ref)
		case spanAnchor:
			fmt.Fprintf(&p.sb, "<a id=\"%s\"></a>", anchorID(s.ref))
		}
	}
}

// text writes a run of text with its formatting and its link.
func (p *page) text(s span) {
	var open, shut []string
	tag := func(name string, wanted bool) {
		if wanted {
			open = append(open, "<"+name+">")
			shut = append([]string{"</" + name + ">"}, shut...)
		}
	}
	href := p.href(s.href)
	if href != "" {
		open = append(open, `<a href="`+escape(href)+`">`)
		shut = append(shut, "</a>")
	}
	tag("b", s.bold)
	tag("i", s.italic)
	tag("u", s.underline && href == "")
	tag("s", s.strike)
	tag(s.vert, s.vert != "")
	p.sb.WriteString(strings.Join(open, "") + escape(s.text) + strings.Join(shut, ""))
}

// href returns the address a link of the document has in the book: the
// file and identifier of an anchor, an address outside the document as
// it is, and nothing for an anchor the document does not have.
func (p *page) href(target string) string {
	name, internal := strings.CutPrefix(target, "#")
	if !internal {
		return target
	}
	file, known := p.c.anchors[name]
	if !known {
		return ""
	}
	return file + "#" + anchorID(name)
}

// reference writes the number of a note as a link to it, or as a plain
// number inside the notes themselves.
func (p *page) reference(key string) {
	if p.plainNotes {
		return
	}
	n := p.c.cite(key, p.file)
	if n == 0 {
		return
	}
	fmt.Fprintf(&p.sb, "<sup><a id=\"ref-%[1]d\" href=\"%[2]s#note-%[1]d\" epub:type=\"noteref\">%[1]d</a></sup>",
		n, notesFile)
}

// notes writes the notes that were cited, in the order they were cited,
// each with its number as a link back to where it is cited.
func (p *page) notes() {
	p.plainNotes = true
	p.sb.WriteString("<h1>Notes</h1>\n")
	for i, key := range p.c.cited {
		fmt.Fprintf(&p.sb, "<div class=\"note\" id=\"note-%[1]d\">\n<p><a href=\"%[2]s#ref-%[1]d\">%[1]d</a></p>\n",
			i+1, p.c.origin[key])
		for _, b := range p.c.footnotes[key] {
			b.level = 0
			p.block(b)
		}
		p.closeLists(0)
		p.sb.WriteString("</div>\n")
	}
}
