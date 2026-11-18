package pdf

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// ErrEncrypted is returned when a PDF is encrypted.
var ErrEncrypted = errors.New("pdf: the file is encrypted")

// ErrNotPDF is returned when a file has no usable PDF structure.
var ErrNotPDF = errors.New("pdf: not a PDF file")

// maxResolve bounds how many references are followed to reach a value.
const maxResolve = 32

// objPattern finds the headers of indirect objects when the cross
// reference table has to be rebuilt.
var objPattern = regexp.MustCompile(`(\d{1,10})[ \t\r\n]+(\d{1,5})[ \t\r\n]+obj\b`)

// entry says where an indirect object is stored.
type entry struct {
	// offset is the position of the object in the file.
	offset int
	// container is the number of the object stream holding the object,
	// or zero when it is stored directly.
	container int
	// free marks deleted objects.
	free bool
}

// Stream is a PDF stream object.
type Stream struct {
	// Dict is the stream dictionary.
	Dict Dict
	// raw holds the stream data as stored in the file.
	raw []byte
	// doc is the document the stream belongs to.
	doc *Document
	// num is the object number of the stream, or zero when unknown.
	num int
}

// Document is an open PDF file.
type Document struct {
	// data is the content of the file.
	data []byte
	// release frees data.
	release func() error
	// xref locates the indirect objects.
	xref map[int]entry
	// trailer is the trailer dictionary.
	trailer Dict
	// cache holds the objects parsed so far.
	cache map[int]any
	// rebuilt reports whether the cross reference table was rebuilt.
	rebuilt bool
	// pages lists the pages in order.
	pages []*Page
	// pageIndex maps the object numbers of pages to their positions.
	pageIndex map[int]int
	// fonts caches the fonts loaded so far by object number.
	fonts map[int]*font
}

// loadWhole reads the file at path onto the heap.
func loadWhole(path string) ([]byte, func() error, error) {
	data, err := os.ReadFile(path)
	return data, func() error { return nil }, err
}

// Open opens the PDF file at path.
func Open(path string) (*Document, error) {
	data, release, err := load(path)
	if err != nil {
		return nil, err
	}
	d, err := parse(data)
	if err != nil {
		release()
		return nil, err
	}
	d.release = release
	return d, nil
}

// Parse opens a PDF held in memory.
func Parse(data []byte) (*Document, error) {
	d, err := parse(data)
	if err != nil {
		return nil, err
	}
	d.release = func() error { return nil }
	return d, nil
}

// parse reads the structure of the PDF in data.
func parse(data []byte) (*Document, error) {
	head := data[:min(len(data), 1024)]
	if !bytes.Contains(head, []byte("%PDF-")) {
		return nil, ErrNotPDF
	}
	d := &Document{data: data, xref: map[int]entry{}, cache: map[int]any{}, fonts: map[int]*font{}}
	if err := d.loadXref(); err != nil || d.catalog() == nil {
		d.rebuild()
	}
	if d.trailer["Encrypt"] != nil {
		return nil, ErrEncrypted
	}
	if d.catalog() == nil {
		return nil, fmt.Errorf("%w: no document catalog", ErrNotPDF)
	}
	d.loadPages()
	return d, nil
}

// Close releases the file.
func (d *Document) Close() error {
	return d.release()
}

// catalog returns the document catalog, or nil when there is none.
func (d *Document) catalog() Dict {
	return d.dict(d.trailer["Root"])
}

// loadXref reads the chain of cross reference sections.
func (d *Document) loadXref() error {
	tail := d.data[max(0, len(d.data)-2048):]
	at := bytes.LastIndex(tail, []byte("startxref"))
	if at < 0 {
		return errors.New("pdf: no startxref")
	}
	l := lexer{data: tail, pos: at + len("startxref")}
	offset, _ := l.object()
	next, ok := offset.(int64)
	seen := map[int]bool{}
	for ok && next > 0 && !seen[int(next)] && len(seen) < 1024 {
		seen[int(next)] = true
		trailer, err := d.readSection(int(next))
		if err != nil {
			return err
		}
		if d.trailer == nil {
			d.trailer = trailer
		}
		if hybrid, isInt := trailer["XRefStm"].(int64); isInt {
			d.readSection(int(hybrid))
		}
		next, ok = trailer["Prev"].(int64)
	}
	if d.trailer == nil {
		return errors.New("pdf: no trailer")
	}
	return nil
}

