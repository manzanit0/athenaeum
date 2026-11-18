package azw3

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"

	_ "image/gif"
	_ "image/png"

	"github.com/manzanit0/athenaeum/internal/kf8"
	"github.com/manzanit0/athenaeum/internal/meta"
)

// Image limits.
const (
	// maxImageBytes bounds the size of an image taken from the EPUB.
	maxImageBytes = 64 << 20
	// thumbWidth is the largest width of the cover thumbnail.
	thumbWidth = 180
	// thumbHeight is the largest height of the cover thumbnail.
	thumbHeight = 240
	// jpegQuality is the quality images are re-encoded with.
	jpegQuality = 90
)

// jfifHeader is the JFIF segment Kindles expect at the start of a JPEG,
// which the standard encoder leaves out.
var jfifHeader = []byte{0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0}

// imageTypes maps the image types the Kindle shows to their MIME types.
var imageTypes = map[string]string{"jpg": "image/jpeg", "png": "image/png", "gif": "image/gif"}

// needsReencoding reports whether the JPEG data uses a variant Kindle
// firmware cannot show: progressive scans or four colour components.
func needsReencoding(data []byte) bool {
	for i := 2; i+9 < len(data) && data[i] == 0xFF; {
		marker := data[i+1]
		size := int(binary.BigEndian.Uint16(data[i+2:]))
		switch {
		case marker == 0xC2:
			return true
		case marker == 0xC0 || marker == 0xC1:
			return data[i+9] == 4
		case marker == 0xDA:
			return false
		}
		i += 2 + size
	}
	return false
}

// baseline returns the JPEG data as a baseline RGB or grey JPEG.
func baseline(data []byte) ([]byte, error) {
	if !needsReencoding(data) {
		return data, nil
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return encodeJPEG(img)
}

// encodeJPEG encodes img as a JFIF JPEG without an alpha channel.
func encodeJPEG(img image.Image) ([]byte, error) {
	if _, grey := img.(*image.Gray); !grey {
		flat := image.NewRGBA(img.Bounds())
		draw.Draw(flat, flat.Bounds(), image.White, image.Point{}, draw.Src)
		draw.Draw(flat, flat.Bounds(), img, img.Bounds().Min, draw.Over)
		img = flat
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	encoded := buf.Bytes()
	out := append([]byte{}, encoded[:2]...)
	out = append(out, jfifHeader...)
	return append(out, encoded[2:]...), nil
}

// addResource stores image data in the book, once, and returns its index
// or -1 when the Kindle cannot show it.
func (c *converter) addResource(data []byte) int {
	kind := meta.ImageExt(data)
	if _, ok := imageTypes[kind]; !ok {
		return -1
	}
	if kind == "jpg" {
		fixed, err := baseline(data)
		if err != nil {
			return -1
		}
		data = fixed
	}
	for i, existing := range c.book.Resources {
		if bytes.Equal(existing, data) {
			return i
		}
	}
	c.book.Resources = append(c.book.Resources, data)
	return len(c.book.Resources) - 1
}

// resource returns the index of the image at name in the EPUB, loading
// it the first time, or -1 when it cannot be used.
func (c *converter) resource(name string) int {
	if index, ok := c.resources[name]; ok {
		return index
	}
	index := -1
	data, err := c.read(name)
	switch {
	case err != nil:
		c.warn("image %s is missing", name)
	case len(data) > maxImageBytes:
		c.warn("image %s is too large and was left out", name)
	default:
		if index = c.addResource(data); index < 0 {
			c.warn("image %s is in a format the Kindle cannot show and was left out", name)
		}
	}
	c.resources[name] = index
	return index
}

// imageAddress returns the address of the image that ref, a reference
// found in the file base, points at.
func (c *converter) imageAddress(ref, base string) (string, bool) {
	name, _, local := resolve(base, ref)
	if !local || name == "" {
		return "", false
	}
	index := c.resource(name)
	if index < 0 {
		return "", false
	}
	mime := imageTypes[meta.ImageExt(c.book.Resources[index])]
	return fmt.Sprintf("kindle:embed:%s?mime=%s", kf8.Base32(index+1, 4), mime), true
}

// shrink returns img scaled down to fit a box of width by height,
// averaging the pixels that fall into each output pixel.
func shrink(img image.Image, width, height int) image.Image {
	b := img.Bounds()
	if b.Dx() <= width && b.Dy() <= height {
		return img
	}
	w, h := width, b.Dy()*width/b.Dx()
	if h > height {
		w, h = b.Dx()*height/b.Dy(), height
	}
	w, h = max(w, 1), max(h, 1)
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+(y+1)*b.Dy()/h
		for x := range w {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+(x+1)*b.Dx()/w
			out.Set(x, y, average(img, image.Rect(x0, y0, max(x1, x0+1), max(y1, y0+1))))
		}
	}
	return out
}

// average returns the mean colour of the pixels of img inside r.
func average(img image.Image, r image.Rectangle) color.Color {
	var sum [4]uint64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, ca := img.At(x, y).RGBA()
			sum[0], sum[1], sum[2], sum[3] = sum[0]+uint64(cr), sum[1]+uint64(cg), sum[2]+uint64(cb), sum[3]+uint64(ca)
		}
	}
	n := uint64(r.Dx() * r.Dy())
	return color.RGBA64{R: uint16(sum[0] / n), G: uint16(sum[1] / n), B: uint16(sum[2] / n), A: uint16(sum[3] / n)}
}

// setCover stores the cover image and a thumbnail of it in the book.
func (c *converter) setCover(data []byte) {
	if len(data) == 0 {
		return
	}
	index := c.addResource(data)
	if index < 0 {
		c.warn("the cover is in a format the Kindle cannot show and was left out")
		return
	}
	c.book.Cover = index
	img, _, err := image.Decode(bytes.NewReader(c.book.Resources[index]))
	if err != nil {
		return
	}
	if thumb, err := encodeJPEG(shrink(img, thumbWidth, thumbHeight)); err == nil {
		c.book.Thumbnail = c.addResource(thumb)
	}
}
