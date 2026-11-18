package pdf

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// Font descriptor flag bits.
const (
	// flagItalic marks italic fonts.
	flagItalic = 1 << 6
	// flagForceBold marks fonts that are drawn bold.
	flagForceBold = 1 << 18
)

// narrowGlyphs lists the characters that are narrow in most typefaces.
const narrowGlyphs = "iIjl.,;:'|!`ftr()[]"

// wideGlyphs lists the characters that are wide in most typefaces.
const wideGlyphs = "mwMW@%"

// font decodes the character codes shown with one font into text and
// advance widths.
type font struct {
	// name is the name of the typeface without any subset prefix.
	name string
	// bold reports whether the typeface is bold.
	bold bool
	// italic reports whether the typeface is italic or oblique.
	italic bool
	// wide reports whether character codes are two bytes long.
	wide bool
	// direct reports whether two byte codes are UTF-16 themselves.
	direct bool
	// enc maps the codes of a simple font to text.
	enc encoding
	// toUnicode maps codes to text and takes precedence over enc.
	toUnicode *cmap
	// first is the code the first entry of simple belongs to.
	first int
	// simple holds the widths of a simple font in glyph units.
	simple []float64
	// cids holds the widths of a composite font by character code.
	cids map[int]float64
	// fallback is the width of codes without one; negative means guess
	// from the shape of the character.
	fallback float64
	// scale converts glyph units to text space units.
	scale float64
}

// font returns the font described by obj, loading it on first use.
func (d *Document) font(obj any) *font {
	ref, isRef := obj.(Ref)
	if isRef {
		if f, ok := d.fonts[ref.Num]; ok {
			return f
		}
	}
	dict := d.dict(obj)
	if dict == nil {
		return nil
	}
	f := &font{fallback: -1, scale: 0.001}
	descriptor := d.dict(dict["FontDescriptor"])
	switch d.name(dict["Subtype"]) {
	case "Type0":
		descendant := d.dict(d.first(dict["DescendantFonts"]))
		descriptor = d.dict(descendant["FontDescriptor"])
		d.loadComposite(f, dict, descendant)
	case "Type3":
		if m := d.array(dict["FontMatrix"]); len(m) == 6 {
			f.scale = d.float(m[0], 0.001)
		}
		d.loadSimple(f, dict, descriptor)
	default:
		d.loadSimple(f, dict, descriptor)
	}
	if stream := d.stream(dict["ToUnicode"]); stream != nil {
		if data, err := stream.Data(); err == nil {
			f.toUnicode = parseCMap(data)
		}
	}
	d.describe(f, dict, descriptor)
	if isRef {
		d.fonts[ref.Num] = f
	}
	return f
}

// first returns the first element of the array obj, or nil.
func (d *Document) first(obj any) any {
	if arr := d.array(obj); len(arr) > 0 {
		return arr[0]
	}
	return nil
}

// describe sets the name and style of f from its dictionaries.
func (d *Document) describe(f *font, dict, descriptor Dict) {
	name := string(d.name(dict["BaseFont"]))
	if name == "" {
		name = string(d.name(descriptor["FontName"]))
	}
	if _, after, found := strings.Cut(name, "+"); found {
		name = after
	}
	f.name = name
	lower := strings.ToLower(name)
	flags := d.integer(descriptor["Flags"], 0)
	for _, word := range []string{"bold", "black", "heavy", "semibold", "demi"} {
		f.bold = f.bold || strings.Contains(lower, word)
	}
	f.bold = f.bold || flags&flagForceBold != 0 || d.float(descriptor["FontWeight"], 400) >= 600
	f.italic = strings.Contains(lower, "italic") || strings.Contains(lower, "oblique") ||
		flags&flagItalic != 0 || d.float(descriptor["ItalicAngle"], 0) != 0
}

// builtinPut matches the lines of a Type 1 font program that assign a
// glyph name to a character code.
var builtinPut = regexp.MustCompile(`dup\s+(\d+)\s*/([^\s/]+)\s+put`)

// builtinEncoding returns the encoding built into the font itself, which
// applies when the font dictionary does not name one. It is read from
// the embedded font program, which is either in the compact CFF format
// or a Type 1 program with a readable part, and is the Symbol encoding
// for the Symbol typeface. The fallback is returned when
// the font has neither.
func (d *Document) builtinEncoding(dict, descriptor Dict, fallback encoding) encoding {
	if compact := d.stream(descriptor["FontFile3"]); compact != nil {
		if data, err := compact.Data(); err == nil {
			if enc, err := cffEncodingOf(data); err == nil {
				return enc
			}
		}
		return fallback
	}
	stream := d.stream(descriptor["FontFile"])
	if stream == nil {
		if strings.Contains(string(d.name(dict["BaseFont"])), "Symbol") {
			return symbol
		}
		return fallback
	}
	data, err := stream.Data()
	if err != nil {
		return fallback
	}
	if clear := d.integer(stream.Dict["Length1"], 0); clear > 0 && clear < len(data) {
		data = data[:clear]
	}
	puts := builtinPut.FindAllSubmatch(data, -1)
	if len(puts) == 0 {
		return fallback
	}
	var enc encoding
	for _, put := range puts {
		if v, err := strconv.Atoi(string(put[1])); err == nil && v >= 0 && v < len(enc) {
			enc[v] = glyphText(string(put[2]))
		}
	}
	return enc
}

