package api_test

import (
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/store"
)

// titles returns the titles of the books a listing request answers with.
func (e *env) titles(query string) []string {
	e.t.Helper()
	var books []store.Book
	e.do("GET", "/api/books?"+query, nil, http.StatusOK, &books)
	titles := []string{}
	for _, b := range books {
		titles = append(titles, b.Title)
	}
	return titles
}

// counted returns the count given for value in counts, or zero.
func counted(counts []store.Count, value string) int {
	for _, c := range counts {
		if c.Value == value {
			return c.Count
		}
	}
	return 0
}

// TestBrowseByTag checks that a listing can be narrowed by tag, author,
// language and format, that the facets list every value of the library
// with what choosing it leads to and that tags can be changed on several
// books at once.
func TestBrowseByTag(t *testing.T) {
	e := newEnv(t)
	sower := e.importFile("sower.epub", booktest.EPUB(booktest.Book{
		Title: "Parable of the Sower", Author: "Octavia E. Butler", Language: "en-US", Tags: []string{"Science Fiction"},
	}))
	solaris := e.importFile("solaris.mobi", booktest.MOBI(booktest.Book{
		Title: "Solaris", Author: "Stanislaw Lem", Tags: []string{"science fiction", "Classics"},
	}))
	ficciones := e.importFile("ficciones.epub", booktest.EPUB(booktest.Book{
		Title: "Ficciones", Author: "Jorge Luis Borges", Language: "es",
	}))

	for query, want := range map[string][]string{
		"tag=Science+Fiction":                        {"Parable of the Sower", "Solaris"},
		"tag=classics":                               {"Solaris"},
		"tag=classics&untagged=true":                 {"Ficciones", "Solaris"},
		"untagged=true":                              {"Ficciones"},
		"language=en":                                {"Parable of the Sower"},
		"language=en&language=es":                    {"Ficciones", "Parable of the Sower"},
		"format=epub":                                {"Ficciones", "Parable of the Sower"},
		"format=epub&format=mobi":                    {"Ficciones", "Parable of the Sower", "Solaris"},
		"format=epub&language=es":                    {"Ficciones"},
		"tag=classics&format=epub":                   {},
		"author=" + url.QueryEscape("stanislaw lem"): {"Solaris"},
		"tag=Classics&q=sower":                       {},
	} {
		if got := e.titles(query); !slices.Equal(got, want) {
			t.Errorf("books?%s = %q, want %q", query, got, want)
		}
	}

	var changedDate store.Book
	e.do("PATCH", "/api/books/"+itoa(sower.ID), map[string]string{"published": "1993", "publisher": "Four Walls"}, http.StatusOK, nil)
	e.do("PATCH", "/api/books/"+itoa(solaris.ID), map[string]string{"published": "1961-06", "publisher": "MON"}, http.StatusOK, nil)
	e.do("PATCH", "/api/books/"+itoa(ficciones.ID), map[string]string{"published": "12/03/1944"}, http.StatusBadRequest, nil)
	e.do("PATCH", "/api/books/"+itoa(ficciones.ID), map[string]string{"published": "1944-2"}, http.StatusOK, &changedDate)
	if changedDate.Published != "1944-02" {
		t.Errorf("published = %q, want it written 1944-02", changedDate.Published)
	}
	e.do("PATCH", "/api/books/"+itoa(ficciones.ID), map[string]string{"published": ""}, http.StatusOK, nil)
	for query, want := range map[string][]string{
		"sort=published":            {"Solaris", "Parable of the Sower", "Ficciones"},
		"sort=published&order=desc": {"Parable of the Sower", "Solaris", "Ficciones"},
		"sort=publisher":            {"Parable of the Sower", "Solaris", "Ficciones"},
		"sort=title&order=desc":     {"Solaris", "Parable of the Sower", "Ficciones"},
		"sort=language":             {"Parable of the Sower", "Ficciones", "Solaris"},
	} {
		if got := e.titles(query); !slices.Equal(got, want) {
			t.Errorf("books?%s = %q, want %q", query, got, want)
		}
	}

	var facets store.Facets
	e.do("GET", "/api/books/facets?format=epub&tag=classics", nil, http.StatusOK, &facets)
	if facets.Total != 3 || facets.Matching != 0 || facets.Untagged != 1 {
		t.Errorf("facets = %+v, want none of three books and one EPUB without tags", facets)
	}
	wantTags := []store.Count{{Value: "Classics", Count: 0}, {Value: "Science Fiction", Count: 1}}
	if !slices.Equal(facets.Tags, wantTags) {
		t.Errorf("tags = %+v, want every tag of the library with its count among the EPUB books", facets.Tags)
	}
	wantFormats := []store.Count{{Value: "epub", Count: 0}, {Value: "mobi", Count: 1}}
	if !slices.Equal(facets.Formats, wantFormats) {
		t.Errorf("formats = %+v, want the formats of the books tagged Classics", facets.Formats)
	}
	if counted(facets.Languages, "en") != 0 || counted(facets.Languages, "es") != 0 || len(facets.Languages) != 2 {
		t.Errorf("languages = %+v, want en and es listed with no book", facets.Languages)
	}

	change := map[string]any{"ids": []int64{sower.ID, ficciones.ID}, "add": []string{"To Read", "science fiction"}}
	var changed []store.Book
	e.do("POST", "/api/books/tags", change, http.StatusOK, &changed)
	if len(changed) != 2 || !slices.Equal(changed[0].Tags, []string{"Science Fiction", "To Read"}) {
		t.Errorf("tagged books = %+v, want the new tag once and the old one kept as it was spelled", changed)
	}
	if got := e.titles("tag=To+Read"); !slices.Equal(got, []string{"Ficciones", "Parable of the Sower"}) {
		t.Errorf("books tagged To Read = %q", got)
	}
	e.do("POST", "/api/books/tags", map[string]any{"ids": []int64{solaris.ID, sower.ID}, "remove": []string{"SCIENCE FICTION"}},
		http.StatusOK, &changed)
	if got := e.titles("tag=Science+Fiction"); !slices.Equal(got, []string{"Ficciones"}) {
		t.Errorf("books tagged Science Fiction = %q, want only Ficciones", got)
	}
	e.checkCategories(sower.ID, solaris.ID, ficciones.ID)
	e.do("POST", "/api/books/tags", map[string]any{"ids": []int64{sower.ID}}, http.StatusBadRequest, nil)
	e.do("POST", "/api/books/tags", map[string]any{"ids": []int64{9999}, "add": []string{"x"}}, http.StatusNotFound, nil)
}

