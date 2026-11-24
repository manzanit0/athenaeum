package api

import (
	"net/http"

	"github.com/manzanit0/athenaeum/internal/covers"
)

// finder returns what searches the internet for covers.
func (s *Server) finder() *covers.Finder {
	s.coversOnce.Do(func() {
		if s.Covers == nil {
			s.Covers = covers.New()
		}
	})
	return s.Covers
}

// findCovers searches the internet for covers of a book. The search is
// the one in the query: title, author and isbn, which the interface
// starts from the details of the book and lets the reader change. The
// answer lists the images found; getFoundCover serves them.
func (s *Server) findCovers(w http.ResponseWriter, r *http.Request) {
	book, err := s.book(r)
	if err != nil {
		fail(w, err)
		return
	}
	query := r.URL.Query()
	q := covers.Query{Title: book.Title, Authors: book.Authors, ISBN: book.ISBN}
	if query.Has("title") || query.Has("author") || query.Has("isbn") {
		q = covers.Query{Title: query.Get("title"), Authors: query["author"], ISBN: query.Get("isbn")}
	}
	result, err := s.finder().Search(r.Context(), q)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// getFoundCover serves an image that a search found.
func (s *Server) getFoundCover(w http.ResponseWriter, r *http.Request) {
	c, err := s.finder().Candidate(r.PathValue("cid"))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(c.Data))
	w.Header().Set("Cache-Control", "private, max-age=900")
	_, _ = w.Write(c.Data)
}

// useFoundCover makes an image that a search found the cover of a book.
// The reader names the image; the server never fetches an address the
// reader gives.
func (s *Server) useFoundCover(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, err)
		return
	}
	c, err := s.finder().Candidate(body.ID)
	if err != nil {
		fail(w, err)
		return
	}
	book, err := s.Library.SetCover(id, c.Data)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, book)
}