// readSection reads the cross reference section at offset, which is
// either a table or a stream, and returns its trailer dictionary.
func (d *Document) readSection(offset int) (Dict, error) {
	if offset < 0 || offset >= len(d.data) {
		return nil, errors.New("pdf: cross reference offset out of range")
	}
	l := lexer{data: d.data, pos: offset}
	l.skipSpace()
	if l.has("xref") {
		l.pos += len("xref")
		return d.readTable(&l)
	}
	obj, _, err := d.parseAt(offset)
	stream, ok := obj.(*Stream)
	if err != nil || !ok {
		return nil, errors.New("pdf: no cross reference section")
	}
	return stream.Dict, d.readXrefStream(stream)
}

// add records where object num is stored unless a newer section already
// said so.
func (d *Document) add(num int, e entry) {
	if _, known := d.xref[num]; !known {
		d.xref[num] = e
	}
}

// readTable reads a classic cross reference table and its trailer.
func (d *Document) readTable(l *lexer) (Dict, error) {
	for {
		l.skipSpace()
		if l.has("trailer") {
			l.pos += len("trailer")
			obj, _ := l.object()
			trailer, ok := obj.(Dict)
			if !ok {
				return nil, errors.New("pdf: malformed trailer")
			}
			return trailer, nil
		}
		first, ok1 := l.object()
		count, ok2 := l.object()
		start, isInt1 := first.(int64)
		n, isInt2 := count.(int64)
		if !ok1 || !ok2 || !isInt1 || !isInt2 {
			return nil, errors.New("pdf: malformed cross reference table")
		}
		for i := range n {
			offset, _ := l.object()
			l.object()
			kind, _ := l.object()
			off, _ := offset.(int64)
			d.add(int(start+i), entry{offset: int(off), free: kind != keyword("n")})
		}
	}
}

// readXrefStream reads the entries of a cross reference stream.
func (d *Document) readXrefStream(s *Stream) error {
	data, err := s.Data()
	if err != nil {
		return err
	}
	widths := d.array(s.Dict["W"])
	if len(widths) < 3 {
		return errors.New("pdf: malformed cross reference stream")
	}
	var w [3]int
	for i := range w {
		w[i] = d.integer(widths[i], 0)
	}
	index := d.array(s.Dict["Index"])
	if len(index) == 0 {
		index = Array{int64(0), int64(d.integer(s.Dict["Size"], 0))}
	}
	size := w[0] + w[1] + w[2]
	for i := 0; i+1 < len(index) && size > 0; i += 2 {
		start, count := d.integer(index[i], 0), d.integer(index[i+1], 0)
		for n := 0; n < count && len(data) >= size; n++ {
			d.addStreamEntry(start+n, data[:size], w)
			data = data[size:]
		}
	}
	return nil
}

// field reads a big endian integer from b.
func field(b []byte) int {
	v := 0
	for _, c := range b {
		v = v<<8 | int(c)
	}
	return v
}

// addStreamEntry records one entry of a cross reference stream, whose
// three fields have the widths in w.
func (d *Document) addStreamEntry(num int, row []byte, w [3]int) {
	kind := 1
	if w[0] > 0 {
		kind = field(row[:w[0]])
	}
	second := field(row[w[0] : w[0]+w[1]])
	switch kind {
	case 0:
		d.add(num, entry{free: true})
	case 1:
		d.add(num, entry{offset: second})
	case 2:
		d.add(num, entry{container: second})
	}
}

