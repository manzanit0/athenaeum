package covers

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// maxImageBytes bounds how large an image a source may send.
	maxImageBytes = 10 << 20
	// maxJSONBytes bounds how large a search answer may be.
	maxJSONBytes = 2 << 20
	// perSource is how many images one search takes from a source.
	perSource = 4
	// userAgent names the application to the websites it asks.
	userAgent = "athenaeum (self-hosted ebook library)"
)

// clean returns q without the notation that gets in the way of searching.
func (q Query) clean() Query {
	q.Title = strings.TrimSpace(q.Title)
	q.ISBN = cleanISBN(q.ISBN)
	var authors []string
	for _, a := range q.Authors {
		if a = strings.TrimSpace(a); a != "" {
			authors = append(authors, a)
		}
	}
	q.Authors = authors
	return q
}

// author returns the first author, who is the one a search is for.
func (q Query) author() string {
	if len(q.Authors) == 0 {
		return ""
	}
	return q.Authors[0]
}

// cleanISBN returns the digits, and the X of the check digit, of an ISBN
// written with spaces, hyphens or a prefix, and nothing if that is not
// the length of an ISBN.
func cleanISBN(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= '0' && r <= '9') || r == 'X' {
			b.WriteRune(r)
		}
	}
	if n := b.Len(); n != 10 && n != 13 {
		return ""
	}
	return b.String()
}

// httpSource is what the sources share: a client and the way to ask it.
type httpSource struct {
	client *http.Client
}

func newHTTPSource() httpSource {
	return httpSource{client: &http.Client{Timeout: 20 * time.Second}}
}

// get returns the body of the response to a GET request. A 404 answer is
// no error: it says the website has nothing, and returns nil.
func (h httpSource) get(ctx context.Context, address string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, nil
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%s answered %s", req.URL.Host, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s sent more than %d bytes", req.URL.Host, limit)
	}
	return data, nil
}

