package reflow

import (
	"bytes"
	"fmt"
	"html"
	"strings"
)

// stylesheet is the style sheet shared by every section.
const stylesheet = `body { margin: 0 5%; line-height: 1.5; }
h1, h2, h3 { line-height: 1.25; margin: 1.6em 0 0.6em; text-align: left; }
p { margin: 0 0 0.8em; }
ul, ol { margin: 0 0 0.8em 1.5em; padding: 0; }
div.figure { margin: 1em 0; text-align: center; }
div.figure img { max-width: 100%; height: auto; }
div.cover { margin: 0; text-align: center; }
div.cover img { max-width: 100%; max-height: 100%; }
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

// escape prepares text for use in XML: it drops the characters XML
// cannot hold and escapes the ones that have a meaning.
func escape(text string) string {
	text = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0xFFFE || r == 0xFFFF || r == 0xFFFD {
			return -1
		}
		return r
	}, strings.ToValidUTF8(text, ""))
	return html.EscapeString(text)
}

// tidy collapses the white space inside spans and trims it at both
// ends of the whole.
func tidy(spans []span) []span {
	out := make([]span, 0, len(spans))
	space := true
	for _, s := range spans {
		var sb strings.Builder
		for _, r := range s.text {
			if r == ' ' || r == '\n' {
				if !space {
					sb.WriteByte(' ')
				}
				space = true
				continue
			}
			sb.WriteRune(r)
			space = false
		}
		if sb.Len() > 0 {
			out = append(out, span{text: sb.String(), bold: s.bold, italic: s.italic})
		}
	}
	if n := len(out); n > 0 {
		out[n-1].text = strings.TrimRight(out[n-1].text, " ")
	}
	return out
}

// inline renders spans as XHTML.
func inline(spans []span) string {
	var sb strings.Builder
	for _, s := range tidy(spans) {
		text := escape(s.text)
		if s.italic {
			text = "<i>" + text + "</i>"
		}
		if s.bold {
			text = "<b>" + text + "</b>"
		}
		sb.WriteString(text)
	}
	return sb.String()
}

// anchor returns the id attribute of an element, with a leading space,
// or nothing when the element has no id.
func anchor(e element) string {
	if e.id == "" {
		return ""
	}
	return fmt.Sprintf(` id="%s"`, e.id)
}

// listTag returns the tag of the list an element of the given kind
// belongs to, or the empty string for elements outside lists.
func listTag(k kind) string {
	if k == bullet {
		return "ul"
	}
	return ""
}

// renderElement renders one element as XHTML.
func renderElement(e element) string {
	switch e.kind {
	case heading:
		return fmt.Sprintf("<h%[1]d%[2]s>%[3]s</h%[1]d>\n", e.level, anchor(e), inline(e.spans))
	case figure:
		return fmt.Sprintf("<div class=\"figure\"%s><img src=\"%s\" alt=\"\"/></div>\n", anchor(e), e.image)
	case bullet:
		return fmt.Sprintf("<li%s>%s</li>\n", anchor(e), inline(e.spans))
	}
	return fmt.Sprintf("<p%s>%s</p>\n", anchor(e), inline(e.spans))
}

// renderSection renders a section as an XHTML file in the language
// lang.
func renderSection(sec section, lang string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, pageHead, lang, escape(sec.title))
	list := ""
	for _, e := range sec.elements {
		if tag := listTag(e.kind); tag != list {
			if list != "" {
				fmt.Fprintf(&buf, "</%s>\n", list)
			}
			if tag != "" {
				fmt.Fprintf(&buf, "<%s>\n", tag)
			}
			list = tag
		}
		buf.WriteString(renderElement(e))
	}
	if list != "" {
		fmt.Fprintf(&buf, "</%s>\n", list)
	}
	buf.WriteString(pageTail)
	return buf.Bytes()
}

// renderCover renders the page that shows the cover image.
func renderCover(image, lang string) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, pageHead, lang, "Cover")
	fmt.Fprintf(&buf, "<div class=\"cover\"><img src=\"%s\" alt=\"Cover\"/></div>\n", image)
	buf.WriteString(pageTail)
	return buf.Bytes()
}
