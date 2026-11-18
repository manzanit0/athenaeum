package kf8

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"unicode/utf8"
)

// Index record limits.
const (
	// indexHeaderLength is the length of the header of an index record.
	indexHeaderLength = 192
	// indexBlockLimit bounds the entry data held by one index record, so
	// the two byte offsets into it never overflow.
	indexBlockLimit = 0xE000
	// stringRecordLimit bounds the strings held by one string record.
	stringRecordLimit = 0x10000 - 1024
	// maxStringLength bounds the length of one index string in bytes.
	maxStringLength = 500
)

// buffer accumulates big-endian binary data.
type buffer struct {
	bytes.Buffer
}

// u16 appends values as 16 bit integers.
func (b *buffer) u16(values ...int) {
	for _, v := range values {
		b.Write(binary.BigEndian.AppendUint16(nil, uint16(v)))
	}
}

// u32 appends values as 32 bit integers.
func (b *buffer) u32(values ...int) {
	for _, v := range values {
		b.Write(binary.BigEndian.AppendUint32(nil, uint32(v)))
	}
}

// fill appends n copies of the byte c.
func (b *buffer) fill(c byte, n int) {
	b.Write(bytes.Repeat([]byte{c}, n))
}

// align pads the data with zero bytes to a multiple of four.
func (b *buffer) align() {
	b.fill(0, (4-b.Len()%4)%4)
}

// null is the value of 32 bit fields that are not set.
const null = 0xFFFFFFFF

// varint encodes v as a variable width integer: seven bits per byte, most
// significant first, with the high bit set on the last byte.
func varint(v int) []byte {
	out := []byte{byte(v&0x7F) | 0x80}
	for v >>= 7; v > 0; v >>= 7 {
		out = append([]byte{byte(v & 0x7F)}, out...)
	}
	return out
}

// tagDef describes one kind of value an index entry can carry.
type tagDef struct {
	// number identifies the tag.
	number byte
	// values is how many integers make up one occurrence of the tag.
	values byte
	// mask selects the control byte bits counting the occurrences.
	mask byte
}

// endTag closes the tags that share one control byte.
var endTag = tagDef{}

// indexEntry is one entry of an index.
type indexEntry struct {
	// key is the text identifying the entry.
	key string
	// values holds the integers of every tag of the index, in the order
	// of its tag table without the end markers. A nil slice leaves the
	// tag out.
	values [][]int
}

// index is a table of entries stored as index records.
type index struct {
	// tags is the tag table, with an endTag closing every control byte.
	tags []tagDef
	// entries are the rows of the table.
	entries []indexEntry
	// strings is the number of string records that follow the index.
	strings int
}

// tagTable returns the TAGX block describing the tags of the index.
func (ix *index) tagTable() []byte {
	var b buffer
	controlBytes := 0
	var table []byte
	for _, tag := range ix.tags {
		if tag == endTag {
			controlBytes++
			table = append(table, 0, 0, 0, 1)
			continue
		}
		table = append(table, tag.number, tag.values, tag.mask, 0)
	}
	b.WriteString("TAGX")
	b.u32(12+len(table), controlBytes)
	b.Write(table)
	return b.Bytes()
}

// render returns the binary form of an entry: its key, the control bytes
// counting the occurrences of each tag and the values themselves.
func (ix *index) render(e indexEntry) []byte {
	out := append([]byte{byte(len(e.key))}, e.key...)
	var control byte
	var body []byte
	next := 0
	for _, tag := range ix.tags {
		if tag == endTag {
			out = append(out, control)
			control = 0
			continue
		}
		values := e.values[next]
		next++
		count := len(values) / int(tag.values)
		control |= tag.mask & byte(count<<bits.TrailingZeros8(tag.mask))
		for _, v := range values {
			body = append(body, varint(v)...)
		}
	}
	return append(out, body...)
}

