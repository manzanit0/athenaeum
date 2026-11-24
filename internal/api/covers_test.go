package api_test

import (
	"bytes"
	"context"
	"image/color"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/covers"
	"github.com/manzanit0/athenaeum/internal/store"
)

var (
	// coverFound and coverOther are the images the stand-in finds.
	coverFound = booktest.PNG(color.RGBA{R: 255, A: 255})
	coverOther = booktest.PNG(color.RGBA{B: 255, G: 90, A: 255})
)

// coverStub is a source that answers every search with the same images
// and remembers what it was asked.
type coverStub struct {
	images [][]byte

	mu      sync.Mutex
	queries []covers.Query
}

func (c *coverStub) Name() string  { return "Stub Library" }
func (c *coverStub) Priority() int { return 1 }
func (c *coverStub) Find(_ context.Context, q covers.Query) ([][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries = append(c.queries, q)
	return c.images, nil
}

// last returns the latest search the stand-in was asked for.
func (c *coverStub) last() covers.Query {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queries[len(c.queries)-1]
}

// foundCovers is the answer to a cover search.
type foundCovers struct {
	Candidates []covers.Candidate `json:"candidates"`
	Failed     []covers.Failure   `json:"failed"`
}

func TestFindAndUseCover(t *testing.T) {
	e := newEnv(t)
	book := e.importSample()
	path := "/api/books/" + itoa(book.ID)

	// Without a query the search is for the book as it is filed.
	var found foundCovers
	e.do("GET", path+"/covers", nil, http.StatusOK, &found)
	if q := e.covers.last(); q.Title != sample.Title || len(q.Authors) != 1 || q.Authors[0] != sample.Author {
		t.Errorf("default search = %+v, want the title and author of the book", q)
	}
	if len(found.Candidates) != 2 || found.Candidates[0].Source != "Stub Library" || found.Candidates[0].Width != 60 {
		t.Fatalf("candidates = %+v", found.Candidates)
	}

	// The reader's own terms replace the book's, and the ISBN is tidied.
	e.do("GET", path+"/covers?title=Kindred&author=Octavia+Butler&isbn=0-8070-8369-6", nil, http.StatusOK, &found)
	if q := e.covers.last(); q.Title != "Kindred" || q.Authors[0] != "Octavia Butler" || q.ISBN != "0807083696" {
		t.Errorf("search = %+v", q)
	}
	e.do("GET", path+"/covers?title=&author=&isbn=", nil, http.StatusBadRequest, nil)
	e.do("GET", "/api/books/9999/covers", nil, http.StatusNotFound, nil)

	// An image that was found is served, and cached by the browser.
	pick := found.Candidates[0]
	resp, err := http.Get(e.url + "/api/covers/" + pick.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("Cache-Control") == "" {
		t.Errorf("found cover response = %d %v", resp.StatusCode, resp.Header)
	}
	if !bytes.Equal(data, coverFound) && !bytes.Equal(data, coverOther) {
		t.Error("the image served is not one that was found")
	}

	// Choosing it makes it the cover of the book.
	var updated store.Book
	e.do("POST", path+"/cover/found", map[string]string{"id": pick.ID}, http.StatusOK, &updated)
	if updated.Cover == "" {
		t.Error("the book has no cover after one was chosen")
	}
	resp, err = http.Get(e.url + path + "/cover")
	if err != nil {
		t.Fatal(err)
	}
	cover, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(cover, data) {
		t.Error("the cover of the book is not the image that was chosen")
	}

	// Only what a search found can be chosen: no address is fetched.
	e.do("POST", path+"/cover/found", map[string]string{"id": "https://example.com/x.png"}, http.StatusNotFound, nil)
	e.do("POST", path+"/cover/found", map[string]string{"id": "00ffffffffffffff"}, http.StatusNotFound, nil)
	e.do("POST", "/api/books/9999/cover/found", map[string]string{"id": pick.ID}, http.StatusNotFound, nil)
	e.do("GET", "/api/covers/00ffffffffffffff", nil, http.StatusNotFound, nil)
}
