package reflow

import (
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/manzanit0/athenaeum/internal/pdf"
)

// Thresholds of the layout analysis, as shares of the font size unless
// noted otherwise.
const (
	// wordGap is the horizontal gap that separates two words.
	wordGap = 0.15
	// baselineSlack is how far baselines may differ within one line.
	baselineSlack = 0.45
	// columnGap is the narrowest gutter between two columns.
	columnGap = 1.2
	// rowGap is the smallest vertical gap at which a region is cut.
	rowGap = 0.6
	// columnShare is the least share of the region width a column has.
	columnShare = 0.15
	// columnLines is the least number of lines a column has.
	columnLines = 3
	// minSize is the smallest font size, in points, that is read.
	minSize = 2.5
	// punctuationGap is the gap below which punctuation is attached to
	// the word before it, as happens after a word in italics.
	punctuationGap = 0.35
	// dropCapRatio is how much larger than the text around it an
	// initial letter is.
	dropCapRatio = 1.7
	// dropCapLines is how many lines an initial letter may span.
	dropCapLines = 4
)

// closing lists the punctuation that is attached to the word before it.
const closing = ",.;:!?)]\u201d\u2019"

// span is a stretch of text in one style.
type span struct {
	// text is the text of the span.
	text string
	// bold reports whether the text is bold.
	bold bool
	// italic reports whether the text is italic.
	italic bool
}

// line is one line of text on a page.
type line struct {
	// spans holds the text of the line.
	spans []span
	// x0 is the left edge.
	x0 float64
	// x1 is the right edge.
	x1 float64
	// y is the position of the baseline, measured from the page top.
	y float64
	// size is the font size most of the line is set in.
	size float64
	// page is the index of the page the line is on.
	page int
	// bold reports whether the whole line is bold.
	bold bool
}

// block is a group of lines that are read from top to bottom.
type block struct {
	// lines holds the lines in reading order.
	lines []line
	// flows reports whether the text before the block may continue in
	// it, which is the case at the top of a column or a page.
	flows bool
	// column tells the columns of a page apart.
	column int
}

// layout holds the lines of one page.
type layout struct {
	// blocks holds the lines in reading order.
	blocks []block
	// width is the width of the page.
	width float64
	// height is the height of the page.
	height float64
}

// text returns the text of the line.
func (l *line) text() string {
	var sb strings.Builder
	for _, s := range l.spans {
		sb.WriteString(s.text)
	}
	return sb.String()
}

// ligatures maps the ligature characters to the letters they join.
var ligatures = strings.NewReplacer("ﬀ", "ff", "ﬁ", "fi", "ﬂ", "fl", "ﬃ", "ffi",
	"ﬄ", "ffl", "ﬅ", "ft", "ﬆ", "st", " ", " ", " ", " ", " ", " ",
	" ", " ", " ", " ", "\t", " ")

// clean normalises the text of a run: ligatures become letters, unusual
// spaces become plain ones and characters that cannot be shown go.
func clean(text string) string {
	text = ligatures.Replace(text)
	return strings.Map(func(r rune) rune {
		if r == unicode.ReplacementChar || (unicode.IsControl(r) && r != ' ') || r == 0xFFFE || r == 0xFFFF {
			return -1
		}
		return r
	}, text)
}

// usable returns the runs of a page that take part in the text flow,
// with their text cleaned, and how many were left out for being turned.
func usable(c *pdf.Content) ([]pdf.Run, int) {
	var runs []pdf.Run
	turned := 0
	for _, run := range c.Runs {
		run.Text = clean(run.Text)
		if strings.TrimSpace(run.Text) == "" || run.Size < minSize {
			continue
		}
		if !run.Upright {
			turned++
			continue
		}
		if run.X+run.Width < 0 || run.X > c.Width || run.Y < 0 || run.Y > c.Height+run.Size {
			continue
		}
		runs = append(runs, run)
	}
	return runs, turned
}

// medianSize returns the median font size of runs.
func medianSize(runs []pdf.Run) float64 {
	sizes := make([]float64, len(runs))
	for i, run := range runs {
		sizes[i] = run.Size
	}
	sort.Float64s(sizes)
	return sizes[len(sizes)/2]
}

// interval is a stretch along one axis.
type interval struct {
	// low is the start.
	low float64
	// high is the end.
	high float64
}

