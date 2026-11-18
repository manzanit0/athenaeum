package pdf

import (
	"testing"
	"unicode/utf8"
)

// TestEncodingTables checks the sizes and some entries of the built in
// tables.
func TestEncodingTables(t *testing.T) {
	sizes := map[string]int{macRomanHigh: 128, winAnsiHigh: 32, pdfDocHigh: 31, pdfDocLow: 8, symbolLow: 95, symbolHigh: 70}
	for table, want := range sizes {
		if got := utf8.RuneCountInString(table); got != want {
			t.Errorf("table starting %.8q has %d entries, want %d", table, got, want)
		}
	}
	entries := map[string]string{
		winAnsi[0x93]: "“", winAnsi[0xE9]: "é", macRoman[0x8E]: "é", macRoman[0xDE]: "ﬁ",
		standard[0x27]: "’", standard[0xAE]: "fi", standard[0xFB]: "ß", pdfDoc[0xA0]: "€",
		pdfDoc[0x8A]: "−", glyphNames["zcaron"]: "ž", glyphNames["longs"]: "ſ",
		glyphNames["ydieresis"]: "ÿ", glyphNames["asciitilde"]: "~", symbol[0x61]: "α", symbol[0x7E]: "∼",
		symbol[0xB7]: "•", symbol[0xE5]: "∑", symbol[0x57]: "Ω",
	}
	for got, want := range entries {
		if got != want {
			t.Errorf("table entry = %q, want %q", got, want)
		}
	}
}

// TestGlyphText decodes glyph names of every supported form.
func TestGlyphText(t *testing.T) {
	names := map[string]string{
		"A": "A", "eacute": "é", "f_f_i": "ffi", "one.oldstyle": "1", "uni00E90041": "éA",
		"u1F600": "😀", "uniD83DDE00": "😀", "g123": "", "Scaron.sc": "Š", "fl": "fl",
	}
	for name, want := range names {
		if got := glyphText(name); got != want {
			t.Errorf("glyphText(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestDecodeText decodes text strings in each supported encoding.
func TestDecodeText(t *testing.T) {
	strings := map[String]string{
		"plain":                            "plain",
		"caf\xe9 \x93x\x94":                "café ﬁxﬂ",
		"\xfe\xff\x00\x41\xd8\x3d\xde\x00": "A😀",
		"\xff\xfe\x41\x00":                 "A",
		"\xef\xbb\xbfcaf\xc3\xa9":          "café",
	}
	for raw, want := range strings {
		if got := DecodeText(raw); got != want {
			t.Errorf("DecodeText(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestLexer reads objects of every kind.
func TestLexer(t *testing.T) {
	l := lexer{data: []byte(`<< /Na#6De (a\(b\)\101\n) /Arr [1 -2.5 +3 .5 true null <48 6>] /Ref 12 0 R /Inner << /K /V >> >> BT`), refs: true}
	obj, ok := l.object()
	dict, isDict := obj.(Dict)
	if !ok || !isDict {
		t.Fatalf("object = %#v", obj)
	}
	if dict["Name"] != String("a(b)A\n") || dict["Ref"] != (Ref{Num: 12}) {
		t.Errorf("dict = %#v", dict)
	}
	arr := dict["Arr"].(Array)
	want := Array{int64(1), -2.5, int64(3), 0.5, true, nil, String("H`")}
	if len(arr) != len(want) {
		t.Fatalf("array = %#v", arr)
	}
	for i := range want {
		if arr[i] != want[i] {
			t.Errorf("array[%d] = %#v, want %#v", i, arr[i], want[i])
		}
	}
	if inner := dict["Inner"].(Dict); inner["K"] != Name("V") {
		t.Errorf("inner = %#v", inner)
	}
	if op, _ := l.object(); op != keyword("BT") {
		t.Errorf("operator = %#v", op)
	}
	if _, ok := l.object(); ok {
		t.Error("object reported more data at the end")
	}
}