// rebuild recreates the cross reference table by scanning the file for
// objects, for files whose table is missing or wrong.
func (d *Document) rebuild() {
	d.rebuilt = true
	d.xref = map[int]entry{}
	d.cache = map[int]any{}
	var streams []int
	for _, m := range objPattern.FindAllSubmatchIndex(d.data, -1) {
		num, err := strconv.Atoi(string(d.data[m[2]:m[3]]))
		if err != nil {
			continue
		}
		d.xref[num] = entry{offset: m[0]}
		window := d.data[m[1]:min(len(d.data), m[1]+256)]
		if bytes.Contains(window, []byte("/ObjStm")) || bytes.Contains(window, []byte("/XRef")) {
			streams = append(streams, num)
		}
	}
	d.trailer = d.findTrailer()
	for _, num := range streams {
		d.adoptStream(num)
	}
	if d.catalog() == nil {
		d.findCatalog()
	}
}

// findTrailer returns the last trailer dictionary in the file that names
// a document catalog.
func (d *Document) findTrailer() Dict {
	trailer := Dict{}
	rest := d.data
	base := 0
	for {
		at := bytes.Index(rest, []byte("trailer"))
		if at < 0 {
			return trailer
		}
		l := lexer{data: d.data, pos: base + at + len("trailer"), refs: true}
		if obj, _ := l.object(); obj != nil {
			if found, ok := obj.(Dict); ok && found["Root"] != nil {
				trailer = found
			}
		}
		base += at + len("trailer")
		rest = d.data[base:]
	}
}

// adoptStream registers the objects held by the object stream num, or
// takes over the trailer entries of the cross reference stream num.
func (d *Document) adoptStream(num int) {
	stream := d.stream(Ref{Num: num})
	if stream == nil {
		return
	}
	switch d.name(stream.Dict["Type"]) {
	case "XRef":
		if stream.Dict["Root"] != nil {
			for _, key := range []Name{"Root", "Info", "Encrypt"} {
				if stream.Dict[key] != nil {
					d.trailer[key] = stream.Dict[key]
				}
			}
		}
	case "ObjStm":
		for _, contained := range d.containedNumbers(stream) {
			d.xref[contained] = entry{container: num}
		}
	}
}

// findCatalog looks for the document catalog among all objects.
func (d *Document) findCatalog() {
	for num := range d.xref {
		if dict, ok := d.object(num).(Dict); ok && d.name(dict["Type"]) == "Catalog" {
			d.trailer["Root"] = Ref{Num: num}
			return
		}
	}
}

// parseAt parses the indirect object stored at offset and returns it with
// its object number.
func (d *Document) parseAt(offset int) (any, int, error) {
	if offset < 0 || offset >= len(d.data) {
		return nil, 0, errors.New("pdf: object offset out of range")
	}
	l := lexer{data: d.data, pos: offset, refs: true}
	num, _ := l.object()
	l.object()
	marker, _ := l.object()
	n, ok := num.(int64)
	if !ok || marker != keyword("obj") {
		return nil, 0, errors.New("pdf: no object at offset")
	}
	obj, _ := l.object()
	dict, isDict := obj.(Dict)
	l.skipSpace()
	if !isDict || !l.has("stream") {
		return obj, int(n), nil
	}
	l.pos += len("stream")
	if l.has("\r\n") {
		l.pos += 2
	} else if l.has("\n") || l.has("\r") {
		l.pos++
	}
	return &Stream{Dict: dict, raw: d.streamBytes(dict, l.pos), doc: d, num: int(n)}, int(n), nil
}

// streamBytes returns the data of the stream starting at start, trusting
// the declared length only when the stream really ends there.
func (d *Document) streamBytes(dict Dict, start int) []byte {
	length := d.integer(dict["Length"], -1)
	if end := start + length; length >= 0 && end <= len(d.data) {
		l := lexer{data: d.data, pos: end}
		l.skipSpace()
		if l.has("endstream") {
			return d.data[start:end]
		}
	}
	end := bytes.Index(d.data[start:], []byte("endstream"))
	if end < 0 {
		return d.data[start:]
	}
	raw := d.data[start : start+end]
	raw = bytes.TrimSuffix(raw, []byte("\n"))
	return bytes.TrimSuffix(raw, []byte("\r"))
}

