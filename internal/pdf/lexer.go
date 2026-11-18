// Package pdf reads PDF files: their objects, pages, text with positions,
// images, document information and outline. It only reads; nothing here
// renders or writes PDFs.
package pdf

import (
	"strconv"
)

// maxDepth bounds how deeply arrays and dictionaries may nest.
const maxDepth = 64

// Name is a PDF name object without its leading slash.
type Name string

// Ref is a reference to an indirect object.
type Ref struct {
	// Num is the object number.
	Num int
	// Gen is the generation number.
	Gen int
}

// Dict is a PDF dictionary.
type Dict map[Name]any

// Array is a PDF array.
type Array []any

// String is a PDF string object holding its raw bytes.
type String string

// keyword is a bare word such as an operator or a structural marker.
type keyword string

// lexer reads PDF objects from a byte slice.
type lexer struct {
	// data is the text being read.
	data []byte
	// pos is the offset of the next unread byte.
	pos int
	// refs enables reading "1 0 R" as a reference.
	refs bool
	// depth counts the containers open around the current position.
	depth int
}

// isSpace reports whether c is PDF white space.
func isSpace(c byte) bool {
	return c == 0 || c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '
}

// isDelim reports whether c is a PDF delimiter.
func isDelim(c byte) bool {
	switch c {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	}
	return false
}

// skipSpace moves past white space and comments.
func (l *lexer) skipSpace() {
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		switch {
		case isSpace(c):
			l.pos++
		case c == '%':
			for l.pos < len(l.data) && l.data[l.pos] != '\n' && l.data[l.pos] != '\r' {
				l.pos++
			}
		default:
			return
		}
	}
}

// has reports whether the unread data starts with s.
func (l *lexer) has(s string) bool {
	return l.pos+len(s) <= len(l.data) && string(l.data[l.pos:l.pos+len(s)]) == s
}

// word returns the run of regular characters at the current position and
// moves past it.
func (l *lexer) word() string {
	start := l.pos
	for l.pos < len(l.data) && !isSpace(l.data[l.pos]) && !isDelim(l.data[l.pos]) {
		l.pos++
	}
	return string(l.data[start:l.pos])
}

// object reads the next object or keyword. It reports false at the end
// of the data.
func (l *lexer) object() (any, bool) {
	l.skipSpace()
	if l.pos >= len(l.data) {
		return nil, false
	}
	switch c := l.data[l.pos]; {
	case c == '/':
		l.pos++
		return l.name(), true
	case c == '(':
		return l.literal(), true
	case c == '<' && l.has("<<"):
		return l.dict(), true
	case c == '<':
		return l.hex(), true
	case c == '[':
		return l.array(), true
	case c == '>' && l.has(">>"):
		l.pos += 2
		return keyword(">>"), true
	case isDelim(c):
		l.pos++
		return keyword(c), true
	case c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9'):
		return l.number(), true
	}
	return l.bare(), true
}

// bare reads a keyword, turning the literals true, false and null into
// their values.
func (l *lexer) bare() any {
	switch w := l.word(); w {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	case "":
		l.pos++
		return keyword("")
	default:
		return keyword(w)
	}
}

// name reads the characters of a name, decoding #xx escapes.
func (l *lexer) name() Name {
	raw := l.word()
	for i := 0; i < len(raw); i++ {
		if raw[i] != '#' {
			continue
		}
		out := make([]byte, 0, len(raw))
		for j := 0; j < len(raw); j++ {
			if raw[j] == '#' && j+2 < len(raw) {
				if v, err := strconv.ParseUint(raw[j+1:j+3], 16, 8); err == nil {
					out = append(out, byte(v))
					j += 2
					continue
				}
			}
			out = append(out, raw[j])
		}
		return Name(out)
	}
	return Name(raw)
}