// getJSON decodes the JSON answer to a GET request into v.
func (h httpSource) getJSON(ctx context.Context, address string, v any) error {
	data, err := h.get(ctx, address, maxJSONBytes)
	if err != nil || data == nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// OpenLibrary finds covers on openlibrary.org, which holds them for
// books by ISBN and by the number of an edition.
type OpenLibrary struct {
	httpSource
	// APIURL is the address of the search API.
	APIURL string
	// CoversURL is the address of the cover images.
	CoversURL string
}

// NewOpenLibrary returns the source for the real Open Library.
func NewOpenLibrary() *OpenLibrary {
	return &OpenLibrary{
		httpSource: newHTTPSource(),
		APIURL:     "https://openlibrary.org",
		CoversURL:  "https://covers.openlibrary.org",
	}
}

// Name implements Source.
func (*OpenLibrary) Name() string { return "Open Library" }

// Priority implements Source.
func (*OpenLibrary) Priority() int { return 1 }

// Find implements Source. The cover of the ISBN comes first, then the
// covers of the editions that a search for the title and author finds.
func (o *OpenLibrary) Find(ctx context.Context, q Query) ([][]byte, error) {
	var images [][]byte
	var firstErr error
	note := func(data []byte, err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if data != nil {
			images = append(images, data)
		}
	}
	if q.ISBN != "" {
		note(o.get(ctx, fmt.Sprintf("%s/b/isbn/%s-L.jpg?default=false", o.CoversURL, q.ISBN), maxImageBytes))
	}
	if q.Title != "" {
		ids, err := o.search(ctx, q)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, id := range ids {
			note(o.get(ctx, fmt.Sprintf("%s/b/id/%d-L.jpg?default=false", o.CoversURL, id), maxImageBytes))
		}
	}
	if len(images) > 0 {
		// Something was found, so a failure of the rest is not worth
		// reporting.
		return images, nil
	}
	return nil, firstErr
}

// search returns the cover numbers of the works matching the title and
// author.
func (o *OpenLibrary) search(ctx context.Context, q Query) ([]int, error) {
	params := url.Values{"title": {q.Title}, "limit": {fmt.Sprint(perSource)}, "fields": {"cover_i"}}
	if a := q.author(); a != "" {
		params.Set("author", a)
	}
	var answer struct {
		Docs []struct {
			CoverID int `json:"cover_i"`
		} `json:"docs"`
	}
	if err := o.getJSON(ctx, o.APIURL+"/search.json?"+params.Encode(), &answer); err != nil {
		return nil, err
	}
	var ids []int
	for _, d := range answer.Docs {
		if d.CoverID > 0 {
			ids = append(ids, d.CoverID)
		}
	}
	return ids, nil
}

// GoogleBooks finds covers through the Atom feed of Google Books, which
// is what calibre asks. The JSON API answers anonymous requests from a
// quota shared by everyone who asks without a key, which runs out, and
// the feed needs no key.
type GoogleBooks struct {
	httpSource
	// FeedURL is the address of the website that serves the feed.
	FeedURL string
	// ImageHosts are the host names, or domains starting with a dot,
	// that image links may point to. The feed is not trusted to send the
	// server wherever it likes.
	ImageHosts []string
}

// NewGoogleBooks returns the source for the real Google Books.
func NewGoogleBooks() *GoogleBooks {
	return &GoogleBooks{
		httpSource: newHTTPSource(),
		FeedURL:    "https://www.google.com",
		ImageHosts: []string{".google.com", ".googleusercontent.com"},
	}
}

// Name implements Source.
func (*GoogleBooks) Name() string { return "Google Books" }

// Priority implements Source. Its covers are large but often poor, scans
// with their errors among them, so a better looking cover from anywhere
// else is listed before them.
func (*GoogleBooks) Priority() int { return 2 }

// feed is the part of the Atom feed of Google Books that is read.
type feed struct {
	Entries []struct {
		Links []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

// Find implements Source.
func (g *GoogleBooks) Find(ctx context.Context, q Query) ([][]byte, error) {
	terms := "intitle:" + q.Title
	if a := q.author(); a != "" && q.Title != "" {
		terms += " inauthor:" + a
	}
	if q.ISBN != "" {
		terms = "isbn:" + q.ISBN
	}
	params := url.Values{"q": {terms}, "max-results": {fmt.Sprint(perSource)}}
	raw, err := g.get(ctx, g.FeedURL+"/books/feeds/volumes?"+params.Encode(), maxJSONBytes)
	if err != nil || raw == nil {
		return nil, err
	}
	var answer feed
	if err := xml.Unmarshal(raw, &answer); err != nil {
		return nil, fmt.Errorf("the answer of Google Books could not be read: %w", err)
	}
	var images [][]byte
	var firstErr error
	for _, entry := range answer.Entries {
		for _, link := range entry.Links {
			if !strings.HasSuffix(link.Rel, "/thumbnail") || link.Href == "" {
				continue
			}
			data, err := g.image(ctx, link.Href)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			if data != nil {
				images = append(images, data)
			}
		}
	}
	if len(images) > 0 {
		return images, nil
	}
	return nil, firstErr
}

// image downloads a cover from the link the feed gave. The link is for a
// thumbnail; zoom=0 asks for the largest image the service holds, and
// zoom=1 for the next size down if there is none, as calibre does.
// Where there is no cover the service answers with a tiny placeholder,
// which is why what comes back is looked at before it is taken.
func (g *GoogleBooks) image(ctx context.Context, link string) ([]byte, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	if !g.trusts(u.Hostname()) {
		return nil, fmt.Errorf("image link to %s was not followed", u.Hostname())
	}
	if u.Scheme == "http" && strings.HasSuffix(u.Hostname(), ".google.com") {
		u.Scheme = "https" // the feed gives plain HTTP links to its images
	}
	var firstErr error
	for _, zoom := range []string{"0", "1"} {
		params := u.Query()
		params.Set("zoom", zoom)
		params.Del("edge") // the curled page corner
		u.RawQuery = params.Encode()
		data, err := g.get(ctx, u.String(), maxImageBytes)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if err == nil && usable(data) {
			return data, nil
		}
	}
	return nil, firstErr
}

// trusts reports whether image links may point to host.
func (g *GoogleBooks) trusts(host string) bool {
	for _, h := range g.ImageHosts {
		if host == h || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h)) {
			return true
		}
	}
	return false
}
