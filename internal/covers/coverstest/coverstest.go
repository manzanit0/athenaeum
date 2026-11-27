// Package coverstest stands in for the websites that covers are found on,
// for the end-to-end tests.
package coverstest

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"

	"github.com/manzanit0/athenaeum/internal/covers"
)

// Finder returns a finder whose two sources answer the same way every
// time. A search for the title "Nothing" finds no covers, and one for
// "Unreachable" fails, as a website that cannot be reached does.
func Finder() *covers.Finder {
	return &covers.Finder{Sources: []covers.Source{
		&source{name: "Open Library", priority: 1, images: [][]byte{
			cover(200, 300, color.RGBA{R: 0xB4, G: 0x7A, B: 0x1E, A: 255}),
			cover(80, 120, color.RGBA{R: 0x1E, G: 0x6E, B: 0x78, A: 255}),
		}},
		&source{name: "Google Books", priority: 2, images: [][]byte{
			cover(400, 600, color.RGBA{R: 0x6B, G: 0x2D, B: 0x5C, A: 255}),
		}},
	}}
}

// source is a website that holds the same covers for every book.
type source struct {
	name     string
	priority int
	images   [][]byte
}

func (s *source) Name() string  { return s.name }
func (s *source) Priority() int { return s.priority }
func (s *source) Find(_ context.Context, q covers.Query) ([][]byte, error) {
	switch q.Title {
	case "Unreachable":
		return nil, errors.New("connection refused")
	case "Nothing":
		return nil, nil
	}
	return s.images, nil
}

// cover draws a plain cover: a colour with a lighter band across it.
func cover(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	band := color.RGBA{R: c.R/2 + 127, G: c.G/2 + 127, B: c.B/2 + 127, A: 255}
	for y := range h {
		for x := range w {
			if y > h/3 && y < h/2 {
				img.Set(x, y, band)
			} else {
				img.Set(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