// checkCategories checks that categories form a tree: a book is filed
// under the most specific category only, a category leads to the books
// below it too, and renaming one reaches the books below it and merges
// it with a category of the new name.
func (e *env) checkCategories(sower, solaris, ficciones int64) {
	e.t.Helper()
	t := e.t
	for id, tags := range map[int64][]string{
		sower:     {"Fiction", "fiction/ Science Fiction ", "Essays"},
		solaris:   {"Fiction / Science Fiction", "Fiction / Classics"},
		ficciones: {"Fiction / Short Stories"},
	} {
		e.do("PATCH", "/api/books/"+itoa(id), map[string]any{"tags": tags}, http.StatusOK, nil)
	}
	var book store.Book
	e.do("GET", "/api/books/"+itoa(sower), nil, http.StatusOK, &book)
	if !slices.Equal(book.Tags, []string{"fiction / Science Fiction", "Essays"}) {
		t.Errorf("tags = %q, want the path tidied and the parent category dropped", book.Tags)
	}
	for query, want := range map[string][]string{
		"tag=Fiction":                         {"Ficciones", "Parable of the Sower", "Solaris"},
		"tag=Fiction+%2F+Science+Fiction":     {"Parable of the Sower", "Solaris"},
		"tag=Fiction+%2F+Classics&tag=essays": {"Parable of the Sower", "Solaris"},
		"tag=Fic":                             {},
	} {
		if got := e.titles(query); !slices.Equal(got, want) {
			t.Errorf("books?%s = %q, want %q", query, got, want)
		}
	}
	var facets store.Facets
	e.do("GET", "/api/books/facets", nil, http.StatusOK, &facets)
	want := []store.Count{
		{Value: "Essays", Count: 1}, {Value: "Fiction", Count: 3}, {Value: "Fiction / Classics", Count: 1},
		{Value: "fiction / Science Fiction", Count: 2}, {Value: "Fiction / Short Stories", Count: 1},
	}
	if !slices.Equal(facets.Tags, want) {
		t.Errorf("tags = %+v, want the parent listed with every book below it", facets.Tags)
	}

	var result struct {
		// Books is the number of books changed.
		Books int `json:"books"`
	}
	e.do("POST", "/api/books/tags/rename", map[string]string{"from": "fiction", "to": "Literature"}, http.StatusOK, &result)
	if got := e.titles("tag=Literature+%2F+Science+Fiction"); result.Books != 3 || len(got) != 2 {
		t.Errorf("renaming the parent changed %d books and left %q below it", result.Books, got)
	}
	e.do("POST", "/api/books/tags/rename", map[string]string{"from": "Literature / Classics", "to": "Literature / Science Fiction"},
		http.StatusOK, &result)
	e.do("GET", "/api/books/"+itoa(solaris), nil, http.StatusOK, &book)
	if result.Books != 1 || !slices.Equal(book.Tags, []string{"Literature / Science Fiction"}) {
		t.Errorf("merging changed %d books and left %q, want one category", result.Books, book.Tags)
	}
	e.do("POST", "/api/books/tags/rename", map[string]string{"from": "Essays"}, http.StatusOK, &result)
	if got := e.titles("tag=Essays"); result.Books != 1 || len(got) != 0 {
		t.Errorf("removing changed %d books and left %q", result.Books, got)
	}
	e.do("POST", "/api/books/tags/rename", map[string]string{"to": "x"}, http.StatusBadRequest, nil)
}
