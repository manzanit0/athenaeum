package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
)

// maxPixels bounds the size of the images that are decoded.
const maxPixels = 40_000_000

// ErrUnsupportedImage is returned for images stored in a way this
// package cannot turn into a JPEG or PNG file.
var ErrUnsupportedImage = errors.New("unsupported image")

// Image is an image stored in a document.
type Image struct {
	// ID identifies the image within its document, so an image drawn
	// several times can be recognised.
	ID int
	// Width is the width in pixels.
	Width int
	// Height is the height in pixels.
	Height int
	// stream holds the image data.
	stream *Stream
}

// colorSpace describes how the samples of an image map to colours.
type colorSpace struct {
	// components is the number of samples per pixel.
	components int
	// palette holds the colours of an indexed image, three bytes each.
	palette []byte
	// cmyk reports whether the four components are CMYK.
	cmyk bool
}

// image wraps an image stream.
func (d *Document) image(stream *Stream) *Image {
	return &Image{
		ID: stream.num, stream: stream,
		Width:  d.integer(stream.Dict["Width"], 0),
		Height: d.integer(stream.Dict["Height"], 0),
	}
}

// unsupported returns an ErrUnsupportedImage carrying the reason.
func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupportedImage, fmt.Sprintf(format, args...))
}

// Encode returns the image as a JPEG or PNG file with the matching file
// extension, "jpg" or "png".
func (im *Image) Encode() ([]byte, string, error) {
	d := im.stream.doc
	dict := im.stream.Dict
	if im.Width <= 0 || im.Height <= 0 || im.Width*im.Height > maxPixels {
		return nil, "", unsupported("%d by %d pixels", im.Width, im.Height)
	}
	if mask, _ := d.resolve(dict["ImageMask"]).(bool); mask {
		return nil, "", unsupported("stencil mask")
	}
	data, filter, err := im.stream.decode()
	if err != nil {
		return nil, "", unsupported("%v", err)
	}
	switch filter {
	case "DCTDecode", "DCT":
		return encodeJPEG(data)
	case "":
	default:
		return nil, "", unsupported("%s encoding", filter)
	}
	space, err := d.colorSpace(dict["ColorSpace"], 0)
	if err != nil {
		return nil, "", err
	}
	samples, err := unpack(data, im.Width, im.Height, space.components,
		d.integer(dict["BitsPerComponent"], 8), space.palette == nil)
	if err != nil {
		return nil, "", err
	}
	if inverted(d, dict["Decode"]) {
		for i := range samples {
			samples[i] = 255 - samples[i]
		}
	}
	img := im.build(samples, space)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "png", nil
}

// encodeJPEG returns JPEG data as it is, except for CMYK images, which
// browsers and e-readers show wrongly and are converted to RGB.
func encodeJPEG(data []byte) ([]byte, string, error) {
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", unsupported("damaged JPEG data")
	}
	if config.ColorModel != color.CMYKModel {
		return data, "jpg", nil
	}
	if config.Width*config.Height > maxPixels {
		return nil, "", unsupported("%d by %d pixels", config.Width, config.Height)
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", unsupported("damaged JPEG data")
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "jpg", nil
}

// inverted reports whether a Decode array maps samples backwards.
func inverted(d *Document, obj any) bool {
	arr := d.array(obj)
	return len(arr) >= 2 && d.float(arr[0], 0) > d.float(arr[1], 1)
}

// colorSpace interprets a ColorSpace entry.
func (d *Document) colorSpace(obj any, depth int) (colorSpace, error) {
	switch v := d.resolve(obj).(type) {
	case Name:
		return namedSpace(v)
	case Array:
		if len(v) == 0 || depth > 4 {
			break
		}
		return d.arraySpace(v, depth)
	}
	return colorSpace{}, unsupported("unknown colour space")
}

// namedSpace interprets a colour space given by name.
func namedSpace(name Name) (colorSpace, error) {
	switch name {
	case "DeviceGray", "G", "CalGray":
		return colorSpace{components: 1}, nil
	case "DeviceRGB", "RGB", "CalRGB":
		return colorSpace{components: 3}, nil
	case "DeviceCMYK", "CMYK":
		return colorSpace{components: 4, cmyk: true}, nil
	}
	return colorSpace{}, unsupported("%s colour space", name)
}

// arraySpace interprets a colour space given as an array.
func (d *Document) arraySpace(arr Array, depth int) (colorSpace, error) {
	kind := d.name(arr[0])
	switch kind {
	case "ICCBased":
		if len(arr) < 2 {
			break
		}
		switch d.integer(d.dict(arr[1])["N"], 3) {
		case 1:
			return colorSpace{components: 1}, nil
		case 3:
			return colorSpace{components: 3}, nil
		case 4:
			return colorSpace{components: 4, cmyk: true}, nil
		}
	case "Indexed", "I":
		if len(arr) < 4 {
			break
		}
		return d.indexedSpace(arr, depth)
	case "CalGray", "CalRGB", "DeviceGray", "DeviceRGB", "DeviceCMYK":
		return namedSpace(kind)
	}
	return colorSpace{}, unsupported("%s colour space", kind)
}

