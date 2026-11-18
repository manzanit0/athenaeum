package kf8

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Errors returned when a file cannot be read as a KF8 book.
var (
	// ErrNotKF8 is returned for files that are not Kindle books or
	// that only hold the older MOBI format.
	ErrNotKF8 = errors.New("the file is not a KF8 book; older MOBI files are not supported")
	// ErrProtected is returned for books encrypted with DRM.
	ErrProtected = errors.New("the Kindle book is protected by DRM")
	// ErrCompression is returned for books compressed with Huffman
	// coding instead of PalmDOC compression.
	ErrCompression = errors.New("the Kindle book uses Huffman compression, which is not supported")
)

// Limits and codes used while reading.
const (
	// maxRecordBytes bounds the size of one record read into memory.
	maxRecordBytes = 256 << 20
	// noCompression is the code of uncompressed text.
	noCompression = 1
	// exthBoundary holds the number of the KF8 header record in a file
	// that carries both the older MOBI form of a book and its KF8 form.
	exthBoundary = 121
	// extraFlagsLength is the header length from which the header says
	// what follows the text in every record.
	extraFlagsLength = 0xE4
)

// database gives access to the records of a Palm database.
type database struct {
	// file is the open database.
	file *os.File
	// offsets holds where every record starts, followed by the size of
	// the file.
	offsets []int64
}

// openDatabase opens the Palm database at path.
func openDatabase(path string) (*database, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	db := &database{file: file}
	if err := db.readOffsets(); err != nil {
		file.Close()
		return nil, err
	}
	return db, nil
}

// readOffsets reads the record table of the database.
func (db *database) readOffsets() error {
	info, err := db.file.Stat()
	if err != nil {
		return err
	}
	head := make([]byte, 78)
	if _, err := db.file.ReadAt(head, 0); err != nil || string(head[60:68]) != "BOOKMOBI" {
		return ErrNotKF8
	}
	count := int(binary.BigEndian.Uint16(head[76:]))
	table := make([]byte, 8*count)
	if _, err := db.file.ReadAt(table, 78); err != nil || count == 0 {
		return ErrNotKF8
	}
	for i := range count {
		db.offsets = append(db.offsets, int64(binary.BigEndian.Uint32(table[8*i:])))
	}
	db.offsets = append(db.offsets, info.Size())
	if !slices.IsSorted(db.offsets) {
		return fmt.Errorf("%w: the record table is out of order", errCorrupt)
	}
	return nil
}

// count returns the number of records.
func (db *database) count() int {
	return len(db.offsets) - 1
}

// record returns the record number i.
func (db *database) record(i int) ([]byte, error) {
	if i < 0 || i >= db.count() {
		return nil, fmt.Errorf("%w: record %d does not exist", errCorrupt, i)
	}
	size := db.offsets[i+1] - db.offsets[i]
	if size > maxRecordBytes {
		return nil, fmt.Errorf("%w: record %d is too large", errCorrupt, i)
	}
	data := make([]byte, size)
	if _, err := db.file.ReadAt(data, db.offsets[i]); err != nil && err != io.EOF {
		return nil, err
	}
	return data, nil
}

// extended returns the value of the EXTH record of the given type in a
// header record as an integer, and whether it is there.
func extended(header []byte, kind int) (int, bool) {
	at := 16 + field(header, 20)
	if field(header, 128)&0x40 == 0 || at+12 > len(header) || string(header[at:at+4]) != "EXTH" {
		return 0, false
	}
	count := field(header, at+8)
	at += 12
	for range count {
		size := field(header, at+4)
		if size < 8 || at+size > len(header) {
			break
		}
		if field(header, at) == kind && size == 12 {
			return field(header, at+8), true
		}
		at += size
	}
	return 0, false
}

// trailing returns the size of the trailing entry at the end of record,
// which stores its own length backwards.
func trailing(record []byte) int {
	size, shift := 0, 0
	for i := len(record) - 1; i >= 0 && shift < 28; i-- {
		size |= int(record[i]&0x7F) << shift
		shift += 7
		if record[i]&0x80 != 0 {
			break
		}
	}
	return min(size, len(record))
}

// trim removes what follows the text in a text record, as announced by
// the extra data flags of the header.
func trim(record []byte, flags int) []byte {
	for bit := 1; bit < 16; bit++ {
		if flags&(1<<bit) != 0 {
			record = record[:len(record)-trailing(record)]
		}
	}
	if flags&multibyteFlag != 0 && len(record) > 0 {
		record = record[:max(len(record)-int(record[len(record)-1]&3)-1, 0)]
	}
	return record
}

// Unpacked is a KF8 book read from a file. Close it after use.
type Unpacked struct {
	// Files are the complete HTML documents in reading order.
	Files [][]byte
	// Flows are the stylesheets and drawings; kindle:flow:0001 is the
	// first.
	Flows [][]byte
	// Contents is the table of contents in reading order.
	Contents []Entry
	// Cover is the number of the cover among the resources, or -1.
	Cover int
	// Thumbnail is the number of the cover thumbnail among the
	// resources, or -1.
	Thumbnail int
	// db is the database the resources are read from.
	db *database
	// firstResource is the number of the first resource record.
	firstResource int
	// resources is the number of records that may hold resources.
	resources int
	// chunks locates every chunk in its file.
	chunks []Position
	// starts holds the offset of every file in the text.
	starts []int
}