// number reads a number, or a reference when references are enabled and
// the number starts one.
func (l *lexer) number() any {
	w := l.word()
	if w == "" {
		l.pos++
		return keyword("")
	}
	if n, err := strconv.ParseInt(w, 10, 64); err == nil {
		if l.refs && n >= 0 {
			if ref, ok := l.reference(int(n)); ok {
				return ref
			}
		}
		return n
	}
	f, err := strconv.ParseFloat(w, 64)
	if err != nil {
		return keyword(w)
	}
	return f
}

// reference reads the rest of a reference to object num, restoring the
// position when what follows is not one.
func (l *lexer) reference(num int) (Ref, bool) {
	saved := l.pos
	l.skipSpace()
	gen, err := strconv.Atoi(l.word())
	if err == nil && gen >= 0 {
		l.skipSpace()
		if l.pos < len(l.data) && l.data[l.pos] == 'R' {
			after := l.pos + 1
			if after >= len(l.data) || isSpace(l.data[after]) || isDelim(l.data[after]) {
				l.pos = after
				return Ref{Num: num, Gen: gen}, true
			}
		}
	}
	l.pos = saved
	return Ref{}, false
}

// literal reads a string written between parentheses.
func (l *lexer) literal() String {
	l.pos++
	var out []byte
	nesting := 1
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		l.pos++
		switch c {
		case '(':
			nesting++
		case ')':
			nesting--
			if nesting == 0 {
				return String(out)
			}
		case '\\':
			out = l.escape(out)
			continue
		}
		out = append(out, c)
	}
	return String(out)
}

// escapes maps the letter of a backslash escape to the byte it stands
// for.
var escapes = map[byte]byte{'n': '\n', 'r': '\r', 't': '\t', 'b': '\b', 'f': '\f'}

// escape appends the character named by the backslash escape at the
// current position to out.
func (l *lexer) escape(out []byte) []byte {
	if l.pos >= len(l.data) {
		return out
	}
	c := l.data[l.pos]
	l.pos++
	if mapped, ok := escapes[c]; ok {
		return append(out, mapped)
	}
	switch {
	case c >= '0' && c <= '7':
		v := int(c - '0')
		for i := 0; i < 2 && l.pos < len(l.data) && l.data[l.pos] >= '0' && l.data[l.pos] <= '7'; i++ {
			v = v*8 + int(l.data[l.pos]-'0')
			l.pos++
		}
		return append(out, byte(v))
	case c == '\r':
		if l.pos < len(l.data) && l.data[l.pos] == '\n' {
			l.pos++
		}
		return out
	case c == '\n':
		return out
	}
	return append(out, c)
}

// hexValue returns the value of the hexadecimal digit c, or -1.
func hexValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// hex reads a string written as hexadecimal digits between angle
// brackets.
func (l *lexer) hex() String {
	l.pos++
	var out []byte
	high := -1
	for l.pos < len(l.data) {
		c := l.data[l.pos]
		l.pos++
		if c == '>' {
			break
		}
		v := hexValue(c)
		if v < 0 {
			continue
		}
		if high < 0 {
			high = v
			continue
		}
		out = append(out, byte(high<<4|v))
		high = -1
	}
	if high >= 0 {
		out = append(out, byte(high<<4))
	}
	return String(out)
}

// array reads an array.
func (l *lexer) array() Array {
	l.pos++
	out := Array{}
	if l.depth >= maxDepth {
		return out
	}
	l.depth++
	defer func() { l.depth-- }()
	for {
		obj, ok := l.object()
		if !ok || obj == keyword("]") {
			return out
		}
		out = append(out, obj)
	}
}

// dict reads a dictionary.
func (l *lexer) dict() Dict {
	l.pos += 2
	out := Dict{}
	if l.depth >= maxDepth {
		return out
	}
	l.depth++
	defer func() { l.depth-- }()
	for {
		key, ok := l.object()
		if !ok || key == keyword(">>") {
			return out
		}
		name, isName := key.(Name)
		if !isName {
			continue
		}
		value, ok := l.object()
		if !ok || value == keyword(">>") {
			return out
		}
		out[name] = value
	}
}
