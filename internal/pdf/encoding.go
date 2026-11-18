package pdf

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// asciiNames lists the glyph names of the characters U+0020 to U+007E in
// order.
const asciiNames = `space exclam quotedbl numbersign dollar percent ampersand quotesingle
parenleft parenright asterisk plus comma hyphen period slash zero one two three four five six
seven eight nine colon semicolon less equal greater question at A B C D E F G H I J K L M N O
P Q R S T U V W X Y Z bracketleft backslash bracketright asciicircum underscore grave a b c d e
f g h i j k l m n o p q r s t u v w x y z braceleft bar braceright asciitilde`

// latin1Names lists the glyph names of the characters U+00A1 to U+00FF in
// order.
const latin1Names = `exclamdown cent sterling currency yen brokenbar section dieresis copyright
ordfeminine guillemotleft logicalnot sfthyphen registered macron degree plusminus twosuperior
threesuperior acute mu paragraph periodcentered cedilla onesuperior ordmasculine guillemotright
onequarter onehalf threequarters questiondown Agrave Aacute Acircumflex Atilde Adieresis Aring
AE Ccedilla Egrave Eacute Ecircumflex Edieresis Igrave Iacute Icircumflex Idieresis Eth Ntilde
Ograve Oacute Ocircumflex Otilde Odieresis multiply Oslash Ugrave Uacute Ucircumflex Udieresis
Yacute Thorn germandbls agrave aacute acircumflex atilde adieresis aring ae ccedilla egrave
eacute ecircumflex edieresis igrave iacute icircumflex idieresis eth ntilde ograve oacute
ocircumflex otilde odieresis divide oslash ugrave uacute ucircumflex udieresis yacute thorn
ydieresis`

// latinANames lists the glyph names of the characters U+0100 to U+017F in
// order.
const latinANames = `Amacron amacron Abreve abreve Aogonek aogonek Cacute cacute Ccircumflex
ccircumflex Cdotaccent cdotaccent Ccaron ccaron Dcaron dcaron Dcroat dcroat Emacron emacron
Ebreve ebreve Edotaccent edotaccent Eogonek eogonek Ecaron ecaron Gcircumflex gcircumflex
Gbreve gbreve Gdotaccent gdotaccent Gcommaaccent gcommaaccent Hcircumflex hcircumflex Hbar hbar
Itilde itilde Imacron imacron Ibreve ibreve Iogonek iogonek Idotaccent dotlessi IJ ij
Jcircumflex jcircumflex Kcommaaccent kcommaaccent kgreenlandic Lacute lacute Lcommaaccent
lcommaaccent Lcaron lcaron Ldot ldot Lslash lslash Nacute nacute Ncommaaccent ncommaaccent
Ncaron ncaron napostrophe Eng eng Omacron omacron Obreve obreve Ohungarumlaut ohungarumlaut OE
oe Racute racute Rcommaaccent rcommaaccent Rcaron rcaron Sacute sacute Scircumflex scircumflex
Scedilla scedilla Scaron scaron Tcommaaccent tcommaaccent Tcaron tcaron Tbar tbar Utilde utilde
Umacron umacron Ubreve ubreve Uring uring Uhungarumlaut uhungarumlaut Uogonek uogonek
Wcircumflex wcircumflex Ycircumflex ycircumflex Ydieresis Zacute zacute Zdotaccent zdotaccent
Zcaron zcaron longs`

// otherNames pairs further glyph names with the text they stand for.
const otherNames = `quoteleft ‘ quoteright ’ quotedblleft “ quotedblright ” quotesinglbase ‚
quotedblbase „ endash – emdash — bullet • ellipsis … dagger † daggerdbl ‡ perthousand ‰
guilsinglleft ‹ guilsinglright › fraction ⁄ florin ƒ Euro € euro € trademark ™ minus −
fi fi fl fl ff ff ffi ffi ffl ffl circumflex ˆ tilde ˜ breve ˘ dotaccent ˙ ring ˚ ogonek ˛
hungarumlaut ˝ caron ˇ hyphenminus - minute ′ second ″ lessequal ≤ greaterequal ≥ notequal ≠
infinity ∞ approxequal ≈ partialdiff ∂ summation ∑ product ∏ integral ∫ radical √ Omega Ω
Delta ∆ lozenge ◊ arrowleft ← arrowright → arrowup ↑ arrowdown ↓ Scommaaccent Ș scommaaccent ș
alpha α beta β gamma γ delta δ epsilon ε zeta ζ eta η theta θ iota ι kappa κ lambda λ nu ν
xi ξ omicron ο pi π rho ρ sigma σ tau τ upsilon υ phi φ chi χ psi ψ omega ω Gamma Γ Theta Θ
Lambda Λ Xi Ξ Pi Π Sigma Σ Phi Φ Psi Ψ figuredash ‒ horizontalbar ― onedotenleader ․
twodotenleader ‥ openbullet ◦ filledbox ■ periodinferior . commainferior , dotlessj ȷ
Gcaron Ǧ gcaron ǧ`