// Close releases the file the book was read from.
func (u *Unpacked) Close() error {
	return u.db.file.Close()
}

// Resources returns how many resource records the book may have. Not
// all of them hold images or fonts.
func (u *Unpacked) Resources() int {
	return u.resources
}

// Resource returns the resource record number i; kindle:embed:0001 is
// number 0.
func (u *Unpacked) Resource(i int) ([]byte, error) {
	if i < 0 || i >= u.Resources() {
		return nil, fmt.Errorf("%w: resource %d does not exist", errCorrupt, i)
	}
	return u.db.record(u.firstResource + i)
}

// Position returns the place in the book of a kindle:pos address: the
// offset within the chunk with the given number.
func (u *Unpacked) Position(chunk, offset int) (Position, bool) {
	if chunk < 0 || chunk >= len(u.chunks) {
		return Position{}, false
	}
	at := u.chunks[chunk]
	return Position{File: at.File, Offset: at.Offset + offset}, true
}

// ParseAddress returns the number at the start of the base 32 field of
// a kindle: address, such as the 0001 of kindle:embed:0001?mime=x.
func ParseAddress(digits string) (int, bool) {
	end := 0
	for end < len(digits) && strings.IndexByte("0123456789ABCDEFGHIJKLMNOPQRSTUV", digits[end]) >= 0 {
		end++
	}
	value, err := strconv.ParseInt(digits[:end], 32, 32)
	return int(value), err == nil
}

// header is the part of a KF8 header needed for reading.
type header struct {
	// base is the number of the header record.
	base int
	// compression is the code of the text compression.
	compression int
	// textRecords is the number of text records.
	textRecords int
	// flags says what follows the text in every record.
	flags int
	// flowTable is the number of the FDST record, or -1.
	flowTable int
	// contents is the number of the contents index, or -1.
	contents int
	// chunks is the number of the chunk index.
	chunks int
	// skeletons is the number of the skeleton index.
	skeletons int
}

// pointer returns the record number stored at offset in a header record
// numbered base, or -1 when it is not set.
func pointer(record []byte, offset, base int) int {
	value := field(record, offset)
	if offset+4 > len(record) || value == null {
		return -1
	}
	return value + base
}

// readHeader finds and reads the KF8 header of the database: its first
// record, or the record a first record in the older format points at.
func (db *database) readHeader(u *Unpacked) (*header, error) {
	first, err := db.record(0)
	if err != nil || len(first) < 16+24 || string(first[16:20]) != "MOBI" {
		return nil, ErrNotKF8
	}
	record, base := first, 0
	if field(first, 36) != formatVersion {
		boundary, ok := extended(first, exthBoundary)
		if !ok || boundary <= 0 || boundary >= db.count() {
			return nil, ErrNotKF8
		}
		base = boundary
		if record, err = db.record(base); err != nil || len(record) < 40 || field(record, 36) != formatVersion {
			return nil, ErrNotKF8
		}
	}
	if len(record) < 16+mobiHeaderLength {
		return nil, fmt.Errorf("%w: the header is cut short", errCorrupt)
	}
	if binary.BigEndian.Uint16(record[12:]) != 0 {
		return nil, ErrProtected
	}
	h := &header{
		base:        base,
		compression: int(binary.BigEndian.Uint16(record)),
		textRecords: int(binary.BigEndian.Uint16(record[8:])),
		flowTable:   -1,
		contents:    pointer(record, 244, base),
		chunks:      pointer(record, 248, base),
		skeletons:   pointer(record, 252, base),
	}
	if field(record, 20) >= extraFlagsLength {
		h.flags = int(binary.BigEndian.Uint16(record[242:]))
	}
	if field(record, 196) > 1 {
		h.flowTable = pointer(record, 192, base)
	}
	u.firstResource = pointer(first, 108, 0)
	u.Cover, u.Thumbnail = -1, -1
	if cover, ok := extended(record, exthCoverOffset); ok {
		u.Cover = cover
	}
	if thumbnail, ok := extended(record, exthThumbOffset); ok {
		u.Thumbnail = thumbnail
	}
	u.resources = resourceCount(u.firstResource, base, pointer(record, 192, base), db.count())
	return h, nil
}

// resourceCount returns how many records, starting at first, may hold
// resources: those up to the KF8 part of a file that also carries the
// older format, or else up to the flow table.
func resourceCount(first, base, flowTable, records int) int {
	end := records
	switch {
	case first < 0:
		return 0
	case base > first:
		end = base
	case flowTable > first:
		end = flowTable
	}
	return end - first
}

// text returns the uncompressed text of the book.
func (db *database) text(h *header) ([]byte, error) {
	if h.compression != noCompression && h.compression != palmdocCompression {
		return nil, ErrCompression
	}
	var out []byte
	for i := 1; i <= h.textRecords; i++ {
		record, err := db.record(h.base + i)
		if err != nil {
			return nil, err
		}
		record = trim(record, h.flags)
		if h.compression == palmdocCompression {
			if record, err = decompress(record); err != nil {
				return nil, fmt.Errorf("text record %d: %w", i, err)
			}
		}
		out = append(out, record...)
	}
	return out, nil
}