// object returns the indirect object num, or nil when it does not exist.
func (d *Document) object(num int) any {
	if obj, ok := d.cache[num]; ok {
		return obj
	}
	e, known := d.xref[num]
	if !known || e.free {
		return nil
	}
	d.cache[num] = nil
	if e.container != 0 {
		d.loadContainer(e.container)
		return d.cache[num]
	}
	obj, found, err := d.parseAt(e.offset)
	if (err != nil || found != num) && !d.rebuilt {
		d.rebuild()
		return d.object(num)
	}
	if err != nil || found != num {
		return nil
	}
	d.cache[num] = obj
	return obj
}

// containedNumbers returns the numbers of the objects held by the object
// stream s.
func (d *Document) containedNumbers(s *Stream) []int {
	data, err := s.Data()
	if err != nil {
		return nil
	}
	l := lexer{data: data}
	var nums []int
	for range d.integer(s.Dict["N"], 0) {
		num, ok1 := l.object()
		_, ok2 := l.object()
		n, isInt := num.(int64)
		if !ok1 || !ok2 || !isInt {
			break
		}
		nums = append(nums, int(n))
	}
	return nums
}

// loadContainer parses every object held by the object stream num into
// the cache.
func (d *Document) loadContainer(num int) {
	e, known := d.xref[num]
	if !known || e.container != 0 {
		return
	}
	stream, _ := d.object(num).(*Stream)
	if stream == nil {
		return
	}
	data, err := stream.Data()
	if err != nil {
		return
	}
	first := d.integer(stream.Dict["First"], 0)
	header := lexer{data: data}
	for range d.integer(stream.Dict["N"], 0) {
		numObj, ok1 := header.object()
		offObj, ok2 := header.object()
		n, isInt1 := numObj.(int64)
		off, isInt2 := offObj.(int64)
		if !ok1 || !ok2 || !isInt1 || !isInt2 || first+int(off) > len(data) {
			return
		}
		if d.xref[int(n)].container != num {
			continue
		}
		body := lexer{data: data, pos: first + int(off), refs: true}
		obj, _ := body.object()
		d.cache[int(n)] = obj
	}
}

// resolve follows references until it reaches a direct object.
func (d *Document) resolve(obj any) any {
	for range maxResolve {
		ref, ok := obj.(Ref)
		if !ok {
			return obj
		}
		obj = d.object(ref.Num)
	}
	return nil
}

// dict returns obj as a dictionary, taking the dictionary of a stream,
// or nil when it is neither.
func (d *Document) dict(obj any) Dict {
	switch v := d.resolve(obj).(type) {
	case Dict:
		return v
	case *Stream:
		return v.Dict
	}
	return nil
}

// array returns obj as an array, or nil when it is not one.
func (d *Document) array(obj any) Array {
	v, _ := d.resolve(obj).(Array)
	return v
}

// name returns obj as a name, or the empty name when it is not one.
func (d *Document) name(obj any) Name {
	v, _ := d.resolve(obj).(Name)
	return v
}

// stream returns obj as a stream, or nil when it is not one.
func (d *Document) stream(obj any) *Stream {
	v, _ := d.resolve(obj).(*Stream)
	return v
}

// number returns obj as a number and whether it is one.
func (d *Document) number(obj any) (float64, bool) {
	switch v := d.resolve(obj).(type) {
	case int64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

// float returns obj as a number, or fallback when it is not one.
func (d *Document) float(obj any, fallback float64) float64 {
	if v, ok := d.number(obj); ok {
		return v
	}
	return fallback
}

// integer returns obj as an integer, or fallback when it is not a
// number.
func (d *Document) integer(obj any, fallback int) int {
	if v, ok := d.number(obj); ok {
		return int(v)
	}
	return fallback
}

// text returns obj as a decoded text string, or the empty string when it
// is not a string.
func (d *Document) text(obj any) string {
	v, ok := d.resolve(obj).(String)
	if !ok {
		return ""
	}
	return DecodeText(v)
}
