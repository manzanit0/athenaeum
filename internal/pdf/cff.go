package pdf

import (
	"errors"
	"strings"
)

// isoAdobeNames lists the glyph names of the CFF standard strings 150 to
// 228 in order. The strings 1 to 149 are the glyphs of StandardEncoding
// in code order.
const isoAdobeNames = `onesuperior logicalnot mu trademark Eth onehalf plusminus Thorn onequarter
divide brokenbar degree thorn threequarters twosuperior registered minus eth multiply
threesuperior copyright Aacute Acircumflex Adieresis Agrave Aring Atilde Ccedilla Eacute
Ecircumflex Edieresis Egrave Iacute Icircumflex Idieresis Igrave Ntilde Oacute Ocircumflex
Odieresis Ograve Otilde Scaron Uacute Ucircumflex Udieresis Ugrave Yacute Ydieresis Zcaron aacute
acircumflex adieresis agrave aring atilde ccedilla eacute ecircumflex edieresis egrave iacute
icircumflex idieresis igrave ntilde oacute ocircumflex odieresis ograve otilde scaron uacute
ucircumflex udieresis ugrave yacute ydieresis zcaron`

// CFF layout constants.
const (
	// cffStandardStrings is the number of predefined strings.
	cffStandardStrings = 391
	// cffCharset is the Top DICT operator giving the charset offset.
	cffCharset = 15
	// cffEncoding is the Top DICT operator giving the encoding offset.
	cffEncoding = 16
	// cffCharStrings is the Top DICT operator giving the offset of the
	// glyph outlines, whose count is the number of glyphs.
	cffCharStrings = 17
)

// errCFF is returned for CFF font programs that cannot be read.
var errCFF = errors.New("pdf: malformed CFF font")

// standardStrings holds the text of the CFF standard strings that name
// ordinary characters, and standardCodes the StandardEncoding code of
// the first 150 of them.
var standardStrings, standardCodes = buildStandardStrings()

// buildStandardStrings builds standardStrings and standardCodes.
func buildStandardStrings() ([]string, []int) {
	texts, codes := []string{""}, []int{-1}
	for code, text := range standard {
		if text != "" {
			texts, codes = append(texts, text), append(codes, code)
		}
	}
	for _, name := range strings.Fields(isoAdobeNames) {
		texts = append(texts, glyphNames[name])
	}
	return texts, codes
}

// cffReader reads the structures of a CFF font program.
type cffReader struct {
	// data is the font program.
	data []byte
	// pos is the offset of the next unread byte.
	pos int
	// failed reports whether a read went past the end of the data.
	failed bool
}

// uint reads a big endian integer of n bytes.
func (r *cffReader) uint(n int) int {
	if r.pos < 0 || r.pos+n > len(r.data) {
		r.failed = true
		return 0
	}
	v := field(r.data[r.pos : r.pos+n])
	r.pos += n
	return v
}

// index reads an INDEX and returns its entries.
func (r *cffReader) index() [][]byte {
	count := r.uint(2)
	if count == 0 || r.failed {
		return nil
	}
	size := r.uint(1)
	offsets := make([]int, count+1)
	for i := range offsets {
		offsets[i] = r.uint(size)
	}
	base := r.pos - 1
	entries := make([][]byte, 0, count)
	for i := range count {
		start, end := base+offsets[i], base+offsets[i+1]
		if r.failed || start < 0 || end < start || end > len(r.data) {
			r.failed = true
			return nil
		}
		entries = append(entries, r.data[start:end])
	}
	r.pos = base + offsets[count]
	return entries
}

