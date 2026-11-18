package reflow

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/manzanit0/athenaeum/internal/pdf"
)

// Limits of the search for running headers, footers and page numbers.
const (
	// marginShare is the share of the page height, at the top and at
	// the bottom, in which furniture is looked for.
	marginShare = 0.12
	// neighbourPages is how many pages before and after a page are
	// searched for the same header or footer.
	neighbourPages = 4
	// repeats is how many nearby pages must carry the same text at the
	// same height for it to be furniture.
	repeats = 2
	// samePlace is how far apart, in points, two pieces of text may sit
	// and still count as being at the same height.
	samePlace = 4
	// segmentGap is the horizontal gap, as a share of the font size,
	// that separates two pieces of furniture on one line, such as a
	// title on the left and a page number on the right.
	segmentGap = 2.5
)

// pageNumber matches text that holds nothing but a page number.
var pageNumber = regexp.MustCompile(`(?i)^\W*(page\s+)?(\d{1,4}|[ivxlc]{1,7})(\s*(of|/)\s*\d{1,4})?\W*$`)

// segment is a piece of text in the top or bottom margin of a page.
type segment struct {
	// key is the text with its digits masked, so pieces that only
	// differ by a page number compare equal.
	key string
	// number reports whether the text is nothing but a page number.
	number bool
	// y is the height of the baseline.
	y float64
	// runs holds the indexes of the runs the piece is made of.
	runs []int
}

// furnitureKey returns text in lower case with its digits masked.
func furnitureKey(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return '#'
		}
		return unicode.ToLower(r)
	}, strings.Join(strings.Fields(text), " "))
}

// inMargin reports whether a baseline at height y lies in the top or
// bottom margin of a page of the given height.
func inMargin(y, height float64) bool {
	return y < marginShare*height || y > (1-marginShare)*height
}

// marginSegments returns the pieces of text in the margins of a page.
func marginSegments(runs []pdf.Run, height float64) []segment {
	var indexes []int
	for i, run := range runs {
		if inMargin(run.Y, height) {
			indexes = append(indexes, i)
		}
	}
	sort.SliceStable(indexes, func(a, b int) bool {
		ra, rb := runs[indexes[a]], runs[indexes[b]]
		if math.Abs(ra.Y-rb.Y) > baselineSlack*math.Max(ra.Size, rb.Size) {
			return ra.Y < rb.Y
		}
		return ra.X < rb.X
	})
	var segments []segment
	var text strings.Builder
	finish := func() {
		if n := len(segments); n > 0 {
			segments[n-1].key = furnitureKey(text.String())
			segments[n-1].number = pageNumber.MatchString(strings.TrimSpace(text.String()))
		}
		text.Reset()
	}
	end := 0.0
	for n, i := range indexes {
		run := runs[i]
		apart := n == 0 || math.Abs(run.Y-segments[len(segments)-1].y) > baselineSlack*run.Size ||
			run.X-end > segmentGap*run.Size
		if apart {
			finish()
			segments = append(segments, segment{y: run.Y})
		}
		last := &segments[len(segments)-1]
		last.runs = append(last.runs, i)
		text.WriteString(" " + run.Text)
		end = run.X + run.Width
	}
	finish()
	return segments
}

// isFurniture reports whether the segment s of the page with the given
// index is a page number, or text that nearby pages repeat at the same
// height. margins holds the segments of every page read so far.
func isFurniture(s segment, index int, margins [][]segment) bool {
	if s.number {
		return true
	}
	count := 0
	for other := max(0, index-neighbourPages); other < len(margins) && other <= index+neighbourPages; other++ {
		if other == index {
			continue
		}
		for _, candidate := range margins[other] {
			if candidate.key == s.key && math.Abs(candidate.y-s.y) < samePlace {
				count++
				break
			}
		}
	}
	needed := min(repeats, len(margins)-1)
	return needed > 0 && count >= needed
}

// withoutFurniture returns the runs of the page with the given index
// that are not running headers, footers or page numbers.
func withoutFurniture(runs []pdf.Run, index int, margins [][]segment) []pdf.Run {
	drop := map[int]bool{}
	for _, s := range margins[index] {
		if isFurniture(s, index, margins) {
			for _, i := range s.runs {
				drop[i] = true
			}
		}
	}
	if len(drop) == 0 {
		return runs
	}
	kept := runs[:0]
	for i, run := range runs {
		if !drop[i] {
			kept = append(kept, run)
		}
	}
	return kept
}