// flows splits the text into its flows as listed by the FDST record.
func (db *database) flows(h *header, text []byte) ([][]byte, error) {
	if h.flowTable < 0 {
		return [][]byte{text}, nil
	}
	table, err := db.record(h.flowTable)
	if err != nil || len(table) < 12 || string(table[:4]) != "FDST" {
		return nil, fmt.Errorf("%w: the flow table is missing", errCorrupt)
	}
	var out [][]byte
	at := field(table, 4)
	for range field(table, 8) {
		start, end := field(table, at), field(table, at+4)
		if at+8 > len(table) || start > end || end > len(text) {
			return nil, fmt.Errorf("%w: a flow lies outside the text", errCorrupt)
		}
		out = append(out, text[start:end])
		at += 8
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: the flow table is empty", errCorrupt)
	}
	return out, nil
}

// files puts the chunks back into their skeletons and returns the
// complete documents.
func (u *Unpacked) files(html []byte, skeletons, chunks *readIndex) error {
	next := 0
	for i, skeleton := range skeletons.entries {
		start, length := skeleton.value(tagGeometry, 0), skeleton.value(tagGeometry, 1)
		if start < 0 || length < 0 || start+length > len(html) {
			return fmt.Errorf("%w: skeleton %d lies outside the text", errCorrupt, i)
		}
		doc := bytes.Clone(html[start : start+length])
		from := start + length
		for range skeleton.value(tagCount, 0) {
			if next >= len(chunks.entries) {
				return fmt.Errorf("%w: skeleton %d has more chunks than the book", errCorrupt, i)
			}
			chunk := chunks.entries[next]
			next++
			insert, err := strconv.Atoi(chunk.key)
			size := chunk.value(tagGeometry, 1)
			at := insert - start
			if err != nil || at < 0 || at > len(doc) || size < 0 || from+size > len(html) {
				return fmt.Errorf("%w: chunk %d is out of place", errCorrupt, next-1)
			}
			doc = slices.Insert(doc, at, html[from:from+size]...)
			u.chunks = append(u.chunks, Position{File: i, Offset: at})
			from += size
		}
		u.Files = append(u.Files, doc)
		u.starts = append(u.starts, start)
	}
	if len(u.Files) == 0 {
		return ErrEmpty
	}
	return nil
}

// locate returns the place in the book of an offset into the text.
func (u *Unpacked) locate(offset int) Position {
	file := 0
	for i, start := range u.starts {
		if start <= offset {
			file = i
		}
	}
	return Position{File: file, Offset: min(max(offset-u.starts[file], 0), len(u.Files[file]))}
}

// contents turns the contents index, whose lines are ordered by depth,
// into a table of contents in reading order.
func (u *Unpacked) contents(ix *readIndex) {
	const tagParent = 21
	children := map[int][]int{}
	var roots []int
	for i, entry := range ix.entries {
		if entry.has(tagParent) && entry.value(tagParent, 0) != i {
			children[entry.value(tagParent, 0)] = append(children[entry.value(tagParent, 0)], i)
		} else {
			roots = append(roots, i)
		}
	}
	seen := make([]bool, len(ix.entries))
	var walk func(i, level int)
	walk = func(i, level int) {
		if i >= len(seen) || seen[i] {
			return
		}
		seen[i] = true
		entry := ix.entries[i]
		target, ok := u.Position(entry.value(tagGeometry, 0), entry.value(tagGeometry, 1))
		if !entry.has(tagGeometry) || !ok {
			target = u.locate(entry.value(tagCount, 0))
		}
		u.Contents = append(u.Contents, Entry{Title: ix.strings[entry.value(tagLabel, 0)], Level: level, Target: target})
		for _, child := range children[i] {
			walk(child, level+1)
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
}

// Read reads the KF8 book in the file at path.
func Read(path string) (*Unpacked, error) {
	db, err := openDatabase(path)
	if err != nil {
		return nil, err
	}
	u := &Unpacked{db: db}
	if err := u.read(); err != nil {
		db.file.Close()
		return nil, err
	}
	return u, nil
}

// read fills the book from its database.
func (u *Unpacked) read() error {
	h, err := u.db.readHeader(u)
	if err != nil {
		return err
	}
	text, err := u.db.text(h)
	if err != nil {
		return err
	}
	flows, err := u.db.flows(h, text)
	if err != nil {
		return err
	}
	u.Flows = flows[1:]
	skeletons, err := readIndexAt(u.db.record, h.skeletons)
	if err != nil {
		return err
	}
	chunks, err := readIndexAt(u.db.record, h.chunks)
	if err != nil {
		return err
	}
	if err := u.files(flows[0], skeletons, chunks); err != nil {
		return err
	}
	if h.contents >= 0 {
		if ix, err := readIndexAt(u.db.record, h.contents); err == nil {
			u.contents(ix)
		}
	}
	return nil
}
