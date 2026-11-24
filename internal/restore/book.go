package restore

import (
	"fmt"
	"hash/crc32"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/manzanit0/athenaeum/internal/epub"
	"github.com/manzanit0/athenaeum/internal/meta"
)

// contentsDepth is how many levels of headings the table of contents
// lists.
const contentsDepth = 2

// stylesheet is the style sheet shared by every section.
const stylesheet = `body { margin: 0 5%; line-height: 1.5; }
h1, h2, h3 { line-height: 1.25; margin: 1.6em 0 0.6em; text-align: left; }
p { margin: 0 0 0.8em; }
ul { margin: 0 0 0.8em 1.5em; padding: 0; }
ul ul { margin-bottom: 0; }
li { margin: 0.25em 0; }
li.plain { list-style: none; }
section.titlepage { margin-top: 15%; text-align: center; }
section.titlepage h1 { margin: 0 0 0.8em; font-size: 1.7em; line-height: 1.3; letter-spacing: 0.04em; text-align: center; text-transform: uppercase; }
section.titlepage p { margin: 0 0 0.4em; }
p.subtitle { margin-bottom: 0; font-size: 1.1em; }
p.author { margin-top: 2em; font-size: 1.15em; font-weight: bold; }
blockquote.epigraph { margin: 3em 8% 0; }
p.imprint { font-size: 0.9em; }
p.first { margin-top: 3em; }
blockquote { margin: 0 0 0.8em 1.5em; }
aside.notes { margin-top: 2em; border-top: 1px solid; padding-top: 0.8em; font-size: 0.9em; }
aside.notes p { margin: 0 0 0.5em; }
a.noteref { vertical-align: super; font-size: 0.75em; line-height: 0; text-decoration: none; }
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

// imageTypes maps the extensions of the pictures a cover can be to
// their media types.
var imageTypes = map[string]string{"jpg": "image/jpeg", "png": "image/png", "gif": "image/gif"}

// languageTag matches the language tags EPUB accepts.
var languageTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`)

// Marks of the text of a block, as they stand once the text is escaped.
var (
	// boldMark matches text marked as bold.
	boldMark = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	// italicMark matches text marked as italic.
	italicMark = regexp.MustCompile(`\*([^*]+)\*`)
	// noteMark matches a reference to a note.
	noteMark = regexp.MustCompile(`\[\^([^\]\s]{1,12})\]`)
)

// section is one file of the book: a chapter, or what comes before the
// first one.
type section struct {
	// title is the title of the section.
	title string
	// elements holds the blocks of the section.
	elements []element
}

// written says what went into the book.
type written struct {
	// sections is the number of files the text is divided into.
	sections int
	// headings is the number of headings.
	headings int
	// linked is the number of references that lead to their note.
	linked int
}

// sectionFile returns the file name of the section with index i.
func sectionFile(i int) string {
	return fmt.Sprintf("section%04d.xhtml", i+1)
}

// divide splits the blocks of a book into sections, a new one starting
// at every heading of the highest level the book has.
func divide(elements []element, title string) []section {
	top := maxLevel + 1
	for _, e := range elements {
		if e.Kind == kindHeading {
			top = min(top, e.Level)
		}
	}
	var out []section
	for _, e := range elements {
		starts := e.Kind == kindHeading && e.Level == top
		if len(out) == 0 || starts && len(out[len(out)-1].elements) > 0 {
			out = append(out, section{title: title})
		}
		cur := &out[len(out)-1]
		if starts {
			cur.title = plain(e.Text)
		}
		cur.elements = append(cur.elements, e)
	}
	return out
}

// plain returns text without the marks of bold, italics and notes.
func plain(text string) string {
	text = noteMark.ReplaceAllString(text, "")
	text = boldMark.ReplaceAllString(text, "$1")
	return strings.TrimSpace(italicMark.ReplaceAllString(text, "$1"))
}

