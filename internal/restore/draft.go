package restore

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// The kinds of blocks a page is transcribed into.
const (
	// kindHeading is the title of a part, chapter or section.
	kindHeading = "heading"
	// kindParagraph is a paragraph of running text.
	kindParagraph = "paragraph"
	// kindQuote is a quotation set apart from the text, or verse.
	kindQuote = "quote"
	// kindItem is one item of a list.
	kindItem = "item"
	// kindNote is a footnote or endnote.
	kindNote = "note"
	// kindTitle is the title of the book, as its title page prints it.
	kindTitle = "title"
	// kindSubtitle is the subtitle of the book.
	kindSubtitle = "subtitle"
	// kindAuthor is the name of an author of the book.
	kindAuthor = "author"
	// kindDetail is a line of the title page about the author or
	// the making of the book: an office the author holds, a
	// translator, an editor, an edition.
	kindDetail = "detail"
	// kindEpigraph is a quotation the book opens with, or its source.
	kindEpigraph = "epigraph"
	// kindImprint is a line of the title page about its publishing:
	// the publisher, the place, the year.
	kindImprint = "imprint"
)

// Limits of the levels of blocks.
const (
	// maxLevel is the deepest level a heading has.
	maxLevel = 3
	// maxDepth is the deepest an item of a list is nested.
	maxDepth = 4
)

// titleKinds holds the kinds of blocks the title page is made from, in
// the order they stand on it.
var titleKinds = []string{kindTitle, kindSubtitle, kindAuthor, kindDetail, kindEpigraph, kindImprint}

// bolded matches the marks of bold text.
var bolded = regexp.MustCompile(`\*{2,3}`)

// Block is one piece of a transcribed page.
type Block struct {
	// Kind is what the block is.
	Kind string `json:"kind" jsonschema:"what the block is: heading, paragraph, quote, item (one item of a list), note (a footnote or endnote), or, for what the title page of the book says, title, subtitle, author, detail, epigraph or imprint"`
	// Level is the level of a heading, or how deep an item is nested.
	Level int `json:"level,omitempty" jsonschema:"for a heading: 1 for a part or chapter, 2 for a section, 3 for a subsection. For an item: 1 for an item of the list itself, 2 for an item of a list within that item, and so on"`
	// Text is the text of the block.
	Text string `json:"text" jsonschema:"the text exactly as printed, with *italics* and **bold** marked so, and a reference to a note written [^marker]"`
	// Marker is what a note is referred to by.
	Marker string `json:"marker,omitempty" jsonschema:"for a note: the number or sign it is referred to by, such as 1 or *"`
	// Continues reports whether a paragraph carries on from the page
	// before.
	Continues bool `json:"continues,omitempty" jsonschema:"true when the block carries on a paragraph that began on the page before"`
}

// Doubt is a passage the model could not read with certainty.
type Doubt struct {
	// Page is the number of the page, counted from one.
	Page int `json:"page"`
	// Text is the passage as it was transcribed.
	Text string `json:"text" jsonschema:"the doubtful words as transcribed"`
	// Reason says why the reading is uncertain.
	Reason string `json:"reason" jsonschema:"why they could not be read with certainty"`
}

// verdict is what the model made of the sample.
type verdict struct {
	// good reports whether the model expects to restore the book well.
	good bool
	// reason says why.
	reason string
}

// element is a block of the whole book.
type element struct {
	Block
	// page is the page the block starts on.
	page int
	// id identifies a heading or a note within the book.
	id string
}

// draft is the book as transcribed so far.
type draft struct {
	// mu guards the fields below.
	mu sync.Mutex
	// pages holds the blocks of every page saved, by page number.
	pages map[int][]Block
	// doubts holds the passages that could not be read with certainty.
	doubts []Doubt
	// verdict is what the model made of the sample, once it has said.
	verdict *verdict
	// elements holds the blocks of the whole book once the pages are
	// put together.
	elements []element
	// changed counts the headings the model has reworded or moved to
	// another level.
	changed int
}

// newDraft returns an empty draft.
func newDraft() *draft {
	return &draft{pages: map[int][]Block{}}
}

// check reports what is wrong with a block, or nil.
func (b Block) check() error {
	if strings.TrimSpace(b.Text) == "" {
		return fmt.Errorf("a %s has no text", b.Kind)
	}
	switch b.Kind {
	case kindHeading:
		if b.Level < 1 || b.Level > maxLevel {
			return fmt.Errorf("the heading %q needs a level from 1 to %d", b.Text, maxLevel)
		}
	case kindItem:
		if b.Level < 0 || b.Level > maxDepth {
			return fmt.Errorf("the item %q is nested deeper than %d", clip(b.Text), maxDepth)
		}
	case kindNote:
		if strings.TrimSpace(b.Marker) == "" {
			return fmt.Errorf("the note %q needs the marker it is referred to by", clip(b.Text))
		}
	case kindParagraph, kindQuote, kindTitle, kindSubtitle, kindAuthor, kindDetail, kindEpigraph, kindImprint:
	default:
		return fmt.Errorf("%q is not a kind of block", b.Kind)
	}
	return nil
}

