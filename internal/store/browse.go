package store

import (
	"cmp"
	"slices"
	"strings"
)

// separator divides the levels of a category, as in "History / Military".
const separator = " / "

// Category returns tag written the way categories are: its levels
// trimmed and joined by the separator, with empty levels left out.
func Category(tag string) string {
	var levels []string
	for _, level := range strings.Split(tag, "/") {
		if level = strings.TrimSpace(level); level != "" {
			levels = append(levels, level)
		}
	}
	return strings.Join(levels, separator)
}

// Under reports whether tag is the category parent or one below it.
func Under(tag, parent string) bool {
	tag, parent = strings.ToLower(tag), strings.ToLower(parent)
	return tag == parent || strings.HasPrefix(tag, parent+separator)
}

// Categories returns tags written as categories, without blanks and
// repeats and without any category that another of them is below: a
// book filed under "History / Military" is not filed under "History"
// as well.
func Categories(tags []string) []string {
	all := []string{}
	for _, tag := range tags {
		if tag = Category(tag); tag != "" && !has(all, tag) {
			all = append(all, tag)
		}
	}
	return slices.DeleteFunc(slices.Clone(all), func(tag string) bool {
		return slices.ContainsFunc(all, func(other string) bool {
			return !strings.EqualFold(other, tag) && Under(other, tag)
		})
	})
}

// top returns the first level of the category tag.
func top(tag string) string {
	first, _, _ := strings.Cut(tag, separator)
	return first
}

// Filter narrows a listing to the books that have certain properties.
// Within tags, languages and formats a book needs only one of the values
// asked for; it must satisfy every kind that is asked for.
type Filter struct {
	// Tags lists categories of which a book must be in at least one, or
	// in one below it.
	Tags []string
	// Untagged also keeps the books that carry no tag.
	Untagged bool
	// Author is a name that must be among the authors of a book.
	Author string
	// Languages lists languages, as primary codes such as "en", of which
	// a book must be in one.
	Languages []string
	// Formats lists formats of which a book must have a file in one.
	Formats []string
}

// Count says how many books share a value.
type Count struct {
	// Value is what the books have in common.
	Value string `json:"value"`
	// Count is the number of books.
	Count int `json:"count"`
}

// Facets says how the books of a listing spread over the values the
// listing can be narrowed by. Every value found in the library is
// listed, in alphabetical order, so the lists stay the same while the
// listing is narrowed. The counts of one kind of value leave the
// narrowing by that kind out, since they say what choosing a value of
// that kind leads to.
type Facets struct {
	// Total is the number of books in the library.
	Total int `json:"total"`
	// Matching is the number of books in the listing.
	Matching int `json:"matching"`
	// Tags counts books by category. A category that has others below
	// it counts the books in any of those too, and is listed even when
	// no book is filed directly under it.
	Tags []Count `json:"tags"`
	// Untagged is the number of books without tags.
	Untagged int `json:"untagged"`
	// Languages counts books by language.
	Languages []Count `json:"languages"`
	// Formats counts books by format.
	Formats []Count `json:"formats"`
}

// LanguageKey returns the primary code of a language tag, so that
// "en-US" and "EN" both count as "en".
func LanguageKey(language string) string {
	primary, _, _ := strings.Cut(strings.ReplaceAll(strings.TrimSpace(language), "_", "-"), "-")
	return strings.ToLower(primary)
}

// has reports whether list holds value, ignoring case.
func has(list []string, value string) bool {
	return slices.ContainsFunc(list, func(entry string) bool { return strings.EqualFold(entry, value) })
}

// tagged reports whether b satisfies the part of f about tags.
func (f Filter) tagged(b *Book) bool {
	if len(f.Tags) == 0 && !f.Untagged {
		return true
	}
	if len(b.Tags) == 0 {
		return f.Untagged
	}
	return slices.ContainsFunc(f.Tags, func(tag string) bool {
		return slices.ContainsFunc(b.Tags, func(own string) bool { return Under(own, tag) })
	})
}

// written reports whether b satisfies the part of f about the author.
func (f Filter) written(b *Book) bool {
	return f.Author == "" || has(b.Authors, f.Author)
}

// spoken reports whether b satisfies the part of f about the language.
func (f Filter) spoken(b *Book) bool {
	return len(f.Languages) == 0 || slices.ContainsFunc(f.Languages, func(language string) bool {
		return LanguageKey(language) == LanguageKey(b.Language)
	})
}

// held reports whether b satisfies the part of f about the format.
func (f Filter) held(b *Book) bool {
	return len(f.Formats) == 0 || slices.ContainsFunc(f.Formats, func(format string) bool {
		_, ok := b.Format(strings.ToLower(format))
		return ok
	})
}

// Match reports whether b has every property f asks for.
func (f Filter) Match(b *Book) bool {
	return f.tagged(b) && f.written(b) && f.spoken(b) && f.held(b)
}

// tally counts values, remembering the first spelling of each.
type tally struct {
	// counts holds the count of every value by its lower case form.
	counts map[string]*Count
}

// add counts n more books for value.
func (t *tally) add(value string, n int) {
	if value == "" {
		return
	}
	if t.counts == nil {
		t.counts = map[string]*Count{}
	}
	key := strings.ToLower(value)
	if t.counts[key] == nil {
		t.counts[key] = &Count{Value: value}
	}
	t.counts[key].Count += n
}

// list returns the counts in alphabetical order of their values.
func (t *tally) list() []Count {
	out := make([]Count, 0, len(t.counts))
	for _, c := range t.counts {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b Count) int {
		return cmp.Compare(strings.ToLower(a.Value), strings.ToLower(b.Value))
	})
	return out
}

// facetTally counts books by tag, language and format.
type facetTally struct {
	// tags counts books by tag.
	tags tally
	// languages counts books by language.
	languages tally
	// formats counts books by format.
	formats tally
}

// add counts b under each of its tags, its language and each of its
// formats, by the amount given for that kind, which may be zero to list
// the values without counting the book.
func (t *facetTally) add(b *Book, tags, language, formats int) {
	var counted []string
	for _, tag := range b.Tags {
		for _, category := range []string{tag, top(tag)} {
			if !has(counted, category) {
				counted = append(counted, category)
				t.tags.add(category, tags)
			}
		}
	}
	t.languages.add(LanguageKey(b.Language), language)
	for _, file := range b.Formats {
		t.formats.add(file.Format, formats)
	}
}

// one returns 1 when every condition holds and 0 otherwise.
func one(conditions ...bool) int {
	if slices.Contains(conditions, false) {
		return 0
	}
	return 1
}

// Facets returns how the books matching opts spread over tags, languages
// and formats.
func (s *Store) Facets(opts ListOptions) (Facets, error) {
	var out Facets
	var counts facetTally
	library, err := s.Books(ListOptions{})
	if err != nil {
		return out, err
	}
	out.Total = len(library)
	for _, b := range library {
		counts.add(b, 0, 0, 0)
	}
	f := opts.Filter
	opts.Filter = Filter{}
	books, err := s.Books(opts)
	if err != nil {
		return out, err
	}
	for _, b := range books {
		written, tagged, spoken, held := f.written(b), f.tagged(b), f.spoken(b), f.held(b)
		counts.add(b, one(written, spoken, held), one(written, tagged, held), one(written, tagged, spoken))
		out.Untagged += one(written, spoken, held, len(b.Tags) == 0)
		out.Matching += one(written, tagged, spoken, held)
	}
	out.Tags, out.Languages, out.Formats = counts.tags.list(), counts.languages.list(), counts.formats.list()
	return out, nil
}