// renderer writes the sections of a book as XHTML.
type renderer struct {
	// notes holds the notes of the whole book, in reading order.
	notes []element
	// used marks the notes a reference already leads to.
	used map[string]bool
	// refs counts the references written, to tell them apart.
	refs int
	// linked counts the references that lead to their note.
	linked int
	// back maps the identifier of a note to the address of the
	// reference that leads to it.
	back map[string]string
}

// target returns the note that a reference with the given marker on
// the given page leads to: the first one with that marker, on that
// page or after it, that no reference leads to yet.
func (r *renderer) target(marker string, page int) (element, bool) {
	for _, note := range r.notes {
		if note.Marker == marker && note.page >= page && !r.used[note.id] {
			return note, true
		}
	}
	return element{}, false
}

// inline renders the text of a block: it escapes it, turns the marks
// of bold and italics into markup and links the references to notes.
// file maps the identifiers of notes to the files they are in, and here
// is the file the block is in.
func (r *renderer) inline(e element, file map[string]string, here string) string {
	text := html.EscapeString(e.Text)
	text = boldMark.ReplaceAllString(text, "<b>$1</b>")
	text = italicMark.ReplaceAllString(text, "<i>$1</i>")
	if e.Kind == kindNote {
		return noteMark.ReplaceAllString(text, "$1")
	}
	return noteMark.ReplaceAllStringFunc(text, func(match string) string {
		marker := html.UnescapeString(noteMark.FindStringSubmatch(match)[1])
		note, ok := r.target(marker, e.page)
		if !ok {
			return "<sup>" + html.EscapeString(marker) + "</sup>"
		}
		r.used[note.id] = true
		r.refs++
		r.linked++
		ref := fmt.Sprintf("r%d", r.refs)
		r.back[note.id] = here + "#" + ref
		return fmt.Sprintf(`<a class="noteref" epub:type="noteref" id="%s" href="%s#%s">%s</a>`,
			ref, file[note.id], note.id, html.EscapeString(marker))
	})
}

// numbered matches the start of an item that brings its own number or
// letter, such as "3.", "IV." or "a)", and needs no bullet before it.
var numbered = regexp.MustCompile(`^(\*{1,3})?\(?([0-9]{1,4}|[IVXLCDMivxlcdm]{1,7}|[A-Za-z])[.)]`)

// renderList renders the items from index at onwards that are nested
// at least as deep as level as one list, with the lists within its
// items inside them, and returns it with the index of the first item
// that is not part of it. texts holds the rendered text of the items.
func renderList(items []element, texts []string, at, level int) (string, int) {
	var sb strings.Builder
	sb.WriteString("<ul>\n")
	for at < len(items) && items[at].Level >= level {
		if items[at].Level > level {
			// An item nested deeper than the list has reached hangs from an item without text.
			inner, next := renderList(items, texts, at, level+1)
			sb.WriteString("<li class=\"plain\">\n" + inner + "</li>\n")
			at = next
			continue
		}
		if numbered.MatchString(items[at].Text) {
			sb.WriteString("<li class=\"plain\">")
		} else {
			sb.WriteString("<li>")
		}
		sb.WriteString(texts[at])
		at++
		if at < len(items) && items[at].Level > level {
			inner, next := renderList(items, texts, at, level+1)
			sb.WriteString("\n" + inner)
			at = next
		}
		sb.WriteString("</li>\n")
	}
	sb.WriteString("</ul>\n")
	return sb.String(), at
}

