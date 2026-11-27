// Package covers finds cover images for books on the internet.
//
// The approach is the one calibre takes: every source is asked at the
// same time, what each one answers is checked to be a usable image, and
// the images are ranked so that the best come first. Unlike calibre it
// never picks one on its own; the candidates go to the person editing
// the book, who chooses.
package covers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"  // registers the GIF decoder
	_ "image/jpeg" // registers the JPEG decoder
	_ "image/png"  // registers the PNG decoder
	"net/url"
	"sort"
	"sync"
	"time"

	_ "golang.org/x/image/webp" // registers the WebP decoder
)

// ErrNotFound is returned when a candidate is not, or no longer, held.
var ErrNotFound = errors.New("that cover is no longer available, search again")

// ErrNoQuery is returned when a search has neither a title nor an ISBN.
var ErrNoQuery = errors.New("give a title or an ISBN to search for")

const (
	// minSide is the size below which an image is no cover: smaller
	// images are icons and tracking pixels, not pictures of a book.
	minSide = 50
	// maxCandidates bounds how many candidates a search keeps.
	maxCandidates = 12
	// maxHeld bounds how many candidates are kept for choosing.
	maxHeld = 120
	// holdFor is how long a candidate can be chosen after it was found.
	holdFor = 15 * time.Minute
	// defaultTimeout bounds how long a search waits for its sources.
	defaultTimeout = 15 * time.Second
)

// Query says which book to find covers for.
type Query struct {
	// Title is the book title.
	Title string
	// Authors lists the book authors.
	Authors []string
	// ISBN is the International Standard Book Number, in any notation.
	ISBN string
}

// Source finds cover images on one website.
type Source interface {
	// Name says where the images come from, in words for a person.
	Name() string
	// Priority orders the sources: the images of a source with a lower
	// number are listed before the others. Equal priorities are ordered
	// by image size.
	Priority() int
	// Find returns the encoded images the source holds for the book. It
	// returns nothing, and no error, when the source has none.
	Find(ctx context.Context, q Query) ([][]byte, error)
}

// Candidate is a cover image that may be chosen for a book.
type Candidate struct {
	// ID identifies the image: the same image has the same ID.
	ID string `json:"id"`
	// Source is the name of the website the image came from.
	Source string `json:"source"`
	// Width is the width of the image in pixels.
	Width int `json:"width"`
	// Height is the height of the image in pixels.
	Height int `json:"height"`
	// Data is the encoded image.
	Data []byte `json:"-"`

	priority int
}

// Result is the outcome of a search.
type Result struct {
	// Candidates lists the images found, best first.
	Candidates []Candidate `json:"candidates"`
	// Failed lists the sources that could not be asked and why, so that a
	// search that found nothing can say it may have missed something.
	Failed []Failure `json:"failed"`
}

// Failure is a source that could not be asked.
type Failure struct {
	// Source is the name of the source.
	Source string `json:"source"`
	// Reason says what went wrong, in words for a person.
	Reason string `json:"reason"`
}

// reasonOf says why a source failed, in words for a person.
func reasonOf(err error) string {
	var urlErr *url.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "it took too long to answer"
	case errors.As(err, &urlErr):
		return urlErr.Err.Error()
	}
	return err.Error()
}

// Finder searches several sources and holds what it found for a while,
// so that a candidate seen in a list can be chosen afterwards.
type Finder struct {
	// Sources are asked in parallel.
	Sources []Source
	// Timeout bounds how long a search waits; zero means 15 seconds.
	Timeout time.Duration

	mu   sync.Mutex
	held map[string]held
}

// held is a candidate kept for choosing, and when it stops being kept.
type held struct {
	candidate Candidate
	until     time.Time
}

// New returns a Finder asking Open Library and Google Books.
func New() *Finder {
	return &Finder{Sources: []Source{NewOpenLibrary(), NewGoogleBooks()}}
}

// Search asks every source for covers of the book and returns the usable
// images they gave. A source that fails does not fail the search; it is
// reported in the result.
func (f *Finder) Search(ctx context.Context, q Query) (Result, error) {
	q = q.clean()
	if q.Title == "" && q.ISBN == "" {
		return Result{}, ErrNoQuery
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type answer struct {
		source Source
		images [][]byte
		err    error
	}
	answers := make(chan answer, len(f.Sources))
	for _, source := range f.Sources {
		go func() {
			images, err := source.Find(ctx, q)
			answers <- answer{source, images, err}
		}()
	}

	result := Result{Candidates: []Candidate{}, Failed: []Failure{}}
	seen := map[string]bool{}
	for range f.Sources {
		a := <-answers
		if a.err != nil {
			result.Failed = append(result.Failed, Failure{a.source.Name(), reasonOf(a.err)})
		}
		for _, data := range a.images {
			c, ok := candidateOf(a.source, data)
			if !ok || seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			result.Candidates = append(result.Candidates, c)
		}
	}
	sort.Slice(result.Failed, func(i, j int) bool { return result.Failed[i].Source < result.Failed[j].Source })
	sort.SliceStable(result.Candidates, func(i, j int) bool {
		a, b := result.Candidates[i], result.Candidates[j]
		if a.priority != b.priority {
			return a.priority < b.priority
		}
		if area(a) != area(b) {
			return area(a) > area(b)
		}
		return a.ID < b.ID
	})
	if len(result.Candidates) > maxCandidates {
		result.Candidates = result.Candidates[:maxCandidates]
	}
	f.hold(result.Candidates)
	return result, nil
}

// Candidate returns a candidate found by an earlier search.
func (f *Finder) Candidate(id string) (Candidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.held[id]
	if !ok || time.Now().After(h.until) {
		delete(f.held, id)
		return Candidate{}, ErrNotFound
	}
	return h.candidate, nil
}

// hold keeps the candidates for choosing and lets old ones go.
func (f *Finder) hold(found []Candidate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = map[string]held{}
	}
	now := time.Now()
	for id, h := range f.held {
		if now.After(h.until) {
			delete(f.held, id)
		}
	}
	for _, c := range found {
		f.held[c.ID] = held{c, now.Add(holdFor)}
	}
	// The window is generous, so the bound only matters to a long
	// session: the oldest go first.
	for len(f.held) > maxHeld {
		oldest, first := "", true
		var at time.Time
		for id, h := range f.held {
			if first || h.until.Before(at) {
				oldest, at, first = id, h.until, false
			}
		}
		delete(f.held, oldest)
	}
}

// candidateOf checks that data is an image that can serve as a cover.
func candidateOf(source Source, data []byte) (Candidate, bool) {
	if !usable(data) {
		return Candidate{}, false
	}
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(data))
	sum := sha256.Sum256(data)
	return Candidate{
		ID:       hex.EncodeToString(sum[:8]),
		Source:   source.Name(),
		Width:    cfg.Width,
		Height:   cfg.Height,
		Data:     data,
		priority: source.Priority(),
	}, true
}

// placeholders holds the SHA-256 sums, in hex, of the images that
// websites send in place of a cover they do not have. They are real
// images of a size that passes for a cover, so they are known by their
// bytes.
var placeholders = map[string]bool{
	// "image not available" of Google Books, 575 by 750 pixels.
	"3efa8c43e5b4348f303a528c81adf435f0111ea752fe9f0f6241478b60987fa6": true,
}

// usable reports whether data is an image that can serve as a cover.
func usable(data []byte) bool {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < minSide || cfg.Height < minSide {
		return false
	}
	sum := sha256.Sum256(data)
	return !placeholders[hex.EncodeToString(sum[:])]
}

// area returns how many pixels the image of c has.
func area(c Candidate) int { return c.Width * c.Height }