// widestGap returns the middle of the widest stretch not covered by any
// of the intervals, and its width.
func widestGap(intervals []interval) (float64, float64) {
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].low < intervals[j].low })
	at, width := 0.0, 0.0
	reach := intervals[0].high
	for _, iv := range intervals[1:] {
		if gap := iv.low - reach; gap > width {
			at, width = reach+gap/2, gap
		}
		reach = math.Max(reach, iv.high)
	}
	return at, width
}

// countLines returns the number of distinct baselines among runs.
func countLines(runs []pdf.Run) int {
	seen := map[int]bool{}
	for _, run := range runs {
		seen[int(math.Round(run.Y/math.Max(run.Size/2, 1)))] = true
	}
	return len(seen)
}

// splitColumns cuts runs at the widest gutter that separates two real
// columns, and returns the width of that gutter.
func splitColumns(runs []pdf.Run) (left, right []pdf.Run, width float64) {
	intervals := make([]interval, len(runs))
	minX, maxX := math.Inf(1), math.Inf(-1)
	for i, run := range runs {
		intervals[i] = interval{run.X, run.X + run.Width}
		minX, maxX = math.Min(minX, run.X), math.Max(maxX, run.X+run.Width)
	}
	at, width := widestGap(intervals)
	if width < columnGap*medianSize(runs) {
		return nil, nil, 0
	}
	for _, run := range runs {
		if run.X < at {
			left = append(left, run)
		} else {
			right = append(right, run)
		}
	}
	narrow := columnShare * (maxX - minX)
	if at-minX < narrow || maxX-at < narrow {
		return nil, nil, 0
	}
	if countLines(left) < columnLines || countLines(right) < columnLines {
		return nil, nil, 0
	}
	return left, right, width
}

// splitRows cuts runs at the tallest horizontal band of white space,
// and returns the height of that band.
func splitRows(runs []pdf.Run) (top, bottom []pdf.Run, height float64) {
	intervals := make([]interval, len(runs))
	for i, run := range runs {
		intervals[i] = interval{run.Y - 0.8*run.Size, run.Y + 0.2*run.Size}
	}
	at, height := widestGap(intervals)
	if height < rowGap*medianSize(runs) {
		return nil, nil, 0
	}
	for _, run := range runs {
		if run.Y < at {
			top = append(top, run)
		} else {
			bottom = append(bottom, run)
		}
	}
	return top, bottom, height
}

// cutter cuts the text of one page into blocks.
type cutter struct {
	// page is the index of the page.
	page int
	// columns counts the columns found so far.
	columns int
}

// arrange returns the runs of a page grouped into blocks in reading
// order.
func arrange(runs []pdf.Run, page int) []block {
	c := cutter{page: page}
	return c.cut(runs, true, 0)
}

// cut groups runs into blocks in reading order by cutting them into
// columns and rows in turn. Where both cuts are possible the wider band
// of white space wins, so a heading that sits below two columns is not
// read as part of the left one. flows tells whether the text before the
// runs may continue in them, and column names the column they are in.
func (c *cutter) cut(runs []pdf.Run, flows bool, column int) []block {
	if len(runs) == 0 {
		return nil
	}
	left, right, gutter := splitColumns(runs)
	top, bottom, band := splitRows(runs)
	switch {
	case gutter > 0 && gutter >= band:
		c.columns += 2
		first, second := c.columns-1, c.columns
		return append(c.cut(left, flows, first), c.cut(right, true, second)...)
	case band > 0:
		return append(c.cut(top, flows, column), c.cut(bottom, false, column)...)
	}
	return []block{{lines: buildLines(runs, c.page), flows: flows, column: column}}
}

// buildLines groups runs into lines from top to bottom.
func buildLines(runs []pdf.Run, page int) []line {
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Y < runs[j].Y })
	var groups [][]pdf.Run
	for _, run := range runs {
		n := len(groups)
		if n == 0 || run.Y-groups[n-1][0].Y > baselineSlack*math.Max(run.Size, groups[n-1][0].Size) {
			groups = append(groups, nil)
			n++
		}
		groups[n-1] = append(groups[n-1], run)
	}
	for i := range groups {
		sort.SliceStable(groups[i], func(a, b int) bool { return groups[i][a].X < groups[i][b].X })
	}
	attachDropCaps(groups)
	lines := make([]line, 0, len(groups))
	for _, group := range groups {
		if len(group) > 0 {
			lines = append(lines, buildLine(group, page))
		}
	}
	return lines
}