// winAnsiHigh holds the characters 0x80 to 0x9F of WinAnsiEncoding, with
// NUL marking undefined codes.
const winAnsiHigh = "€\x00‚ƒ„…†‡ˆ‰Š‹Œ\x00Ž\x00\x00‘’“”•–—˜™š›œ\x00žŸ"

// macRomanHigh holds the characters 0x80 to 0xFF of MacRomanEncoding.
const macRomanHigh = "ÄÅÇÉÑÖÜáàâäãåçéèêëíìîïñóòôöõúùûü†°¢£§•¶ß®©™´¨≠ÆØ∞±≤≥¥µ∂∑∏π∫ªºΩæø" +
	"¿¡¬√ƒ≈∆«»… ÀÃÕŒœ–—“”‘’÷◊ÿŸ⁄€‹›ﬁﬂ‡·‚„‰ÂÊÁËÈÍÎÏÌÓÔÒÚÛÙıˆ˜¯˘˙˚¸˝˛ˇ"

// pdfDocHigh holds the characters 0x80 to 0x9E of PDFDocEncoding.
const pdfDocHigh = "•†‡…—–ƒ⁄‹›−‰„“”‘’‚™ﬁﬂŁŒŠŸŽıłœšž"

// pdfDocLow holds the characters 0x18 to 0x1F of PDFDocEncoding.
const pdfDocLow = "˘ˇˆ˙˝˛˚˜"

// symbolLow holds the characters 0x20 to 0x7E of the Symbol encoding,
// with NUL marking codes without a character of their own.
const symbolLow = " !∀#∃%&∋()∗+,−./0123456789:;<=>?≅ΑΒΧΔΕΦΓΗΙϑΚΛΜΝΟΠΘΡΣΤΥςΩΞΨΖ[∴]⊥_\x00αβχδεφγηιϕκλμνοπθρστυϖωξψζ{|}∼"

// symbolHigh holds the characters 0xA0 to 0xE5 of the Symbol encoding.
const symbolHigh = "€ϒ′≤⁄∞ƒ♣♦♥♠↔←↑→↓°±″≥×∝∂•÷≠≡≈…\x00\x00↵ℵℑℜ℘⊗⊕∅∩∪⊃⊇⊄⊂⊆∈∉∠∇®©™∏√⋅¬∧∨⇔⇐⇑⇒⇓◊〈®©™∑"

// standardHigh maps the codes above 0x7F of StandardEncoding to glyph
// names.
var standardHigh = map[int]string{
	0xA1: "exclamdown", 0xA2: "cent", 0xA3: "sterling", 0xA4: "fraction", 0xA5: "yen",
	0xA6: "florin", 0xA7: "section", 0xA8: "currency", 0xA9: "quotesingle",
	0xAA: "quotedblleft", 0xAB: "guillemotleft", 0xAC: "guilsinglleft", 0xAD: "guilsinglright",
	0xAE: "fi", 0xAF: "fl", 0xB1: "endash", 0xB2: "dagger", 0xB3: "daggerdbl",
	0xB4: "periodcentered", 0xB6: "paragraph", 0xB7: "bullet", 0xB8: "quotesinglbase",
	0xB9: "quotedblbase", 0xBA: "quotedblright", 0xBB: "guillemotright", 0xBC: "ellipsis",
	0xBD: "perthousand", 0xBF: "questiondown", 0xC1: "grave", 0xC2: "acute", 0xC3: "circumflex",
	0xC4: "tilde", 0xC5: "macron", 0xC6: "breve", 0xC7: "dotaccent", 0xC8: "dieresis",
	0xCA: "ring", 0xCB: "cedilla", 0xCD: "hungarumlaut", 0xCE: "ogonek", 0xCF: "caron",
	0xD0: "emdash", 0xE1: "AE", 0xE3: "ordfeminine", 0xE8: "Lslash", 0xE9: "Oslash", 0xEA: "OE",
	0xEB: "ordmasculine", 0xF1: "ae", 0xF5: "dotlessi", 0xF8: "lslash", 0xF9: "oslash",
	0xFA: "oe", 0xFB: "germandbls",
}

// glyphNames maps glyph names to the text they stand for.
var glyphNames = buildGlyphNames()

// encoding maps the 256 character codes of a simple font to text.
type encoding [256]string

// The predefined encodings of simple fonts and of text strings.
var (
	// winAnsi is WinAnsiEncoding.
	winAnsi = buildWinAnsi()
	// macRoman is MacRomanEncoding.
	macRoman = buildMacRoman()
	// standard is StandardEncoding.
	standard = buildStandard()
	// pdfDoc is PDFDocEncoding, used by text strings outside content.
	pdfDoc = buildPDFDoc()
	// symbol is the encoding built into the Symbol typeface.
	symbol = buildSymbol()
)

