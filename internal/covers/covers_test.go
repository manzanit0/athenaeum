package covers_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/manzanit0/athenaeum/internal/booktest"
	"github.com/manzanit0/athenaeum/internal/covers"
)

// fake is a source that answers with what it was given.
type fake struct {
	name     string
	priority int
	images   [][]byte
	err      error
	// wait, when set, is waited for before answering.
	wait <-chan struct{}
}

func (f *fake) Name() string  { return f.name }
func (f *fake) Priority() int { return f.priority }
func (f *fake) Find(ctx context.Context, _ covers.Query) ([][]byte, error) {
	if f.wait != nil {
		select {
		case <-f.wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.images, f.err
}

// sized returns a PNG of the given size.
func sized(w, h int, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

var (
	red   = color.RGBA{R: 200, A: 255}
	green = color.RGBA{G: 200, A: 255}
	blue  = color.RGBA{B: 200, A: 255}
)

func TestSearchRanksBySourceThenSize(t *testing.T) {
	f := &covers.Finder{Sources: []covers.Source{
		&fake{name: "Poor", priority: 2, images: [][]byte{sized(600, 900, red)}},
		&fake{name: "Good", priority: 1, images: [][]byte{sized(100, 150, green), sized(200, 300, blue)}},
	}}
	got, err := f.Search(context.Background(), covers.Query{Title: "Dune"})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range got.Candidates {
		order = append(order, fmt.Sprintf("%s %dx%d", c.Source, c.Width, c.Height))
	}
	want := "Good 200x300,Good 100x150,Poor 600x900"
	if strings.Join(order, ",") != want {
		t.Errorf("order = %v, want %s", order, want)
	}
}

func TestSearchDropsUnusableAndDuplicateImages(t *testing.T) {
	same := sized(100, 150, red)
	f := &covers.Finder{Sources: []covers.Source{
		&fake{name: "A", priority: 1, images: [][]byte{same, sized(20, 20, green), []byte("not an image")}},
		&fake{name: "B", priority: 1, images: [][]byte{same}},
	}}
	got, err := f.Search(context.Background(), covers.Query{Title: "Dune"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("got %d candidates, want the one usable, distinct image", len(got.Candidates))
	}
}

func TestSearchReportsFailedSourcesAndKeepsTheRest(t *testing.T) {
	f := &covers.Finder{Sources: []covers.Source{
		&fake{name: "Down", err: errors.New("boom")},
		&fake{name: "Up", images: [][]byte{sized(100, 150, red)}},
	}}
	got, err := f.Search(context.Background(), covers.Query{Title: "Dune"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 1 || len(got.Failed) != 1 || got.Failed[0] != (covers.Failure{Source: "Down", Reason: "boom"}) {
		t.Errorf("got %d candidates, failed %v", len(got.Candidates), got.Failed)
	}
}

func TestSearchNeedsATitleOrAnISBN(t *testing.T) {
	f := &covers.Finder{Sources: []covers.Source{&fake{name: "A"}}}
	for _, q := range []covers.Query{{}, {ISBN: "123"}, {Authors: []string{"Herbert"}}} {
		if _, err := f.Search(context.Background(), q); !errors.Is(err, covers.ErrNoQuery) {
			t.Errorf("Search(%+v) error = %v, want ErrNoQuery", q, err)
		}
	}
	if _, err := f.Search(context.Background(), covers.Query{ISBN: "978-0-441-17271-9"}); err != nil {
		t.Errorf("a hyphenated ISBN alone should be enough: %v", err)
	}
}

func TestSearchStopsWaitingForASlowSource(t *testing.T) {
	never := make(chan struct{})
	f := &covers.Finder{
		Timeout: 50 * time.Millisecond,
		Sources: []covers.Source{
			&fake{name: "Slow", wait: never},
			&fake{name: "Quick", images: [][]byte{sized(100, 150, red)}},
		},
	}
	start := time.Now()
	got, err := f.Search(context.Background(), covers.Query{Title: "Dune"})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("the search waited for the slow source")
	}
	if len(got.Candidates) != 1 || len(got.Failed) != 1 || got.Failed[0].Source != "Slow" {
		t.Errorf("got %d candidates, failed %v", len(got.Candidates), got.Failed)
	}
	if got.Failed[0].Reason != "it took too long to answer" {
		t.Errorf("reason = %q", got.Failed[0].Reason)
	}
}

func TestFoundCandidatesCanBeChosenAfterwards(t *testing.T) {
	f := &covers.Finder{Sources: []covers.Source{&fake{name: "A", images: [][]byte{sized(100, 150, red)}}}}
	got, err := f.Search(context.Background(), covers.Query{Title: "Dune"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.Candidate(got.Candidates[0].ID)
	if err != nil || len(c.Data) == 0 {
		t.Fatalf("Candidate() = %v, %v", len(c.Data), err)
	}
	if _, err := f.Candidate("0000000000000000"); !errors.Is(err, covers.ErrNotFound) {
		t.Errorf("unknown candidate error = %v, want ErrNotFound", err)
	}
}

func TestOpenLibrary(t *testing.T) {
	isbnCover := booktest.PNG(red)
	editionCover := sized(100, 150, green)
	mux := http.NewServeMux()
	mux.HandleFunc("/b/isbn/9780441172719-L.jpg", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("default") != "false" {
			t.Error("the request did not ask for a 404 where there is no cover")
		}
		w.Write(isbnCover)
	})
	mux.HandleFunc("/b/id/777-L.jpg", func(w http.ResponseWriter, r *http.Request) { w.Write(editionCover) })
	mux.HandleFunc("/search.json", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("title") != "Dune" || q.Get("author") != "Frank Herbert" {
			t.Errorf("search = %v", q)
		}
		fmt.Fprint(w, `{"docs":[{"cover_i":777},{}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	o := covers.NewOpenLibrary()
	o.APIURL, o.CoversURL = srv.URL, srv.URL
	images, err := o.Find(context.Background(), covers.Query{Title: "Dune", Authors: []string{"Frank Herbert"}, ISBN: "9780441172719"})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || !bytes.Equal(images[0], isbnCover) || !bytes.Equal(images[1], editionCover) {
		t.Errorf("got %d images, the ISBN cover should come first and the edition second", len(images))
	}
}

func TestOpenLibraryWithNothingToFind(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	o := covers.NewOpenLibrary()
	o.APIURL, o.CoversURL = srv.URL, srv.URL
	images, err := o.Find(context.Background(), covers.Query{Title: "Nothing", ISBN: "9780441172719"})
	if err != nil || len(images) != 0 {
		t.Errorf("Find() = %d images, %v; a 404 is no error and no cover", len(images), err)
	}
}

func TestOpenLibraryFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	o := covers.NewOpenLibrary()
	o.APIURL, o.CoversURL = srv.URL, srv.URL
	if _, err := o.Find(context.Background(), covers.Query{Title: "Dune"}); err == nil {
		t.Error("a 503 should be reported as a failure")
	}
}

// atom is the answer of the feed of Google Books to a search.
func atom(thumbnails ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version='1.0' encoding='UTF-8'?><feed xmlns='http://www.w3.org/2005/Atom'>`)
	for _, t := range thumbnails {
		fmt.Fprintf(&b, `<entry><id>http://www.google.com/books/feeds/volumes/abc</id>`+
			`<link rel='http://schemas.google.com/books/2008/thumbnail' type='image/x-unknown' href='%s'/>`+
			`<link rel='alternate' href='http://books.google.com/books?id=abc'/></entry>`, strings.ReplaceAll(t, "&", "&amp;"))
	}
	b.WriteString(`</feed>`)
	return b.String()
}

func TestGoogleBooks(t *testing.T) {
	cover := sized(300, 450, blue)
	var zooms []string
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/books/feeds/volumes", func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("q"); q != "isbn:9780441172719" {
			t.Errorf("q = %q, an ISBN should be searched on its own", q)
		}
		fmt.Fprint(w, atom(srv.URL+"/books/content?id=abc&edge=curl&zoom=5"))
	})
	mux.HandleFunc("/books/content", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		zooms = append(zooms, q.Get("zoom"))
		if q.Has("edge") {
			t.Error("the curled corner should be asked away")
		}
		if q.Get("zoom") == "0" {
			// Where there is no large image the service sends a tiny
			// placeholder, not an error.
			w.Write(sized(1, 1, red))
			return
		}
		w.Write(cover)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()
	host, _ := url.Parse(srv.URL)

	g := covers.NewGoogleBooks()
	g.FeedURL, g.ImageHosts = srv.URL, []string{host.Hostname()}
	images, err := g.Find(context.Background(), covers.Query{Title: "Dune", ISBN: "9780441172719"})
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || !bytes.Equal(images[0], cover) {
		t.Fatalf("got %d images", len(images))
	}
	if strings.Join(zooms, ",") != "0,1" {
		t.Errorf("zooms tried = %v, want 0 then 1", zooms)
	}
}

func TestGoogleBooksSearchesTheTitleAndAuthor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("q"); q != "intitle:Dune inauthor:Frank Herbert" {
			t.Errorf("q = %q", q)
		}
		fmt.Fprint(w, atom())
	}))
	defer srv.Close()
	g := covers.NewGoogleBooks()
	g.FeedURL = srv.URL
	images, err := g.Find(context.Background(), covers.Query{Title: "Dune", Authors: []string{"Frank Herbert"}})
	if err != nil || len(images) != 0 {
		t.Errorf("Find() = %d images, %v", len(images), err)
	}
}

func TestGoogleBooksSaysWhyItFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "quota", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	g := covers.NewGoogleBooks()
	g.FeedURL = srv.URL
	_, err := g.Find(context.Background(), covers.Query{Title: "Dune"})
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %v, want one that names the 429 answer", err)
	}
}

func TestGoogleBooksDoesNotFollowLinksToOtherHosts(t *testing.T) {
	var hit bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit = true }))
	defer other.Close()
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, atom(other.URL+"/internal"))
	}))
	defer feed.Close()

	g := covers.NewGoogleBooks()
	g.FeedURL = feed.URL
	if _, err := g.Find(context.Background(), covers.Query{Title: "Dune"}); err == nil {
		t.Error("a link to a host that is not Google's should fail")
	}
	if hit {
		t.Error("the server was sent to a host that is not Google's")
	}
}
