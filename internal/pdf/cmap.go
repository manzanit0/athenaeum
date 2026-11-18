package pdf

import (
	"unicode/utf16"
)

// maxRangeExpansion bounds how many codes of one range are expanded when
// the range lists its targets one by one.
const maxRangeExpansion = 65536

// cmapRange maps a run of consecutive codes to consecutive characters.
type cmapRange struct {
	// low is the first code of the range.
	low uint32
	// high is the last code of the range.
	high uint32
	// base holds the UTF-16 units the first code maps to.
	base []uint16
}

// cmap maps character codes to text, as described by a ToUnicode CMap.
type cmap struct {
	// chars holds the codes mapped one by one.
	chars map[uint32]string
	// ranges holds the codes mapped as ranges.
	ranges []cmapRange
}

// code reads a character code from the bytes of a string.
func code(s String) uint32 {
	var v uint32
	for i := 0; i < len(s) && i < 4; i++ {
		v = v<<8 | uint32(s[i])
	}
	return v
}

// units reads UTF-16 code units from the bytes of a string.
func units(s String) []uint16 {
	out := make([]uint16, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		out = append(out, uint16(s[i])<<8|uint16(s[i+1]))
	}
	return out
}

// parseCMap reads the mappings of a ToUnicode CMap.
func parseCMap(data []byte) *cmap {
	c := &cmap{chars: map[uint32]string{}}
	l := lexer{data: data}
	for {
		obj, ok := l.object()
		if !ok {
			return c
		}
		switch obj {
		case keyword("beginbfchar"):
			c.readChars(&l)
		case keyword("beginbfrange"):
			c.readRanges(&l)
		}
	}
}

// readChars reads the pairs of a bfchar section.
func (c *cmap) readChars(l *lexer) {
	for {
		src, ok1 := l.object()
		from, isString := src.(String)
		if !ok1 || !isString {
			return
		}
		dst, ok2 := l.object()
		to, isString := dst.(String)
		if !ok2 || !isString {
			return
		}
		c.chars[code(from)] = string(utf16.Decode(units(to)))
	}
}

// readRanges reads the triples of a bfrange section.
func (c *cmap) readRanges(l *lexer) {
	for {
		first, ok1 := l.object()
		low, isString1 := first.(String)
		last, ok2 := l.object()
		high, isString2 := last.(String)
		if !ok1 || !ok2 || !isString1 || !isString2 {
			return
		}
		target, ok := l.object()
		if !ok {
			return
		}
		switch t := target.(type) {
		case String:
			c.ranges = append(c.ranges, cmapRange{low: code(low), high: code(high), base: units(t)})
		case Array:
			c.expand(code(low), code(high), t)
		}
	}
}

// expand records a range whose targets are listed one by one.
func (c *cmap) expand(low, high uint32, targets Array) {
	for i, target := range targets {
		value := low + uint32(i)
		if value > high || i >= maxRangeExpansion {
			return
		}
		if s, ok := target.(String); ok {
			c.chars[value] = string(utf16.Decode(units(s)))
		}
	}
}

// lookup returns the text the code v maps to.
func (c *cmap) lookup(v uint32) (string, bool) {
	if text, ok := c.chars[v]; ok {
		return text, true
	}
	for _, r := range c.ranges {
		if v < r.low || v > r.high || len(r.base) == 0 {
			continue
		}
		mapped := append([]uint16(nil), r.base...)
		mapped[len(mapped)-1] += uint16(v - r.low)
		return string(utf16.Decode(mapped)), true
	}
	return "", false
}