// buildSymbol builds the encoding of the Symbol typeface.
func buildSymbol() encoding {
	var enc encoding
	enc.fill(0x20, symbolLow)
	enc.fill(0xA0, symbolHigh)
	enc[0xF1], enc[0xF2] = "〉", "∫"
	return enc
}

// buildGlyphNames builds the glyph name table from the name lists.
func buildGlyphNames() map[string]string {
	names := map[string]string{}
	sequences := []struct {
		// first is the character the first name of list stands for.
		first rune
		// list holds the names in character order.
		list string
	}{{0x20, asciiNames}, {0xA1, latin1Names}, {0x100, latinANames}}
	for _, seq := range sequences {
		for i, name := range strings.Fields(seq.list) {
			names[name] = string(seq.first + rune(i))
		}
	}
	pairs := strings.Fields(otherNames)
	for i := 0; i+1 < len(pairs); i += 2 {
		names[pairs[i]] = pairs[i+1]
	}
	names["nbspace"], names["nonbreakingspace"] = " ", " "
	return names
}

// ascii returns an encoding holding only the printable ASCII characters.
func ascii() encoding {
	var enc encoding
	for c := 0x20; c < 0x7F; c++ {
		enc[c] = string(rune(c))
	}
	return enc
}

// fill sets the codes starting at first to the characters of chars,
// skipping NUL characters.
func (enc *encoding) fill(first int, chars string) {
	for _, r := range chars {
		if r != 0 {
			enc[first] = string(r)
		}
		first++
	}
}

// latin1 sets the codes 0xA0 to 0xFF to their Latin-1 characters.
func (enc *encoding) latin1() {
	for c := 0xA0; c <= 0xFF; c++ {
		enc[c] = string(rune(c))
	}
}

// buildWinAnsi builds WinAnsiEncoding.
func buildWinAnsi() encoding {
	enc := ascii()
	enc.fill(0x80, winAnsiHigh)
	enc.latin1()
	return enc
}

// buildMacRoman builds MacRomanEncoding.
func buildMacRoman() encoding {
	enc := ascii()
	enc.fill(0x80, macRomanHigh)
	return enc
}

// buildStandard builds StandardEncoding.
func buildStandard() encoding {
	enc := ascii()
	enc[0x27], enc[0x60] = "’", "‘"
	for code, name := range standardHigh {
		enc[code] = glyphNames[name]
	}
	return enc
}

// buildPDFDoc builds PDFDocEncoding.
func buildPDFDoc() encoding {
	enc := ascii()
	for _, c := range []int{'\t', '\n', '\r'} {
		enc[c] = string(rune(c))
	}
	enc.fill(0x18, pdfDocLow)
	enc.fill(0x80, pdfDocHigh)
	enc.latin1()
	enc[0xA0] = "€"
	return enc
}

// glyphText returns the text the glyph called name stands for, or the
// empty string when the name is not known.
func glyphText(name string) string {
	if text, ok := glyphNames[name]; ok {
		return text
	}
	name, _, _ = strings.Cut(name, ".")
	var sb strings.Builder
	for _, part := range strings.Split(name, "_") {
		if text, ok := glyphNames[part]; ok {
			sb.WriteString(text)
			continue
		}
		sb.WriteString(codePointName(part))
	}
	return sb.String()
}

// codePointName decodes glyph names of the forms uniXXXX and uXXXXXX.
func codePointName(name string) string {
	var digits string
	width := 0
	switch {
	case strings.HasPrefix(name, "uni") && (len(name)-3)%4 == 0:
		digits, width = name[3:], 4
	case strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7:
		digits, width = name[1:], len(name)-1
	default:
		return ""
	}
	var units []uint16
	var sb strings.Builder
	for i := 0; i+width <= len(digits); i += width {
		v, err := strconv.ParseUint(digits[i:i+width], 16, 32)
		if err != nil {
			return ""
		}
		if width == 4 {
			units = append(units, uint16(v))
		} else if utf8.ValidRune(rune(v)) {
			sb.WriteRune(rune(v))
		}
	}
	sb.WriteString(string(utf16.Decode(units)))
	return sb.String()
}

// DecodeText decodes a text string such as a title or a bookmark, which
// is stored as UTF-16, UTF-8 or PDFDocEncoding.
func DecodeText(s String) string {
	switch {
	case strings.HasPrefix(string(s), "\xfe\xff"):
		return decodeUTF16(s[2:], false)
	case strings.HasPrefix(string(s), "\xff\xfe"):
		return decodeUTF16(s[2:], true)
	case strings.HasPrefix(string(s), "\xef\xbb\xbf"):
		return strings.ToValidUTF8(string(s[3:]), "")
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		sb.WriteString(pdfDoc[s[i]])
	}
	return sb.String()
}

// decodeUTF16 decodes UTF-16 text in the given byte order.
func decodeUTF16(s String, little bool) string {
	units := make([]uint16, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		hi, lo := s[i], s[i+1]
		if little {
			hi, lo = lo, hi
		}
		units = append(units, uint16(hi)<<8|uint16(lo))
	}
	return string(utf16.Decode(units))
}