// topDict returns the integer operands of the operators of a Top DICT
// that take a single offset.
func topDict(data []byte) map[int]int {
	values := map[int]int{}
	last := 0
	for i := 0; i < len(data); {
		b := int(data[i])
		switch {
		case b == 12:
			i += 2
		case b <= 21:
			values[b] = last
			i++
		case b == 28 && i+2 < len(data):
			last = int(int16(data[i+1])<<8 | int16(data[i+2]))
			i += 3
		case b == 29 && i+4 < len(data):
			last = int(int32(field(data[i+1 : i+5])))
			i += 5
		case b == 30:
			for i++; i < len(data) && data[i]&0x0f != 0x0f && data[i]>>4 != 0x0f; i++ {
			}
			i++
		case b >= 32 && b <= 246:
			last = b - 139
			i++
		case b >= 247 && b <= 250 && i+1 < len(data):
			last = (b-247)*256 + int(data[i+1]) + 108
			i += 2
		case b >= 251 && b <= 254 && i+1 < len(data):
			last = -(b-251)*256 - int(data[i+1]) - 108
			i += 2
		default:
			i++
		}
	}
	return values
}

// cffEncodingOf returns the encoding built into the CFF font program in
// data.
func cffEncodingOf(data []byte) (encoding, error) {
	var enc encoding
	if len(data) < 4 {
		return enc, errCFF
	}
	r := &cffReader{data: data, pos: int(data[2])}
	r.index()
	dicts := r.index()
	names := r.index()
	if r.failed || len(dicts) == 0 {
		return enc, errCFF
	}
	top := topDict(dicts[0])
	glyphs := (&cffReader{data: data, pos: top[cffCharStrings]}).uint(2)
	sids, err := cffCharsetOf(data, top[cffCharset], glyphs)
	if err != nil {
		return enc, err
	}
	text := func(sid int) string {
		switch {
		case sid < len(standardStrings):
			return standardStrings[sid]
		case sid >= cffStandardStrings && sid-cffStandardStrings < len(names):
			return glyphText(string(names[sid-cffStandardStrings]))
		}
		return ""
	}
	if top[cffEncoding] == 0 {
		for _, sid := range sids {
			if sid > 0 && sid < len(standardCodes) {
				enc[standardCodes[sid]] = text(sid)
			}
		}
		return enc, nil
	}
	return enc, cffCustomEncoding(&enc, data, top[cffEncoding], sids, text)
}

// cffCharsetOf returns the string identifier of every glyph of a font
// with the given number of glyphs, read from the charset at offset.
func cffCharsetOf(data []byte, offset, glyphs int) ([]int, error) {
	if glyphs <= 0 || glyphs > 65536 || offset == 1 || offset == 2 {
		return nil, errCFF
	}
	sids := make([]int, 1, glyphs)
	if offset == 0 {
		for sid := 1; sid < glyphs; sid++ {
			sids = append(sids, sid)
		}
		return sids, nil
	}
	r := &cffReader{data: data, pos: offset}
	format := r.uint(1)
	for len(sids) < glyphs && !r.failed {
		first := r.uint(2)
		left := 0
		switch format {
		case 1:
			left = r.uint(1)
		case 2:
			left = r.uint(2)
		}
		for n := 0; n <= left && len(sids) < glyphs; n++ {
			sids = append(sids, first+n)
		}
	}
	if r.failed {
		return nil, errCFF
	}
	return sids, nil
}

// cffCustomEncoding fills enc from the encoding table at offset, which
// assigns character codes to the glyphs whose string identifiers are in
// sids.
func cffCustomEncoding(enc *encoding, data []byte, offset int, sids []int, text func(int) string) error {
	if offset == 1 {
		return errCFF
	}
	r := &cffReader{data: data, pos: offset}
	format := r.uint(1)
	glyph := 1
	assign := func(code int) {
		if glyph < len(sids) {
			enc[code&0xFF] = text(sids[glyph])
		}
		glyph++
	}
	for range r.uint(1) {
		code := r.uint(1)
		if format&0x7F == 0 {
			assign(code)
			continue
		}
		for n, left := 0, r.uint(1); n <= left; n++ {
			assign(code + n)
		}
	}
	if format&0x80 != 0 {
		for range r.uint(1) {
			code := r.uint(1)
			enc[code] = text(r.uint(2))
		}
	}
	if r.failed {
		return errCFF
	}
	return nil
}