// render renders the section with index i in the language lang. The
// items that follow one another make up one list, nested as their
// levels say, and the notes of the section come at its end.
func (r *renderer) render(i int, sec section, lang string, file map[string]string) []byte {
	var sb, notes strings.Builder
	fmt.Fprintf(&sb, pageHead, lang, html.EscapeString(sec.title))
	var items []element
	var texts []string
	flush := func() {
		if len(items) > 0 {
			list, _ := renderList(items, texts, 0, 1)
			sb.WriteString(list)
			items, texts = nil, nil
		}
	}
	for _, e := range sec.elements {
		text := r.inline(e, file, sectionFile(i))
		switch e.Kind {
		case kindItem:
			items, texts = append(items, e), append(texts, text)
			continue
		case kindNote:
			marker := html.EscapeString(e.Marker)
			if ref, ok := r.back[e.id]; ok {
				marker = fmt.Sprintf(`<a href="%s">%s</a>`, ref, marker)
			}
			fmt.Fprintf(&notes, "<p id=\"%s\">%s %s</p>\n", e.id, marker, text)
			continue
		}
		flush()
		switch e.Kind {
		case kindHeading:
			fmt.Fprintf(&sb, "<h%d id=\"%s\">%s</h%d>\n", e.Level, e.id, text, e.Level)
		case kindQuote:
			fmt.Fprintf(&sb, "<blockquote><p>%s</p></blockquote>\n", text)
		default:
			fmt.Fprintf(&sb, "<p>%s</p>\n", text)
		}
	}
	flush()
	if notes.Len() > 0 {
		fmt.Fprintf(&sb, "<aside class=\"notes\" epub:type=\"footnotes\">\n%s</aside>\n", notes.String())
	}
	sb.WriteString(pageTail)
	return []byte(sb.String())
}

// titleFile is the file of the book that holds its title page.
const titleFile = "title.xhtml"

// front takes the blocks of the title page out of the blocks of a book
// and returns both. What the book prints twice, as on a half title and
// a title page, is kept once.
func front(elements []element) (title, body []element) {
	seen := map[string]bool{}
	for _, e := range elements {
		if !slices.Contains(titleKinds, e.Kind) {
			body = append(body, e)
			continue
		}
		if key := e.Kind + " " + letters(e.Text); !seen[key] {
			seen[key] = true
			title = append(title, e)
		}
	}
	return title, body
}

// titlePage renders the first page of the book the way publishers lay
// one out: the title, shown in capitals by the style sheet, then the
// subtitle, the authors, what the page says about them and the making
// of the book, the epigraph, and at the foot the publisher, place and
// year. What the book does not print is taken from its details. It returns nil
// for a book of which neither title nor author is known.
func (r *renderer) titlePage(blocks []element, details meta.Metadata, file map[string]string) []byte {
	of := func(kind string) []string {
		var out []string
		for _, e := range blocks {
			if e.Kind == kind {
				out = append(out, r.inline(e, file, titleFile))
			}
		}
		return out
	}
	escaped := func(texts ...string) []string {
		var out []string
		for _, text := range texts {
			if text != "" {
				out = append(out, html.EscapeString(text))
			}
		}
		return out
	}
	titles, authors, imprint := of(kindTitle), of(kindAuthor), of(kindImprint)
	if len(titles) == 0 {
		titles = escaped(details.Title)
	}
	if len(authors) == 0 {
		authors = escaped(details.Authors...)
	}
	if len(imprint) == 0 {
		year, _, _ := strings.Cut(details.Published, "-")
		imprint = escaped(details.Publisher, year)
	}
	if len(titles) == 0 && len(authors) == 0 {
		return nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, pageHead, details.Language, html.EscapeString(details.Title))
	sb.WriteString("<section class=\"titlepage\" epub:type=\"titlepage\">\n")
	for _, text := range titles {
		fmt.Fprintf(&sb, "<h1 class=\"title\">%s</h1>\n", text)
	}
	for _, text := range of(kindSubtitle) {
		fmt.Fprintf(&sb, "<p class=\"subtitle\">%s</p>\n", text)
	}
	for _, text := range authors {
		fmt.Fprintf(&sb, "<p class=\"author\">%s</p>\n", text)
	}
	for _, text := range of(kindDetail) {
		fmt.Fprintf(&sb, "<p class=\"detail\">%s</p>\n", text)
	}
	if epigraph := of(kindEpigraph); len(epigraph) > 0 {
		sb.WriteString("<blockquote class=\"epigraph\" epub:type=\"epigraph\">\n")
		for _, text := range epigraph {
			fmt.Fprintf(&sb, "<p>%s</p>\n", text)
		}
		sb.WriteString("</blockquote>\n")
	}
	for i, text := range imprint {
		class := "imprint"
		if i == 0 {
			class = "imprint first"
		}
		fmt.Fprintf(&sb, "<p class=\"%s\">%s</p>\n", class, text)
	}
	sb.WriteString("</section>\n" + pageTail)
	return []byte(sb.String())
}

