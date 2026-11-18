package kf8

// Trailing byte sequences tell the Kindle which lines of the table of
// contents touch a text record: which start, end, lie within or span it.
// The lines of a record are arranged in strands, each running from a top
// level line down through its first children, and every depth of a
// strand is encoded as one sequence. The encoding follows KindleGen's as
// worked out by calibre and KindleUnpack.

// Flags of a sequence.
const (
	// flagSpans says the last line of the sequence spans the record.
	flagSpans = 1
	// flagKind carries the kind of indexing; set on the first sequence.
	flagKind = 2
	// flagCount carries the number of lines the sequence stands for.
	flagCount = 4
	// flagLater marks the first sequence of a second or later strand.
	flagLater = 8
)

// Kinds of indexing.
const (
	// kindBook is the kind used for books.
	kindBook = 8
	// kindFlat is the kind used when a later strand starts further on
	// than the strand before it ended.
	kindFlat = 5
)

// touch is a line of the table of contents as it relates to one record.
type touch struct {
	// row is the line.
	row tocRow
	// index is the position of the line in the table.
	index int
	// spans reports whether the line starts before the record and ends
	// after it.
	spans bool
}

// touches returns the lines of rows that overlap the record covering
// the text from start up to end, ordered by where they start.
func touches(rows []tocRow, order []int, start, end int) []touch {
	var out []touch
	for _, i := range order {
		row := rows[i]
		if row.offset >= end {
			break
		}
		if row.offset+row.length <= start {
			continue
		}
		out = append(out, touch{row: row, index: i, spans: row.offset < start && row.offset+row.length > end})
	}
	return out
}

// take removes and returns the first line of rest accepted by match.
func take(rest *[]touch, match func(touch) bool) (touch, bool) {
	for i, t := range *rest {
		if match(t) {
			*rest = append((*rest)[:i:i], (*rest)[i+1:]...)
			return t, true
		}
	}
	return touch{}, false
}

// hasChild reports whether rest holds a line nested under parent.
func hasChild(rest []touch, parent int) bool {
	for _, t := range rest {
		if t.row.parent == parent {
			return true
		}
	}
	return false
}

// strand removes from rest the lines that belong with top and returns
// them after it: its first child and that child's strand, or else the
// siblings that directly follow it.
func strand(top touch, rest *[]touch) []touch {
	out := []touch{top}
	child, ok := take(rest, func(t touch) bool { return t.row.parent == top.index })
	if ok {
		return append(out, strand(child, rest)...)
	}
	current := top.index
	for {
		sibling, ok := take(rest, func(t touch) bool {
			return t.row.depth == top.row.depth && t.row.parent == top.row.parent && t.index == current+1
		})
		if !ok {
			return out
		}
		current++
		if hasChild(*rest, sibling.index) {
			return append(out, strand(sibling, rest)...)
		}
		out = append(out, sibling)
	}
}

// layers splits a strand into its runs of lines at the same depth, in
// the order the depths first appear.
func layers(lines []touch) [][]touch {
	var depths []int
	byDepth := map[int][]touch{}
	for _, t := range lines {
		if _, seen := byDepth[t.row.depth]; !seen {
			depths = append(depths, t.row.depth)
		}
		byDepth[t.row.depth] = append(byDepth[t.row.depth], t)
	}
	out := make([][]touch, len(depths))
	for i, depth := range depths {
		out[i] = byDepth[depth]
	}
	return out
}

// sequence is the encoded form of one layer of a strand.
type sequence struct {
	// value is the index of the first line, relative to its parent or,
	// for a later strand, to the end of the strand before.
	value int
	// flags says which of the extra fields are present.
	flags int
	// kind is the kind of indexing.
	kind int
	// count is the number of lines.
	count int
}

// bytes returns the binary form of the sequence. The first sequence of
// a record has three flag bits, the others four.
func (s sequence) bytes(first bool) []byte {
	width := 4
	if first {
		width = 3
	}
	out := varint(s.value<<width | s.flags&(1<<width-1))
	if s.flags&flagKind != 0 {
		out = append(out, varint(s.kind)...)
	}
	if s.flags&flagCount != 0 {
		out = append(out, byte(s.count))
	}
	if s.flags&flagSpans != 0 {
		out = append(out, varint(0)...)
	}
	return out
}

// sequences encodes the lines touching one record. It reports false
// when a later strand starts further on than the one before it ended,
// which the given kind of indexing cannot express.
func sequences(lines []touch, kind int) ([]sequence, bool) {
	var out []sequence
	last := 0
	started := false
	for len(lines) > 0 {
		top := lines[0]
		lines = lines[1:]
		var own []sequence
		for _, layer := range layers(strand(top, &lines)) {
			s := sequence{value: layer[0].index - max(layer[0].row.parent, 0)}
			if layer[len(layer)-1].spans {
				s.flags |= flagSpans
			}
			if !started {
				s.flags, s.kind, started = s.flags|flagKind, kind, true
			}
			if len(layer) > 1 {
				s.flags, s.count = s.flags|flagCount, len(layer)
			}
			if len(out) > 0 && len(own) == 0 {
				s.value = last - layer[0].index
				switch {
				case s.value >= 0:
					s.flags |= flagLater
				case kind == kindFlat:
					s.value = -s.value
				default:
					return nil, false
				}
			}
			last = layer[len(layer)-1].index
			own = append(own, s)
		}
		for i := 0; i+1 < len(own); i++ {
			if own[i].flags&own[i+1].flags&flagSpans != 0 {
				own[i].flags &^= flagSpans
			}
		}
		out = append(out, own...)
	}
	return out, true
}

// trailer returns the trailing entry holding data: the data followed by
// the length of the entry, stored so it can be read from the end.
func trailer(data []byte) []byte {
	for width := 1; ; width++ {
		size := len(data) + width
		if size < 1<<(7*width) {
			out := append([]byte{}, data...)
			for i := width - 1; i >= 0; i-- {
				b := byte(size>>(7*i)) & 0x7F
				if i == width-1 {
					b |= 0x80
				}
				out = append(out, b)
			}
			return out
		}
	}
}

// indexing returns the trailing entry of every text record of a text of
// the given length, describing the lines of rows that touch it.
func indexing(rows []tocRow, textLength int) [][]byte {
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	sortStable(order, func(a, b int) bool { return rows[a].offset < rows[b].offset })
	for _, kind := range []int{kindBook, kindFlat} {
		var out [][]byte
		ok := true
		for start := 0; start < textLength && ok; start += recordSize {
			var seqs []sequence
			seqs, ok = sequences(touches(rows, order, start, min(start+recordSize, textLength)), kind)
			var data []byte
			for i, s := range seqs {
				data = append(data, s.bytes(i == 0)...)
			}
			out = append(out, trailer(data))
		}
		if ok {
			return out
		}
	}
	return nil
}
