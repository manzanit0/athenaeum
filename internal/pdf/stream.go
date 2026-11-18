package pdf

import (
	"bytes"
	"compress/lzw"
	"compress/zlib"
	"encoding/ascii85"
	"errors"
	"fmt"
	"io"
)

// maxDecoded bounds the size of a decoded stream, as a guard against
// files that expand without limit.
const maxDecoded = 256 << 20

// imageFilters names the filters that produce an image file rather than
// plain data, and are therefore left for the image code to handle.
var imageFilters = map[Name]bool{
	"DCTDecode": true, "DCT": true, "JPXDecode": true, "CCITTFaxDecode": true, "CCF": true,
	"JBIG2Decode": true,
}

// filters returns the filters of the stream with their parameters, in
// the order they have to be undone.
func (s *Stream) filters() ([]Name, []Dict) {
	d := s.doc
	var names []Name
	switch f := d.resolve(s.Dict["Filter"]).(type) {
	case Name:
		names = []Name{f}
	case Array:
		for _, item := range f {
			names = append(names, d.name(item))
		}
	}
	parms := make([]Dict, len(names))
	switch p := d.resolve(s.Dict["DecodeParms"]).(type) {
	case Dict:
		if len(parms) > 0 {
			parms[0] = p
		}
	case Array:
		for i := 0; i < len(p) && i < len(parms); i++ {
			parms[i] = d.dict(p[i])
		}
	}
	return names, parms
}

// Data returns the fully decoded content of the stream. It fails when
// the stream is stored in an image format.
func (s *Stream) Data() ([]byte, error) {
	data, rest, err := s.decode()
	if err != nil {
		return nil, err
	}
	if rest != "" {
		return nil, fmt.Errorf("pdf: stream uses the image filter %s", rest)
	}
	return data, nil
}

// decode undoes the filters of the stream until it meets an image
// filter, whose name it returns with the data that filter applies to.
func (s *Stream) decode() ([]byte, Name, error) {
	names, parms := s.filters()
	data := s.raw
	for i, name := range names {
		if imageFilters[name] {
			return data, name, nil
		}
		decoded, err := s.unfilter(name, parms[i], data)
		if err != nil {
			return nil, "", err
		}
		data = decoded
	}
	return data, "", nil
}

// unfilter undoes one filter.
func (s *Stream) unfilter(name Name, parms Dict, data []byte) ([]byte, error) {
	var out []byte
	var err error
	switch name {
	case "FlateDecode", "Fl":
		out, err = inflate(data)
	case "LZWDecode", "LZW":
		early := s.doc.integer(parms["EarlyChange"], 1)
		out, err = unLZW(data, early)
	case "ASCIIHexDecode", "AHx":
		l := lexer{data: append([]byte("<"), data...)}
		return []byte(l.hex()), nil
	case "ASCII85Decode", "A85":
		out, err = unASCII85(data)
	case "RunLengthDecode", "RL":
		return unRunLength(data), nil
	case "Crypt":
		return data, nil
	default:
		return nil, fmt.Errorf("pdf: unsupported filter %s", name)
	}
	if err != nil && len(out) == 0 {
		return nil, err
	}
	return s.unpredict(out, parms)
}

// readLimited reads r to its end, keeping what was read when the data
// is damaged part of the way through.
func readLimited(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	_, err := io.Copy(&buf, io.LimitReader(r, maxDecoded))
	return buf.Bytes(), err
}

// inflate undoes zlib compression.
func inflate(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return readLimited(r)
}

// unLZW undoes LZW compression. Only the variant that switches code
// width early, which is the default and what the standard library
// implements, is supported.
func unLZW(data []byte, early int) ([]byte, error) {
	if early == 0 {
		return nil, errors.New("pdf: LZW without early change is not supported")
	}
	r := lzw.NewReader(bytes.NewReader(data), lzw.MSB, 8)
	defer r.Close()
	return readLimited(r)
}

// unASCII85 undoes ASCII base 85 encoding.
func unASCII85(data []byte) ([]byte, error) {
	data = bytes.TrimSpace(data)
	data = bytes.TrimPrefix(data, []byte("<~"))
	if end := bytes.Index(data, []byte("~>")); end >= 0 {
		data = data[:end]
	}
	return readLimited(ascii85.NewDecoder(bytes.NewReader(data)))
}

// unRunLength undoes run length encoding.
func unRunLength(data []byte) []byte {
	var out []byte
	for i := 0; i < len(data); {
		n := int(data[i])
		i++
		switch {
		case n == 128:
			return out
		case n < 128:
			end := min(i+n+1, len(data))
			out = append(out, data[i:end]...)
			i = end
		case i < len(data):
			out = append(out, bytes.Repeat(data[i:i+1], 257-n)...)
			i++
		}
	}
	return out
}

// unpredict undoes the PNG or TIFF predictor described by parms.
func (s *Stream) unpredict(data []byte, parms Dict) ([]byte, error) {
	d := s.doc
	predictor := d.integer(parms["Predictor"], 1)
	if predictor <= 1 {
		return data, nil
	}
	colors := d.integer(parms["Colors"], 1)
	bits := d.integer(parms["BitsPerComponent"], 8)
	columns := d.integer(parms["Columns"], 1)
	pixel := max(1, colors*bits/8)
	row := (colors*bits*columns + 7) / 8
	if row <= 0 {
		return data, nil
	}
	if predictor == 2 {
		if bits == 8 {
			unTIFF(data, row, pixel)
		}
		return data, nil
	}
	return unPNG(data, row, pixel), nil
}

// unTIFF undoes horizontal differencing in place for 8 bit samples.
func unTIFF(data []byte, row, pixel int) {
	for start := 0; start+row <= len(data); start += row {
		line := data[start : start+row]
		for i := pixel; i < len(line); i++ {
			line[i] += line[i-pixel]
		}
	}
}

// paeth is the PNG Paeth predictor.
func paeth(a, b, c byte) byte {
	p := int(a) + int(b) - int(c)
	pa, pb, pc := abs(p-int(a)), abs(p-int(b)), abs(p-int(c))
	switch {
	case pa <= pb && pa <= pc:
		return a
	case pb <= pc:
		return b
	}
	return c
}

// abs returns the absolute value of v.
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// unPNG undoes the PNG row filters. Each row of the input starts with a
// byte naming its filter.
func unPNG(data []byte, row, pixel int) []byte {
	out := make([]byte, 0, len(data)/(row+1)*row)
	prev := make([]byte, row)
	for start := 0; start+1+row <= len(data); start += row + 1 {
		kind := data[start]
		line := data[start+1 : start+1+row]
		cur := make([]byte, row)
		for i := range line {
			var left, up, corner byte
			if i >= pixel {
				left, corner = cur[i-pixel], prev[i-pixel]
			}
			up = prev[i]
			cur[i] = line[i] + pngPredict(kind, left, up, corner)
		}
		out = append(out, cur...)
		prev = cur
	}
	return out
}

// pngPredict returns the predicted value for the PNG row filter kind.
func pngPredict(kind, left, up, corner byte) byte {
	switch kind {
	case 1:
		return left
	case 2:
		return up
	case 3:
		return byte((int(left) + int(up)) / 2)
	case 4:
		return paeth(left, up, corner)
	}
	return 0
}