// clip shortens text for use in a message.
func clip(text string) string {
	if r := []rune(text); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return text
}

// save keeps the transcription of a page, replacing an earlier one.
func (d *draft) save(page int, blocks []Block, doubts []Doubt) error {
	for i := range blocks {
		if err := blocks[i].check(); err != nil {
			return err
		}
		blocks[i].Text = strings.Join(strings.Fields(blocks[i].Text), " ")
		blocks[i].Marker = strings.TrimSpace(blocks[i].Marker)
		switch blocks[i].Kind {
		case kindHeading, kindTitle, kindSubtitle, kindAuthor:
			// These are set apart by what they are, not by being bold.
			blocks[i].Text = strings.TrimSpace(bolded.ReplaceAllString(blocks[i].Text, ""))
		case kindItem:
			blocks[i].Level = max(blocks[i].Level, 1)
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pages[page] = blocks
	d.doubts = append(deleteDoubts(d.doubts, page), doubts...)
	return nil
}

// deleteDoubts returns doubts without those of the given page.
func deleteDoubts(doubts []Doubt, page int) []Doubt {
	out := doubts[:0]
	for _, doubt := range doubts {
		if doubt.Page != page {
			out = append(out, doubt)
		}
	}
	return out
}

// missing returns the pages from first to last that are not saved yet.
func (d *draft) missing(first, last int) []int {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []int
	for page := first; page <= last; page++ {
		if _, ok := d.pages[page]; !ok {
			out = append(out, page)
		}
	}
	return out
}

// saved returns how many pages are saved.
func (d *draft) saved() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pages)
}

// judge records what the model made of the sample.
func (d *draft) judge(good bool, reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.verdict = &verdict{good: good, reason: strings.TrimSpace(reason)}
}

// judged returns what the model made of the sample, or nil.
func (d *draft) judged() *verdict {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.verdict
}

// assemble puts the saved pages together into the blocks of the whole
// book: a paragraph that carries on from the page before is joined to
// it, and headings and notes get their identifiers.
func (d *draft) assemble() {
	d.mu.Lock()
	defer d.mu.Unlock()
	numbers := make([]int, 0, len(d.pages))
	for page := range d.pages {
		numbers = append(numbers, page)
	}
	sort.Ints(numbers)
	d.elements = nil
	headings, notes, lines := 0, 0, 0
	for _, page := range numbers {
		for _, b := range d.pages[page] {
			if b.Continues && d.join(b) {
				continue
			}
			e := element{Block: b, page: page}
			switch b.Kind {
			case kindHeading:
				headings++
				e.id = fmt.Sprintf("h%d", headings)
			case kindNote:
				notes++
				e.id = fmt.Sprintf("n%d", notes)
			case kindTitle, kindSubtitle, kindAuthor, kindDetail, kindEpigraph, kindImprint:
				lines++
				e.id = fmt.Sprintf("t%d", lines)
			}
			d.elements = append(d.elements, e)
		}
	}
	sort.SliceStable(d.doubts, func(i, j int) bool { return d.doubts[i].Page < d.doubts[j].Page })
}

// join adds b to the last block of running text of the same kind, past
// the notes at the foot of its page, and reports whether there was one.
// A word broken across the two pages is put back together.
func (d *draft) join(b Block) bool {
	for i := len(d.elements) - 1; i >= 0; i-- {
		prev := &d.elements[i]
		if prev.Kind == kindNote {
			continue
		}
		if prev.Kind != b.Kind || b.Kind == kindHeading {
			return false
		}
		first, _ := firstRune(b.Text)
		if broken(prev.Text) && unicode.IsLower(first) {
			prev.Text = strings.TrimSuffix(prev.Text, "-") + b.Text
		} else {
			prev.Text += " " + b.Text
		}
		return true
	}
	return false
}

// firstRune returns the first character of text.
func firstRune(text string) (rune, bool) {
	for _, r := range text {
		return r, true
	}
	return 0, false
}

// broken reports whether text ends in a word cut off with a hyphen.
func broken(text string) bool {
	r := []rune(text)
	return len(r) >= 2 && r[len(r)-1] == '-' && unicode.IsLetter(r[len(r)-2])
}

// opened is a heading with what it opens.
type opened struct {
	element
	// opens is how the text under the heading begins.
	opens string
	// length is how many characters of text stand under the heading,
	// up to the next one.
	length int
}