// loadSimple reads the encoding and widths of a simple font.
func (d *Document) loadSimple(f *font, dict, descriptor Dict) {
	f.enc = winAnsi
	if d.name(dict["Subtype"]) == "Type1" {
		f.enc = standard
	}
	switch enc := d.resolve(dict["Encoding"]).(type) {
	case Name:
		f.enc = baseEncoding(enc, f.enc)
	case Dict:
		if base := d.name(enc["BaseEncoding"]); base != "" {
			f.enc = baseEncoding(base, f.enc)
		} else {
			f.enc = d.builtinEncoding(dict, descriptor, f.enc)
		}
		d.applyDifferences(&f.enc, d.array(enc["Differences"]))
	default:
		f.enc = d.builtinEncoding(dict, descriptor, f.enc)
	}
	f.first = d.integer(dict["FirstChar"], 0)
	for _, w := range d.array(dict["Widths"]) {
		f.simple = append(f.simple, d.float(w, 0))
	}
	if missing, ok := d.number(descriptor["MissingWidth"]); ok && len(f.simple) > 0 {
		f.fallback = missing
	}
}

// baseEncoding returns the predefined encoding called name, or fallback
// when there is none.
func baseEncoding(name Name, fallback encoding) encoding {
	switch name {
	case "WinAnsiEncoding":
		return winAnsi
	case "MacRomanEncoding":
		return macRoman
	case "StandardEncoding":
		return standard
	}
	return fallback
}

// applyDifferences changes enc as told by a Differences array, which
// lists codes followed by the glyph names assigned from each on.
func (d *Document) applyDifferences(enc *encoding, differences Array) {
	next := 0
	for _, item := range differences {
		switch v := d.resolve(item).(type) {
		case int64:
			next = int(v)
		case Name:
			if next >= 0 && next < len(enc) {
				if text := glyphText(string(v)); text != "" {
					enc[next] = text
				}
			}
			next++
		}
	}
}

// loadComposite reads the code layout and widths of a composite font.
func (d *Document) loadComposite(f *font, dict, descendant Dict) {
	f.wide = true
	encName := string(d.name(dict["Encoding"]))
	f.direct = strings.Contains(encName, "UCS2") || strings.Contains(encName, "UTF16")
	f.fallback = d.float(descendant["DW"], 1000)
	f.cids = map[int]float64{}
	w := d.array(descendant["W"])
	for i := 0; i < len(w); {
		start := d.integer(w[i], 0)
		if i+1 >= len(w) {
			break
		}
		if list := d.array(w[i+1]); list != nil {
			for n, width := range list {
				f.cids[start+n] = d.float(width, f.fallback)
			}
			i += 2
			continue
		}
		if i+2 >= len(w) {
			break
		}
		end, width := d.integer(w[i+1], start), d.float(w[i+2], f.fallback)
		for c := start; c <= end && c-start < maxRangeExpansion; c++ {
			f.cids[c] = width
		}
		i += 3
	}
}

// each calls fn for every character code in s with the text it stands
// for, its advance in text space units for a font size of one, and
// whether it is the single byte space that word spacing applies to. The
// text is empty for codes that cannot be decoded.
func (f *font) each(s String, fn func(text string, advance float64, space bool)) {
	step := 1
	if f.wide {
		step = 2
	}
	for i := 0; i+step <= len(s); i += step {
		v := int(s[i])
		if f.wide {
			v = v<<8 | int(s[i+1])
		}
		text := f.text(v)
		fn(text, f.width(v, text)*f.scale, !f.wide && v == ' ')
	}
}

// text returns the text the code v stands for.
func (f *font) text(v int) string {
	if f.toUnicode != nil {
		if text, ok := f.toUnicode.lookup(uint32(v)); ok {
			return text
		}
	}
	switch {
	case f.direct:
		return string(utf16.Decode([]uint16{uint16(v)}))
	case f.wide:
		return ""
	}
	return f.enc[v]
}

// width returns the advance of the code v in glyph units. The text of
// the code helps to guess a width when the font declares none.
func (f *font) width(v int, text string) float64 {
	if f.wide {
		if w, ok := f.cids[v]; ok {
			return w
		}
		return f.fallback
	}
	if i := v - f.first; i >= 0 && i < len(f.simple) {
		return f.simple[i]
	}
	if f.fallback >= 0 {
		return f.fallback
	}
	return guessWidth(f.name, text)
}

// guessWidth estimates the advance in glyph units of text set in the
// typeface name, for the standard fonts that come without widths.
func guessWidth(name, text string) float64 {
	if strings.Contains(name, "Courier") || strings.Contains(name, "Mono") {
		return 600
	}
	var r rune
	for _, r = range text {
		break
	}
	switch {
	case r == ' ' || r == 0:
		return 265
	case strings.ContainsRune(narrowGlyphs, r):
		return 290
	case strings.ContainsRune(wideGlyphs, r):
		return 860
	case unicode.IsUpper(r):
		return 680
	}
	return 520
}