// attachDropCaps joins large initial letters to the words they begin.
// Such a letter sits on the baseline of the second or third line of its
// paragraph, so it is moved to the first of the lines set around it.
func attachDropCaps(groups [][]pdf.Run) {
	for i, group := range groups {
		if len(group) < 2 || !isDropCap(group[0], group[1]) {
			continue
		}
		initial := group[0]
		groups[i] = group[1:]
		target := i
		for j := i - 1; j >= 0 && i-j < dropCapLines && len(groups[j]) > 0; j-- {
			if groups[j][0].X < initial.X+initial.Width-1 {
				break
			}
			target = j
		}
		groups[target][0].Text = strings.TrimSpace(initial.Text) + strings.TrimLeft(groups[target][0].Text, " ")
	}
}

// isDropCap reports whether run is an initial letter set much larger
// than next, the text that follows it.
func isDropCap(run, next pdf.Run) bool {
	text := strings.TrimSpace(run.Text)
	return utf8.RuneCountInString(text) <= 2 && hasLetter(text) && run.Size >= dropCapRatio*next.Size
}

// duplicate reports whether run repeats an earlier run of the line, as
// happens when bold is faked by drawing text twice.
func duplicate(run pdf.Run, earlier []pdf.Run) bool {
	return slices.ContainsFunc(earlier, func(other pdf.Run) bool {
		return other.Text == run.Text && math.Abs(other.X-run.X) < breakSlack*run.Size &&
			math.Abs(other.Y-run.Y) < breakSlack*run.Size
	})
}

// breakSlack is how close, as a share of the font size, two runs with
// the same text are for one to count as a repeat of the other.
const breakSlack = 0.12

// separated reports whether run stands apart from the text that ends at
// the position end, so a space belongs between them.
func separated(run pdf.Run, end float64) bool {
	gap := run.X - end
	if first, _ := utf8.DecodeRuneInString(run.Text); strings.ContainsRune(closing, first) {
		return gap > punctuationGap*run.Size
	}
	return gap > wordGap*run.Size
}

// buildLine joins the runs of one line, which are in order from left to
// right, putting spaces where the runs are apart.
func buildLine(runs []pdf.Run, page int) line {
	out := line{x0: runs[0].X, page: page, bold: true}
	weights := map[float64]int{}
	baselines := map[float64]int{}
	end := runs[0].X
	for i, run := range runs {
		if duplicate(run, runs[:i]) {
			continue
		}
		text := run.Text
		if i > 0 && separated(run, end) && !strings.HasPrefix(text, " ") && !endsWithSpace(out.spans) {
			text = " " + text
		}
		out.spans = appendSpan(out.spans, span{text: text, bold: run.Bold, italic: run.Italic})
		out.bold = out.bold && run.Bold
		end = math.Max(end, run.X+run.Width)
		size := math.Round(run.Size*10) / 10
		weights[size] += len(run.Text)
		baselines[math.Round(run.Y*10)/10] += len(run.Text)
	}
	out.x1 = end
	out.size, out.y = heaviest(weights), heaviest(baselines)
	return out
}

// heaviest returns the key with the largest weight, preferring the
// smaller key on a tie so the result does not depend on map order.
func heaviest(weights map[float64]int) float64 {
	best, bestWeight := 0.0, -1
	for value, weight := range weights {
		if weight > bestWeight || (weight == bestWeight && value < best) {
			best, bestWeight = value, weight
		}
	}
	return best
}

// endsWithSpace reports whether the text of spans ends with a space.
func endsWithSpace(spans []span) bool {
	return len(spans) > 0 && strings.HasSuffix(spans[len(spans)-1].text, " ")
}

// appendSpan adds s to spans, merging it into the last span when the
// styles match.
func appendSpan(spans []span, s span) []span {
	if n := len(spans); n > 0 && spans[n-1].bold == s.bold && spans[n-1].italic == s.italic {
		spans[n-1].text += s.text
		return spans
	}
	return append(spans, s)
}
