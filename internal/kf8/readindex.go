package kf8

import (
	"encoding/binary"
	"fmt"
	"math/bits"
)

// Tag numbers of the indexes of a KF8 book.
const (
	// tagCount is the number of chunks of a skeleton, or the offset of
	// a contents line.
	tagCount = 1
	// tagLength is the length of the text a contents line covers.
	tagLength = 2
	// tagLabel is the string offset of a contents title.
	tagLabel = 3
	// tagFile is the number of the file a chunk belongs to.
	tagFile = 3
	// tagDepth is the nesting depth of a contents line.
	tagDepth = 4
	// tagGeometry is a start and a length, or a chunk and an offset.
	tagGeometry = 6
)

// readVarint decodes a variable width integer at the start of data and
// returns it with the number of bytes it took.
func readVarint(data []byte) (value, size int) {
	for size < len(data) && size < 4 {
		b := data[size]
		size++
		value = value<<7 | int(b&0x7F)
		if b&0x80 != 0 {
			break
		}
	}
	return value, size
}

// field returns the 32 bit integer at offset in data, or zero when data
// is too short.
func field(data []byte, offset int) int {
	if offset < 0 || offset+4 > len(data) {
		return 0
	}
	return int(binary.BigEndian.Uint32(data[offset:]))
}

// readEntry is one entry read from an index.
type readEntry struct {
	// key is the text identifying the entry.
	key string
	// tags maps tag numbers to their values.
	tags map[int][]int
}

// value returns the value number i of the tag, or zero.
func (e readEntry) value(tag, i int) int {
	if values := e.tags[tag]; i < len(values) {
		return values[i]
	}
	return 0
}

// has reports whether the entry carries the tag.
func (e readEntry) has(tag int) bool {
	return len(e.tags[tag]) > 0
}

// readIndex is an index read from a book.
type readIndex struct {
	// entries are the rows of the index.
	entries []readEntry
	// strings maps offsets to the strings stored after the index.
	strings map[int]string
}

// readTags returns the tag table of an index header record and the
// number of control bytes its entries start with.
func readTags(header []byte) ([]tagDef, []bool, int, error) {
	start := field(header, 4)
	if start+12 > len(header) || string(header[start:start+4]) != "TAGX" {
		return nil, nil, 0, fmt.Errorf("%w: an index has no tag table", errCorrupt)
	}
	end := start + field(header, start+4)
	if end > len(header) {
		return nil, nil, 0, fmt.Errorf("%w: a tag table runs past its record", errCorrupt)
	}
	var tags []tagDef
	var ends []bool
	for at := start + 12; at+4 <= end; at += 4 {
		tags = append(tags, tagDef{header[at], header[at+1], header[at+2]})
		ends = append(ends, header[at+3] == 1)
	}
	return tags, ends, field(header, start+8), nil
}

// counted is a tag of an entry together with how its values are sized.
type counted struct {
	// tag is the tag.
	tag tagDef
	// count is the number of occurrences, or -1 when width is set.
	count int
	// width is the number of bytes the values take, when count is -1.
	width int
}

// readValues decodes the tags of one entry, whose control bytes start
// at data[0].
func readValues(tags []tagDef, ends []bool, controlBytes int, data []byte) map[int][]int {
	if controlBytes > len(data) {
		return nil
	}
	at := controlBytes
	next := func() int {
		value, size := readVarint(data[min(at, len(data)):])
		at += size
		return value
	}
	var present []counted
	control := 0
	for i, tag := range tags {
		if ends[i] {
			control++
			continue
		}
		if control >= controlBytes {
			break
		}
		masked := data[control] & tag.mask
		switch {
		case masked == 0:
		case masked == tag.mask && bits.OnesCount8(tag.mask) > 1:
			present = append(present, counted{tag: tag, count: -1, width: next()})
		default:
			present = append(present, counted{tag: tag, count: int(masked >> bits.TrailingZeros8(tag.mask))})
		}
	}
	out := map[int][]int{}
	for _, p := range present {
		var values []int
		if p.count >= 0 {
			for range p.count * int(p.tag.values) {
				values = append(values, next())
			}
		} else {
			for stop := at + p.width; at < stop && at < len(data); {
				values = append(values, next())
			}
		}
		out[int(p.tag.number)] = values
	}
	return out
}

// readStrings decodes a record of index strings into strings, keyed by
// their offsets plus base.
func readStrings(record []byte, base int, strings map[int]string) {
	for at := 0; at < len(record); {
		length, size := readVarint(record[at:])
		if length == 0 || at+size+length > len(record) {
			return
		}
		strings[base+at] = string(record[at+size : at+size+length])
		at += size + length
	}
}

// readIndexAt reads the index whose header record is number first,
// fetching records with get.
func readIndexAt(get func(int) ([]byte, error), first int) (*readIndex, error) {
	header, err := get(first)
	if err != nil || len(header) < indexHeaderLength {
		return nil, fmt.Errorf("%w: an index points outside the file", errCorrupt)
	}
	tags, ends, controlBytes, err := readTags(header)
	if err != nil {
		return nil, err
	}
	count := field(header, 24)
	ix := &readIndex{strings: map[int]string{}}
	for i := range field(header, 52) {
		if record, err := get(first + count + 1 + i); err == nil {
			readStrings(record, i*0x10000, ix.strings)
		}
	}
	for number := first + 1; number <= first+count; number++ {
		record, err := get(number)
		if err != nil {
			return nil, err
		}
		if err := ix.readRecord(record, tags, ends, controlBytes); err != nil {
			return nil, err
		}
	}
	return ix, nil
}

// readRecord adds the entries of one data record to the index.
func (ix *readIndex) readRecord(record []byte, tags []tagDef, ends []bool, controlBytes int) error {
	table := field(record, 20)
	entries := field(record, 24)
	if len(record) < indexHeaderLength || table+4+2*entries > len(record) {
		return fmt.Errorf("%w: an index record is cut short", errCorrupt)
	}
	for i := range entries {
		start := int(binary.BigEndian.Uint16(record[table+4+2*i:]))
		end := table
		if i+1 < entries {
			end = int(binary.BigEndian.Uint16(record[table+4+2*(i+1):]))
		}
		if start >= end || end > len(record) || start+1+int(record[start]) > end {
			return fmt.Errorf("%w: an index entry is out of place", errCorrupt)
		}
		key := string(record[start+1 : start+1+int(record[start])])
		ix.entries = append(ix.entries, readEntry{
			key: key, tags: readValues(tags, ends, controlBytes, record[start+1+len(key):end]),
		})
	}
	return nil
}
