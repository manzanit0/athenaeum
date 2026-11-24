package device

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"path"
	"path/filepath"
	"strings"

	_ "image/gif"
	_ "image/png"

	xdraw "golang.org/x/image/draw"

	"github.com/manzanit0/athenaeum/internal/meta"
)

// Thumbnail geometry and storage. A Kindle keeps the cover picture of
// each book in system/thumbnails, named after the book's identifier and
// type, and shows "No image available" when there is none. It makes
// them itself for books it can read the cover of; this one is written
// as well, as calibre does, so the book has one from the start.
const (
	// systemDir is the folder of the Kindle's own files.
	systemDir = "system"
	// thumbnailsDir is the folder holding the cover pictures.
	thumbnailsDir = "system/thumbnails"
	// thumbnailWidth is the largest width of a cover picture.
	thumbnailWidth = 330
	// thumbnailHeight is the largest height of a cover picture.
	thumbnailHeight = 470
	// thumbnailQuality is the quality the pictures are encoded with.
	thumbnailQuality = 90
)

// ErrNoThumbnail is returned when a book has nothing to name or show a
// thumbnail with.
var ErrNoThumbnail = errors.New("the book has no Kindle identifier or cover")

// EncodeThumbnail scales the cover image in data down to the size of a
// Kindle thumbnail and returns it as a baseline JPEG.
func EncodeThumbnail(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading the cover: %w", err)
	}
	b := src.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return nil, errors.New("the cover is empty")
	}
	w, h := b.Dx(), b.Dy()
	if w > thumbnailWidth || h > thumbnailHeight {
		if w*thumbnailHeight > h*thumbnailWidth {
			w, h = thumbnailWidth, max(h*thumbnailWidth/w, 1)
		} else {
			w, h = max(w*thumbnailHeight/h, 1), thumbnailHeight
		}
	}
	flat := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(flat, flat.Bounds(), image.White, image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(flat, flat.Bounds(), src, b, xdraw.Over, nil)
	var out bytes.Buffer
	if err := jpeg.Encode(&out, flat, &jpeg.Options{Quality: thumbnailQuality}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// thumbnailPath returns the path, relative to the Kindle root, of the
// thumbnail of the book with the given identifier and document type.
func thumbnailPath(asin, kind string) string {
	return path.Join(thumbnailsDir, fmt.Sprintf("thumbnail_%s_%s_portrait.jpg", asin, kind))
}

// safeID reports whether an identifier can be part of a file name.
func safeID(id string) bool {
	return id != "" && !strings.ContainsAny(id, `/\:*?"<>|`) && !strings.ContainsFunc(id, func(r rune) bool { return r < 0x20 })
}

// PutThumbnail makes the Kindle show the cover of the book it was sent
// as rel, a path returned by Put, so it does not list the book as
// having "No image available". The identifier to name it by is read from
// the book's file on the Kindle, and the picture is the cover that file
// holds, falling back to the image at fallback when it holds none. Books
// that declare no identifier need no thumbnail and give ErrNoThumbnail.
func (k *Kindle) PutThumbnail(rel, fallback string) error {
	if !validPath(rel) {
		return ErrInvalidPath
	}
	root, err := k.open()
	if err != nil {
		return err
	}
	defer root.Close()
	file := filepath.Join(root.Name(), filepath.FromSlash(rel))
	asin, kind := meta.KindleID(file)
	if !safeID(asin) {
		return ErrNoThumbnail
	}
	if kind == "" {
		kind = "PDOC"
	}
	var cover []byte
	if _, c := meta.Read(file, path.Base(rel)); c != nil {
		cover = c.Data
	}
	if len(cover) == 0 && fallback != "" {
		cover, _ = os.ReadFile(fallback)
	}
	if len(cover) == 0 {
		return ErrNoThumbnail
	}
	thumb, err := EncodeThumbnail(cover)
	if err != nil {
		return err
	}
	if info, err := root.Stat(systemDir); err != nil || !info.IsDir() {
		// Not a Kindle's own storage layout: leave it as it is.
		return nil
	}
	if err := root.MkdirAll(thumbnailsDir, 0o755); err != nil {
		return err
	}
	dest := thumbnailPath(asin, kind)
	partial := dest + ".partial"
	if err := writeFile(root, partial, thumb); err != nil {
		root.Remove(partial)
		return err
	}
	if err := root.Rename(partial, dest); err != nil {
		root.Remove(partial)
		return err
	}
	return nil
}

// removeThumbnail deletes the thumbnail of the book at rel, if the file
// is still there to tell which one it is.
func removeThumbnail(root *os.Root, rel string) {
	asin, kind := meta.KindleID(filepath.Join(root.Name(), filepath.FromSlash(rel)))
	if !safeID(asin) {
		return
	}
	if kind == "" {
		kind = "PDOC"
	}
	root.Remove(thumbnailPath(asin, kind))
}

// writeFile writes data to the file name inside root.
func writeFile(root *os.Root, name string, data []byte) error {
	out, err := root.Create(name)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}