// headings returns the headings of the book in reading order, each
// with what it opens.
func (d *draft) headings() []opened {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []opened
	for _, e := range d.elements {
		switch {
		case e.Kind == kindHeading:
			out = append(out, opened{element: e})
		case len(out) > 0 && e.Kind != kindNote && !slices.Contains(titleKinds, e.Kind):
			last := &out[len(out)-1]
			if last.opens == "" {
				last.opens = clipTo(e.Text, 160)
			}
			last.length += len([]rune(e.Text))
		}
	}
	return out
}

// clipTo shortens text to at most n characters.
func clipTo(text string, n int) string {
	if r := []rune(text); len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}

// unaccented maps the vowels that carry an accent to the bare vowels.
// Capitals are often printed without their accents, so a word taken out
// of capitals may gain one and still be the same word.
var unaccented = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ä': 'a', 'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i', 'ó': 'o', 'ò': 'o', 'ô': 'o', 'ö': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u',
}

// letters returns the letters and digits of text in lower case and
// without the accents of its vowels, which is what two wordings have to
// share to say the same.
func letters(text string) string {
	var sb strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			r = unicode.ToLower(r)
			if bare, ok := unaccented[r]; ok {
				r = bare
			}
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// shouted matches a stretch of two or more words set in capitals.
var shouted = regexp.MustCompile(`\p{Lu}{2,}(?:[^\p{L}\p{N}*]{1,3}\p{Lu}+)+`)

// trailing matches the last word of a stretch with what parts it from
// the word before.
var trailing = regexp.MustCompile(`[^\p{L}\p{N}*]+\p{Lu}+$`)

// shouts returns the stretches of two or more words set in capitals in
// text. The capital that opens the word after a stretch is not part of
// it.
func shouts(text string) []string {
	var out []string
	for _, at := range shouted.FindAllStringIndex(text, -1) {
		found := text[at[0]:at[1]]
		if next, _ := utf8.DecodeRuneInString(text[at[1]:]); unicode.IsLetter(next) {
			found = trailing.ReplaceAllString(found, "")
		}
		if shouted.FindString(found) == found {
			out = append(out, found)
		}
	}
	return out
}

// stretch is a piece of the text of the book whose capitals the model
// is asked about.
type stretch struct {
	// id identifies the stretch.
	id string
	// page is the page it is on.
	page int
	// text is the stretch itself.
	text string
	// within is the block it is part of, when it is not all of it.
	within string
	// index is the position of its block among the blocks of the book.
	index int
	// whole reports whether the stretch is all of its block.
	whole bool
}

// stretches returns what the capitals of are to be looked at: the lines
// of the title page, and the stretches of words set in capitals in the
// text. Headings are not among them; they are looked at by themselves.
func (d *draft) stretches() []stretch {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []stretch
	count := 0
	for i, e := range d.elements {
		switch e.Kind {
		case kindHeading:
		case kindTitle, kindSubtitle, kindAuthor, kindDetail, kindEpigraph, kindImprint:
			out = append(out, stretch{id: e.id, page: e.page, text: e.Text, index: i, whole: true})
		default:
			for _, found := range shouts(e.Text) {
				count++
				out = append(out, stretch{
					id: fmt.Sprintf("c%d", count), page: e.page, text: found, within: e.Text, index: i,
				})
			}
		}
	}
	return out
}

// recase gives a stretch the wording text, which may only differ from
// the one it has in capitals, accents, spacing, punctuation and
// emphasis.
func (d *draft) recase(s stretch, text string) error {
	text = strings.Join(strings.Fields(text), " ")
	if letters(text) != letters(s.text) {
		return fmt.Errorf("%s: %q does not have the words of %q; only capitals, accents and emphasis may change", s.id, text, s.text)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	e := &d.elements[s.index]
	if s.whole {
		e.Text = text
	} else {
		e.Text = strings.Replace(e.Text, s.text, text, 1)
	}
	return nil
}

// retitle gives the heading called id a new level and wording. A level
// of zero turns it into a paragraph. The wording may only differ from
// the one it has in capitals, spacing and punctuation.
func (d *draft) retitle(id string, level int, text string) error {
	text = strings.Join(strings.Fields(text), " ")
	if level < 0 || level > maxLevel {
		return fmt.Errorf("%s: the level has to be from 0 to %d", id, maxLevel)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.elements {
		e := &d.elements[i]
		if e.id != id || e.Kind != kindHeading {
			continue
		}
		if letters(text) != letters(e.Text) {
			return fmt.Errorf("%s: %q does not have the words of %q; only capitals, accents, spacing and punctuation may change", id, text, e.Text)
		}
		if e.Text != text || e.Level != level {
			d.changed++
		}
		e.Text, e.Level = text, level
		if level == 0 {
			e.Kind = kindParagraph
		}
		return nil
	}
	return fmt.Errorf("%s: there is no such heading", id)
}
