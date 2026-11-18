package kf8

import "errors"

// recordSize is the length of the text held by one text record before it
// is compressed.
const recordSize = 4096

// PalmDOC compression limits.
const (
	// maxDistance is how far back a repeated sequence may start.
	maxDistance = 2047
	// minMatch is the shortest repeated sequence worth encoding.
	minMatch = 3
	// maxMatch is the longest repeated sequence one code can copy.
	maxMatch = 10
	// maxLiterals is the longest run of escaped bytes one code can hold.
	maxLiterals = 8
	// maxCandidates bounds how many earlier positions are tried per byte.
	maxCandidates = 48
	// hashBits is the size of the hash of a three byte sequence.
	hashBits = 12
)

// errCorrupt is returned for PalmDOC data that cannot be expanded.
var errCorrupt = errors.New("kf8: corrupt PalmDOC data")

// matcher finds repeated sequences in one record with hash chains.
type matcher struct {
	// data is the record being compressed.
	data []byte
	// head maps the hash of a sequence to its latest position plus one.
	head [1 << hashBits]int
	// prev links a position to the previous one with the same hash,
	// stored plus one.
	prev []int
}

// hash returns the hash of the three bytes at position i.
func (m *matcher) hash(i int) int {
	d := m.data
	return (int(d[i])<<8 ^ int(d[i+1])<<4 ^ int(d[i+2])) & (1<<hashBits - 1)
}

// insert records position i so later positions can refer back to it.
func (m *matcher) insert(i int) {
	if i+minMatch > len(m.data) {
		return
	}
	h := m.hash(i)
	m.prev[i] = m.head[h]
	m.head[h] = i + 1
}

// find returns the distance and length of the longest earlier copy of
// the bytes at position i, or a zero length when there is none.
func (m *matcher) find(i int) (distance, length int) {
	if i+minMatch > len(m.data) {
		return 0, 0
	}
	limit := min(maxMatch, len(m.data)-i)
	tries := 0
	for at := m.head[m.hash(i)]; at > 0 && tries < maxCandidates; at = m.prev[at-1] {
		start := at - 1
		if i-start > maxDistance {
			break
		}
		n := 0
		for n < limit && m.data[start+n] == m.data[i+n] {
			n++
		}
		if n > length {
			distance, length = i-start, n
		}
		tries++
	}
	if length < minMatch {
		return 0, 0
	}
	return distance, length
}

// plain reports whether c stands for itself in PalmDOC data.
func plain(c byte) bool {
	return c == 0 || (c > 8 && c < 0x80)
}

// literals returns how many bytes starting at position i must be written
// as one escaped run.
func literals(data []byte, i int) int {
	n := 1
	for n < maxLiterals && i+n < len(data) && !plain(data[i+n]) {
		n++
	}
	return n
}

// compress packs the text of one record with PalmDOC compression: codes
// 0x80-0xBF copy an earlier sequence, 0xC0-0xFF stand for a space and a
// character, 0x01-0x08 escape the bytes that follow and everything else
// stands for itself.
func compress(data []byte) []byte {
	m := &matcher{data: data, prev: make([]int, len(data))}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		step := 1
		c := data[i]
		distance, length := m.find(i)
		switch {
		case length > 0:
			code := 0x8000 | distance<<3 | (length - minMatch)
			out = append(out, byte(code>>8), byte(code))
			step = length
		case c == ' ' && i+1 < len(data) && data[i+1] >= 0x40 && data[i+1] < 0x80:
			out = append(out, data[i+1]^0x80)
			step = 2
		case plain(c):
			out = append(out, c)
		default:
			step = literals(data, i)
			out = append(out, byte(step))
			out = append(out, data[i:i+step]...)
		}
		for end := i + step; i < end; i++ {
			m.insert(i)
		}
	}
	return out
}

// decompress expands PalmDOC compressed data.
func decompress(data []byte) ([]byte, error) {
	out := make([]byte, 0, recordSize)
	for i := 0; i < len(data); {
		c := data[i]
		i++
		switch {
		case c >= 1 && c <= 8:
			if i+int(c) > len(data) {
				return nil, errCorrupt
			}
			out = append(out, data[i:i+int(c)]...)
			i += int(c)
		case c < 0x80:
			out = append(out, c)
		case c >= 0xC0:
			out = append(out, ' ', c^0x80)
		default:
			if i >= len(data) {
				return nil, errCorrupt
			}
			code := (int(c)<<8 | int(data[i])) & 0x3FFF
			i++
			distance, length := code>>3, code&7+minMatch
			if distance == 0 || distance > len(out) {
				return nil, errCorrupt
			}
			for range length {
				out = append(out, out[len(out)-distance])
			}
		}
	}
	return out, nil
}