// language returns the first of the candidates that is a language tag
// a book can declare, or the tag for an undetermined language.
func language(candidates ...string) string {
	for _, candidate := range candidates {
		if languageTag.MatchString(candidate) {
			return candidate
		}
	}
	return "und"
}

// writeBook writes the blocks of a book as an EPUB at dst: a title
// page, one file for every chapter, each with its notes at the end, and
// a table of contents made from the headings.
func writeBook(dst string, elements []element, details meta.Metadata, id, cover string) (written, error) {
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".restore-*.epub")
	if err != nil {
		return written{}, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	out := epub.NewWriter(tmp)
	out.Add("style.css", "text/css", []byte(fontRules()+stylesheet))
	if err := addFonts(out); err != nil {
		return written{}, err
	}
	details.Language = language(details.Language)
	title, elements := front(elements)
	sections := divide(elements, details.Title)
	r := &renderer{used: map[string]bool{}, back: map[string]string{}}
	file := map[string]string{}
	for i, sec := range sections {
		for _, e := range sec.elements {
			if e.Kind == kindNote {
				r.notes = append(r.notes, e)
				file[e.id] = sectionFile(i)
			}
		}
	}
	result := written{sections: len(sections)}
	var contents []epub.Entry
	page := r.titlePage(title, details, file)
	if page != nil {
		out.AddDocument(titleFile, page)
	}
	for i, sec := range sections {
		out.AddDocument(sectionFile(i), r.render(i, sec, details.Language, file))
		// Text ahead of the first chapter is a section of its own, and
		// has no heading to be listed by: it is listed by the title.
		if first := sec.elements[0]; first.Kind != kindHeading || first.Level > contentsDepth {
			contents = append(contents, epub.Entry{Title: sec.title, Href: sectionFile(i)})
		}
		for _, e := range sec.elements {
			if e.Kind != kindHeading {
				continue
			}
			result.headings++
			if e.Level <= contentsDepth {
				contents = append(contents, epub.Entry{
					Title: plain(e.Text), Href: sectionFile(i) + "#" + e.id, Level: e.Level - 1,
				})
			}
		}
	}
	if page != nil && result.headings > 0 {
		contents = append([]epub.Entry{{Title: "Title page", Href: titleFile}}, contents...)
	}
	result.linked = r.linked
	book := epub.Details{Metadata: details, ID: id, Contents: contents, Cover: addCover(out, cover)}
	if book.ID == "" {
		book.ID = fmt.Sprintf("athenaeum-%08x", crc32.ChecksumIEEE([]byte(details.Title+strings.Join(details.Authors, ","))))
	}
	if err := out.Close(book); err != nil {
		return written{}, err
	}
	if err := tmp.Close(); err != nil {
		return written{}, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return written{}, err
	}
	return result, os.Rename(tmp.Name(), dst)
}

// addCover adds the image at path to the book as its cover and returns
// the name it has there, or an empty string when there is none.
func addCover(out *epub.Writer, path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	ext := meta.ImageExt(data)
	if imageTypes[ext] == "" {
		return ""
	}
	out.Add("images/cover."+ext, imageTypes[ext], data)
	return "images/cover." + ext
}
