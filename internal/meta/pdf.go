package meta

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf16"
)

const (
	// pdfTailBytes is how much of the end of a PDF is searched for the
	// reference to the document information dictionary.
	pdfTailBytes = 1 << 20
	// pdfChunkBytes is the buffer size used to scan a PDF for an object.
	pdfChunkBytes = 64 << 10
	// pdfObjectBytes bounds the size of the information dictionary read.
	pdfObjectBytes = 64 << 10
)

// pdfInfoRef matches the trailer entry pointing at the information
// dictionary.
var pdfInfoRef = regexp.MustCompile(`/Info\s+(\d+)\s+(\d+)\s+R`)

// readPDF extracts metadata from the information dictionary of the PDF at
// path. Dictionaries stored in compressed object streams or encrypted
// documents are not read.
func readPDF(path string) (Metadata, error) {
	f, err := os.Open(path)
	if err != nil {
		return Metadata{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Metadata{}, err
	}
	tail := make([]byte, min(info.Size(), pdfTailBytes))
	if _, err := f.ReadAt(tail, info.Size()-int64(len(tail))); err != nil && err != io.EOF {
		return Metadata{}, err
	}
	refs := pdfInfoRef.FindAllSubmatch(tail, -1)
	if len(refs) == 0 {
		return Metadata{}, nil
	}
	ref := refs[len(refs)-1]
	obj, err := findPDFObject(f, fmt.Sprintf("%s %s obj", ref[1], ref[2]))
	if err != nil || obj == nil {
		return Metadata{}, err
	}
	return Metadata{
		Title:       pdfString(obj, "/Title"),
		Authors:     splitAuthors(pdfString(obj, "/Author")),
		Description: pdfString(obj, "/Subject"),
		Tags:        strings.FieldsFunc(pdfString(obj, "/Keywords"), isListSeparator),
	}, nil
}

// isListSeparator reports whether r separates entries in a keyword list.
func isListSeparator(r rune) bool {
	return r == ',' || r == ';'
}

// findPDFObject scans f for the last object introduced by marker and
// returns its body, or nil when there is none.
func findPDFObject(f *os.File, marker string) ([]byte, error) {
	needle := []byte(marker)
	buf := make([]byte, 0, pdfChunkBytes+len(needle)+1)
	chunk := make([]byte, pdfChunkBytes)
	found := int64(-1)
	var base int64
	for {
		n, err := f.ReadAt(chunk, base+int64(len(buf)))
		buf = append(buf, chunk[:n]...)
		for i := 0; ; {
			j := bytes.Index(buf[i:], needle)
			if j < 0 {
				break
			}
			i += j
			if i == 0 || !isDigit(buf[i-1]) {
				found = base + int64(i) + int64(len(needle))
			}
			i++
		}
		if err != nil {
			break
		}
		keep := min(len(buf), len(needle)+1)
		base += int64(len(buf) - keep)
		buf = append(buf[:0], buf[len(buf)-keep:]...)
	}
	if found < 0 {
		return nil, nil
	}
	obj := make([]byte, pdfObjectBytes)
	n, err := f.ReadAt(obj, found)
	if err != nil && err != io.EOF {
		return nil, err
	}
	obj = obj[:n]
	if end := bytes.Index(obj, []byte("endobj")); end >= 0 {
		obj = obj[:end]
	}
	return obj, nil
}

// isDigit reports whether c is an ASCII digit.
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

// pdfString returns the decoded string value of key in the dictionary obj,
// or an empty string when the key is absent or not a direct string.
func pdfString(obj []byte, key string) string {
	for start := 0; ; {
		i := bytes.Index(obj[start:], []byte(key))
		if i < 0 {
			return ""
		}
		rest := bytes.TrimLeft(obj[start+i+len(key):], " \t\r\n")
		start += i + len(key)
		if len(rest) == 0 {
			return ""
		}
		switch {
		case rest[0] == '(':
			return decodePDFText(pdfLiteral(rest))
		case rest[0] == '<' && len(rest) > 1 && rest[1] != '<':
			return decodePDFText(pdfHex(rest))
		}
	}
}

// pdfEscapes maps the single character escapes of PDF literal strings.
var pdfEscapes = map[byte]byte{
	'n': '\n', 'r': '\r', 't': '\t', 'b': '\b', 'f': '\f',
}

// pdfLiteral decodes the literal string starting at the opening
// parenthesis of b.
func pdfLiteral(b []byte) []byte {
	var out []byte
	depth := 0
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '\\' && i+1 < len(b):
			var decoded []byte
			decoded, i = pdfEscape(b, i+1)
			out = append(out, decoded...)
		case c == '(':
			depth++
			if depth > 1 {
				out = append(out, c)
			}
		case c == ')':
			depth--
			if depth == 0 {
				return out
			}
			out = append(out, c)
		default:
			out = append(out, c)
		}
	}
	return out
}

// pdfEscape decodes the escape sequence whose first character after the
// backslash is at b[i]. It returns the decoded bytes and the index of the
// last byte consumed.
func pdfEscape(b []byte, i int) ([]byte, int) {
	c := b[i]
	if v, ok := pdfEscapes[c]; ok {
		return []byte{v}, i
	}
	if c == '\n' || c == '\r' {
		return nil, i
	}
	if c < '0' || c > '7' {
		return []byte{c}, i
	}
	value := 0
	end := i
	for ; end < len(b) && end < i+3 && b[end] >= '0' && b[end] <= '7'; end++ {
		value = value*8 + int(b[end]-'0')
	}
	return []byte{byte(value)}, end - 1
}

// pdfHex decodes the hexadecimal string starting at the opening angle
// bracket of b.
func pdfHex(b []byte) []byte {
	end := bytes.IndexByte(b, '>')
	if end < 0 {
		return nil
	}
	digits := bytes.Map(func(r rune) rune {
		if strings.ContainsRune(" \t\r\n", r) {
			return -1
		}
		return r
	}, b[1:end])
	if len(digits)%2 == 1 {
		digits = append(digits, '0')
	}
	out := make([]byte, len(digits)/2)
	if _, err := hex.Decode(out, digits); err != nil {
		return nil
	}
	return out
}

// decodePDFText converts a PDF text string to UTF-8.
func decodePDFText(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		units := make([]uint16, (len(b)-2)/2)
		for i := range units {
			units[i] = binary.BigEndian.Uint16(b[2+i*2:])
		}
		return string(utf16.Decode(units))
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return decodeUTF8(b[3:])
	}
	return decodeCP1252(b)
}