// indexedSpace interprets an indexed colour space, turning its lookup
// table into an RGB palette.
func (d *Document) indexedSpace(arr Array, depth int) (colorSpace, error) {
	base, err := d.colorSpace(arr[1], depth+1)
	if err != nil || base.palette != nil {
		return colorSpace{}, unsupported("indexed colour space")
	}
	var table []byte
	switch v := d.resolve(arr[3]).(type) {
	case String:
		table = []byte(v)
	case *Stream:
		if table, err = v.Data(); err != nil {
			return colorSpace{}, unsupported("indexed colour space")
		}
	}
	palette := make([]byte, 0, 3*256)
	for i := 0; i+base.components <= len(table) && len(palette) < 3*256; i += base.components {
		r, g, b := toRGB(table[i:i+base.components], base)
		palette = append(palette, r, g, b)
	}
	return colorSpace{components: 1, palette: palette}, nil
}

// toRGB converts one pixel in the given colour space to RGB.
func toRGB(pixel []byte, space colorSpace) (byte, byte, byte) {
	switch {
	case space.palette != nil:
		i := 3 * int(pixel[0])
		if i+2 >= len(space.palette) {
			return 0, 0, 0
		}
		return space.palette[i], space.palette[i+1], space.palette[i+2]
	case space.components == 1:
		return pixel[0], pixel[0], pixel[0]
	case space.cmyk:
		k := 255 - int(pixel[3])
		return byte((255 - int(pixel[0])) * k / 255), byte((255 - int(pixel[1])) * k / 255),
			byte((255 - int(pixel[2])) * k / 255)
	}
	return pixel[0], pixel[1], pixel[2]
}

// unpack returns the samples of an image as one byte each. Samples with
// fewer than eight bits are stretched to the full range when stretch is
// set, which is wanted for intensities but not for palette indexes.
func unpack(data []byte, width, height, components, bits int, stretch bool) ([]byte, error) {
	count := width * height * components
	switch bits {
	case 8:
		if len(data) < count {
			return nil, unsupported("truncated image data")
		}
		return data[:count], nil
	case 16:
		if len(data) < 2*count {
			return nil, unsupported("truncated image data")
		}
		out := make([]byte, count)
		for i := range out {
			out[i] = data[2*i]
		}
		return out, nil
	case 1, 2, 4:
		return unpackBits(data, width, height, components, bits, stretch)
	}
	return nil, unsupported("%d bits per component", bits)
}

// unpackBits unpacks samples of one, two or four bits, whose rows start
// on byte boundaries.
func unpackBits(data []byte, width, height, components, bits int, stretch bool) ([]byte, error) {
	perRow := width * components
	rowBytes := (perRow*bits + 7) / 8
	if len(data) < rowBytes*height {
		return nil, unsupported("truncated image data")
	}
	scale := 1
	if stretch {
		scale = 255 / (1<<bits - 1)
	}
	out := make([]byte, 0, perRow*height)
	mask := byte(1<<bits - 1)
	for y := range height {
		row := data[y*rowBytes : (y+1)*rowBytes]
		for i := range perRow {
			shift := 8 - bits - (i*bits)%8
			out = append(out, (row[i*bits/8]>>shift&mask)*byte(scale))
		}
	}
	return out, nil
}

// alpha returns the soft mask of the image as one byte per pixel, or nil
// when there is none that fits.
func (im *Image) alpha() []byte {
	d := im.stream.doc
	mask := d.stream(im.stream.Dict["SMask"])
	if mask == nil {
		return nil
	}
	w, h := d.integer(mask.Dict["Width"], 0), d.integer(mask.Dict["Height"], 0)
	if w != im.Width || h != im.Height {
		return nil
	}
	data, err := mask.Data()
	if err != nil {
		return nil
	}
	samples, err := unpack(data, w, h, 1, d.integer(mask.Dict["BitsPerComponent"], 8), true)
	if err != nil {
		return nil
	}
	return samples
}

// build turns samples into an image.
func (im *Image) build(samples []byte, space colorSpace) image.Image {
	alpha := im.alpha()
	if alpha == nil && space.components == 1 && space.palette == nil {
		return &image.Gray{Pix: samples, Stride: im.Width, Rect: image.Rect(0, 0, im.Width, im.Height)}
	}
	img := image.NewNRGBA(image.Rect(0, 0, im.Width, im.Height))
	for i := range im.Width * im.Height {
		r, g, b := toRGB(samples[i*space.components:(i+1)*space.components], space)
		a := byte(255)
		if alpha != nil {
			a = alpha[i]
		}
		copy(img.Pix[4*i:], []byte{r, g, b, a})
	}
	return img
}

// Encodable reports whether Encode is likely to succeed, judging by how
// the image is stored and without decoding it: it is false for stencil
// masks, images of unusable sizes and images kept in the JPEG 2000,
// fax and JBIG2 encodings.
func (im *Image) Encodable() bool {
	d := im.stream.doc
	if im.Width <= 0 || im.Height <= 0 || im.Width*im.Height > maxPixels {
		return false
	}
	if mask, _ := d.resolve(im.stream.Dict["ImageMask"]).(bool); mask {
		return false
	}
	names, _ := im.stream.filters()
	for _, name := range names {
		if imageFilters[name] {
			return name == "DCTDecode" || name == "DCT"
		}
	}
	return true
}
