package reflow

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/manzanit0/athenaeum/internal/pdf"
)

// Limits of the split into sections.
const (
	// chunkPages is how many pages a section holds when the document
	// offers nothing to split it by.
	chunkPages = 25
	// tocDepth is how many levels of entries below the sections the
	// table of contents shows.
	tocDepth = 2
	// fineSections is the number of sections per page above which a
	// split is too fine to be the chapters of a book.
	fineSections = 1.5
	// manySections is the number of sections below which a split is
	// never too fine.
	manySections = 20
)

// section is one file of the book.
type section struct {
	// title is the title of the section.
	title string
	// elements holds the content.
	elements []element
}

// entry is one line of the table of contents.
type entry struct {
	// title is the text of the line.
	title string
	// level is the nesting depth, starting at zero.
	level int
	// section is the index of the section the line points into.
	section int
	// id is the anchor within that section, or empty for its start.
	id string
}

// mark is a place where the table of contents points.
type mark struct {
	// title is the text of the entry.
	title string
	// level is the nesting depth, starting at zero.
	level int
	// index is the index of the element the entry points at.
	index int
}

// normalize reduces a title to its letters and digits in lower case, so
// titles can be compared whatever their spacing and punctuation.
func normalize(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, text)
}

// locate returns the index of the element a bookmark points at: the
// element on its page that carries its title, else the first element at
// or below its position, else the first element after it.
func locate(elements []element, mark pdf.Bookmark) int {
	title := normalize(mark.Title)
	fallback := -1
	for i, e := range elements {
		if e.page < mark.Page {
			continue
		}
		if e.page > mark.Page {
			if fallback < 0 {
				fallback = i
			}
			break
		}
		text := normalize(e.text())
		if title != "" && text != "" && (strings.HasPrefix(text, title) || strings.HasPrefix(title, text)) {
			return i
		}
		if fallback < 0 && (mark.Top < 0 || e.y+e.size >= mark.Top-e.size) {
			fallback = i
		}
	}
	return fallback
}

// outlineMarks turns the bookmarks of the document into marks. The
// shallowest level that holds at least two bookmarks becomes level
// zero, and bookmarks above it are dropped.
func outlineMarks(elements []element, outline []pdf.Bookmark) []mark {
	counts := map[int]int{}
	for _, b := range outline {
		if b.Page >= 0 {
			counts[b.Level]++
		}
	}
	base := -1
	for level := 0; level < 8 && base < 0; level++ {
		if counts[level] >= 2 {
			base = level
		}
	}
	if base < 0 {
		return nil
	}
	var marks []mark
	for _, b := range outline {
		title := strings.Join(strings.Fields(b.Title), " ")
		if b.Page < 0 || b.Level < base || b.Level > base+tocDepth || title == "" {
			continue
		}
		index := locate(elements, b)
		if index < 0 || (len(marks) > 0 && index < marks[len(marks)-1].index) {
			continue
		}
		marks = append(marks, mark{title: title, level: b.Level - base, index: index})
	}
	return marks
}

// sectionNumber matches the number that starts a numbered heading, such
// as "2.1" in "2.1 Scope".
var sectionNumber = regexp.MustCompile(`^(\d+(\.\d+)*)\.?\s`)

// numberDepth returns how many levels below the top a numbered heading
// is, going by its number: zero for "2", one for "2.1" and so on.
func numberDepth(title string) int {
	m := sectionNumber.FindStringSubmatch(title)
	if m == nil {
		return 0
	}
	return strings.Count(m[1], ".")
}

// headingMarks turns the headings of the document into marks. The
// highest heading level that occurs at least twice, counting the levels
// above it, becomes level zero.
func headingMarks(elements []element) []mark {
	counts := [4]int{}
	for _, e := range elements {
		if e.kind == heading {
			counts[e.level]++
		}
	}
	base, total := 0, 0
	for level := 1; level <= 3 && base == 0; level++ {
		if total += counts[level]; total >= 2 {
			base = level
		}
	}
	if base == 0 {
		return nil
	}
	var marks []mark
	for i, e := range elements {
		if e.kind != heading || e.text() == "" {
			continue
		}
		if level := max(e.level-base, 0) + numberDepth(e.text()); level <= tocDepth {
			marks = append(marks, mark{title: e.text(), level: level, index: i})
		}
	}
	return marks
}

// tooFine reports whether marks would cut a document of the given
// number of pages into more sections than chapters would, as happens
// when every bold line of a reference page is taken for a heading.
func tooFine(marks []mark, pages int) bool {
	sections := 0
	for _, m := range marks {
		if m.level == 0 {
			sections++
		}
	}
	return sections > manySections && float64(sections) > fineSections*float64(pages)
}

// pageMarks splits the document every few pages, for documents without
// bookmarks or headings. Documents that fit in one such part get no
// marks.
func pageMarks(elements []element) []mark {
	var marks []mark
	chunk := -1
	for i, e := range elements {
		if c := e.page / chunkPages; c > chunk {
			chunk = c
			first := c*chunkPages + 1
			marks = append(marks, mark{title: fmt.Sprintf("Pages %d to %d", first, first+chunkPages-1), index: i})
		}
	}
	if len(marks) < 2 {
		return nil
	}
	return marks
}

// split cuts elements into sections at the marks of level zero and
// returns them with the table of contents. Elements before the first
// mark form a section named after the book.
func split(elements []element, marks []mark, title string) ([]section, []entry) {
	var sections []section
	var entries []entry
	start := 0
	open := func(name string, from int) {
		if len(sections) > 0 {
			sections[len(sections)-1].elements = elements[start:from]
		}
		sections = append(sections, section{title: name})
		start = from
	}
	if len(marks) == 0 || marks[0].index > 0 || marks[0].level > 0 {
		open(title, 0)
		entries = append(entries, entry{title: title})
	}
	for n, m := range marks {
		if m.level == 0 && (m.index > start || len(sections) == 0) {
			open(m.title, m.index)
			entries = append(entries, entry{title: m.title, section: len(sections) - 1})
			continue
		}
		e := &elements[m.index]
		if e.id == "" {
			e.id = fmt.Sprintf("a%d", n+1)
		}
		entries = append(entries, entry{
			title: m.title, level: max(m.level, 1), section: len(sections) - 1, id: e.id,
		})
	}
	sections[len(sections)-1].elements = elements[start:]
	return sections, entries
}
