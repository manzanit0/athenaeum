package api

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/store"
)

// listOptions reads what a listing of books is searched, sorted and
// narrowed by from the q, sort, order, tag, untagged, author, language
// and format parameters of r. tag, language and format may be given
// several times, and a book then needs only one of the values.
func listOptions(r *http.Request) store.ListOptions {
	q := r.URL.Query()
	return store.ListOptions{
		Query: q.Get("q"),
		Sort:  q.Get("sort"),
		Desc:  q.Get("order") == "desc",
		Filter: store.Filter{
			Tags:      meta.CleanList(q["tag"]),
			Untagged:  q.Get("untagged") == "true",
			Author:    strings.TrimSpace(q.Get("author")),
			Languages: meta.CleanList(q["language"]),
			Formats:   meta.CleanList(q["format"]),
		},
	}
}

// facets returns how the books matching the parameters of the request
// spread over the values they can be narrowed by.
func (s *Server) facets(w http.ResponseWriter, r *http.Request) {
	facets, err := s.Store.Facets(listOptions(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, facets)
}

// tagRequest asks for tags to be put on, or taken off, several books.
type tagRequest struct {
	// IDs lists the books to change.
	IDs []int64 `json:"ids"`
	// Add lists the tags to put on the books.
	Add []string `json:"add"`
	// Remove lists the tags to take off the books.
	Remove []string `json:"remove"`
}

// retag returns tags without those in remove and with those in add,
// comparing tags without regard to case.
func retag(tags, add, remove []string) []string {
	same := func(a string) func(string) bool {
		return func(b string) bool { return strings.EqualFold(a, b) }
	}
	out := slices.DeleteFunc(slices.Clone(tags), func(tag string) bool {
		return slices.ContainsFunc(remove, same(tag))
	})
	for _, tag := range add {
		if !slices.ContainsFunc(out, same(tag)) {
			out = append(out, tag)
		}
	}
	return out
}

// tagBooks changes the tags of several books at once and returns the
// books as they are afterwards.
func (s *Server) tagBooks(w http.ResponseWriter, r *http.Request) {
	var req tagRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	req.Add, req.Remove = meta.CleanList(req.Add), meta.CleanList(req.Remove)
	if len(req.IDs) == 0 || len(req.Add)+len(req.Remove) == 0 {
		fail(w, errors.Join(errBadRequest, errors.New("name the books and at least one tag to add or remove")))
		return
	}
	books := make([]*store.Book, 0, len(req.IDs))
	for _, id := range req.IDs {
		book, err := s.Store.Book(id)
		if err != nil {
			fail(w, err)
			return
		}
		m := book.Metadata
		m.Tags = retag(m.Tags, req.Add, req.Remove)
		if !slices.Equal(m.Tags, book.Tags) {
			if book, err = s.Library.UpdateMetadata(id, m); err != nil {
				fail(w, err)
				return
			}
		}
		books = append(books, book)
	}
	writeJSON(w, http.StatusOK, books)
}

// renameRequest asks for a category to be renamed, merged into another
// or removed.
type renameRequest struct {
	// From is the category to change, with every category below it.
	From string `json:"from"`
	// To is its new name. Naming an existing category merges the two,
	// and an empty name takes the books out of the category.
	To string `json:"to"`
}

// renameResult says how far a change of category reached.
type renameResult struct {
	// Books is the number of books that were changed.
	Books int `json:"books"`
}

// renamed returns tags with the category from, and those below it,
// moved to the category to, or left out when to is empty.
func renamed(tags []string, from, to string) []string {
	out := []string{}
	for _, tag := range tags {
		switch {
		case !store.Under(tag, from) || len(tag) < len(from):
			out = append(out, tag)
		case to != "":
			out = append(out, to+tag[len(from):])
		}
	}
	return out
}

// renameTag renames, merges or removes a category on every book that is
// in it or below it.
func (s *Server) renameTag(w http.ResponseWriter, r *http.Request) {
	var req renameRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	from, to := store.Category(req.From), store.Category(req.To)
	if from == "" {
		fail(w, errors.Join(errBadRequest, errors.New("name the category to change")))
		return
	}
	books, err := s.Store.Books(store.ListOptions{Filter: store.Filter{Tags: []string{from}}})
	if err != nil {
		fail(w, err)
		return
	}
	for _, book := range books {
		m := book.Metadata
		m.Tags = renamed(book.Tags, from, to)
		if _, err := s.Library.UpdateMetadata(book.ID, m); err != nil {
			fail(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, renameResult{Books: len(books)})
}