// block is the entry data of one index record.
type block struct {
	// data holds the rendered entries.
	data buffer
	// offsets locates every entry within the record.
	offsets []int
	// last is the key of the last entry.
	last string
}

// blocks renders the entries and groups them into index records.
func (ix *index) blocks() []*block {
	current := &block{}
	out := []*block{current}
	for _, e := range ix.entries {
		raw := ix.render(e)
		if current.data.Len()+len(raw) > indexBlockLimit {
			current = &block{}
			out = append(out, current)
		}
		current.offsets = append(current.offsets, indexHeaderLength+current.data.Len())
		current.data.Write(raw)
		current.last = e.key
	}
	return out
}

// offsetTable returns the IDXT block listing the offsets of the entries
// of a record.
func offsetTable(offsets []int) []byte {
	var b buffer
	b.WriteString("IDXT")
	b.u16(offsets...)
	b.align()
	return b.Bytes()
}

// dataRecord returns the index record holding the entries of blk.
func dataRecord(blk *block) []byte {
	blk.data.align()
	var b buffer
	b.WriteString("INDX")
	b.u32(indexHeaderLength, 0, 1, 0, indexHeaderLength+blk.data.Len(), len(blk.offsets))
	b.fill(0xFF, 8)
	b.fill(0, 156)
	b.Write(blk.data.Bytes())
	b.Write(offsetTable(blk.offsets))
	return b.Bytes()
}

// headerRecord returns the first record of the index, which carries the
// tag table and, for every data record, its last key and entry count.
func (ix *index) headerRecord(blocks []*block) []byte {
	tagx := ix.tagTable()
	var summary buffer
	var offsets []int
	for _, blk := range blocks {
		offsets = append(offsets, indexHeaderLength+len(tagx)+summary.Len())
		summary.WriteByte(byte(len(blk.last)))
		summary.WriteString(blk.last)
		summary.u16(len(blk.offsets))
	}
	summary.align()
	var b buffer
	b.WriteString("INDX")
	b.u32(indexHeaderLength, 0, 0, 2, indexHeaderLength+len(tagx)+summary.Len(),
		len(blocks), 65001, null, len(ix.entries), 0, 0, 0, ix.strings)
	b.fill(0, 124)
	b.u32(indexHeaderLength, 0, 0)
	b.Write(tagx)
	b.Write(summary.Bytes())
	b.Write(offsetTable(offsets))
	return b.Bytes()
}

// records returns the header record of the index followed by its data
// records.
func (ix *index) records() [][]byte {
	blocks := ix.blocks()
	out := [][]byte{ix.headerRecord(blocks)}
	for _, blk := range blocks {
		out = append(out, dataRecord(blk))
	}
	return out
}

// stringTable stores the strings referenced by index entries.
type stringTable struct {
	// records holds the finished string records.
	records [][]byte
	// current is the record being filled.
	current buffer
	// offsets remembers where each string was stored.
	offsets map[string]int
}

// add stores s, unless it is already stored, and returns its offset.
func (t *stringTable) add(s string) int {
	if offset, ok := t.offsets[s]; ok {
		return offset
	}
	if t.offsets == nil {
		t.offsets = map[string]int{}
	}
	text := []byte(s)
	if len(text) > maxStringLength {
		cut := maxStringLength
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = text[:cut]
	}
	raw := append(varint(len(text)), text...)
	if t.current.Len()+len(raw) > stringRecordLimit {
		t.flush()
	}
	offset := len(t.records)*0x10000 + t.current.Len()
	t.current.Write(raw)
	t.offsets[s] = offset
	return offset
}

// flush finishes the record being filled.
func (t *stringTable) flush() {
	if t.current.Len() == 0 {
		return
	}
	t.current.align()
	t.records = append(t.records, bytes.Clone(t.current.Bytes()))
	t.current.Reset()
}

// finish returns the string records.
func (t *stringTable) finish() [][]byte {
	t.flush()
	return t.records
}
