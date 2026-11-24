package meta

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const (
	// pdbHeaderSize is the size of the Palm Database header.
	pdbHeaderSize = 78
	// maxRecord0Bytes bounds how much of the first record is read.
	maxRecord0Bytes = 4 << 20
	// mobiHeaderOffset is where the MOBI header starts within record 0.
	mobiHeaderOffset = 16
	// mobiMinRecord0 is the smallest record 0 holding the fields read here.
	mobiMinRecord0 = 132
	// exthFlag marks the presence of an EXTH header in the MOBI flags.
	exthFlag = 0x40
	// mobiUTF8 is the MOBI text encoding identifier for UTF-8.
	mobiUTF8 = 65001
	// noCover is the EXTH cover offset meaning the book has no cover.
	noCover = 0xFFFFFFFF
)

// EXTH record types read from MOBI files.
const (
	// exthAuthor holds an author name.
	exthAuthor = 100
	// exthPublisher holds the publisher name.
	exthPublisher = 101
	// exthDescription holds the book description.
	exthDescription = 103
	// exthISBN holds the ISBN.
	exthISBN = 104
	// exthSubject holds a subject.
	exthSubject = 105
	// exthPublished holds the publication date.
	exthPublished = 106
	// exthASIN holds the identifier of the book on a Kindle.
	exthASIN = 113
	// exthCoverOffset holds the cover image index relative to the first
	// image record.
	exthCoverOffset = 201
	// exthDocType holds the kind of document: EBOK or PDOC.
	exthDocType = 501
	// exthUpdatedTitle holds a title overriding the full name.
	exthUpdatedTitle = 503
	// exthLanguage holds the language code.
	exthLanguage = 524
)

// mobiFile gives access to the records of a Palm Database file.
type mobiFile struct {
	// f is the open file.
	f *os.File
	// size is the file size in bytes.
	size int64
	// name is the database name from the Palm Database header.
	name string
	// kind is the eight byte type and creator identifier.
	kind string
	// offsets holds the start offset of each record.
	offsets []uint32
}

// openMOBI opens path and reads its Palm Database record table.
func openMOBI(path string) (*mobiFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	m, err := readRecordTable(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return m, nil
}

// readRecordTable parses the Palm Database header and record list of f.
func readRecordTable(f *os.File) (*mobiFile, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	var header [pdbHeaderSize]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return nil, err
	}
	count := int(binary.BigEndian.Uint16(header[76:78]))
	if count == 0 {
		return nil, errors.New("mobi: no records")
	}
	table := make([]byte, count*8)
	if _, err := io.ReadFull(f, table); err != nil {
		return nil, err
	}
	offsets := make([]uint32, count)
	for i := range offsets {
		offsets[i] = binary.BigEndian.Uint32(table[i*8:])
	}
	return &mobiFile{
		f:       f,
		size:    info.Size(),
		name:    strings.TrimRight(string(header[:32]), "\x00"),
		kind:    string(header[60:68]),
		offsets: offsets,
	}, nil
}

// record reads record i, reading at most limit bytes of it.
func (m *mobiFile) record(i int, limit int64) ([]byte, error) {
	if i < 0 || i >= len(m.offsets) {
		return nil, errors.New("mobi: record out of range")
	}
	start := int64(m.offsets[i])
	end := m.size
	if i+1 < len(m.offsets) {
		end = int64(m.offsets[i+1])
	}
	if start > end || end > m.size {
		return nil, errors.New("mobi: corrupt record table")
	}
	buf := make([]byte, min(end-start, limit))
	if _, err := m.f.ReadAt(buf, start); err != nil {
		return nil, err
	}
	return buf, nil
}

// readMOBI extracts metadata from the MOBI or AZW file at path, and its
// cover when withCover is set.
func readMOBI(path string, withCover bool) (Metadata, *Cover, error) {
	file, err := openMOBI(path)
	if err != nil {
		return Metadata{}, nil, err
	}
	defer file.f.Close()
	meta := Metadata{Title: file.name}
	if file.kind != "BOOKMOBI" {
		return meta, nil, nil
	}
	rec0, err := file.record(0, maxRecord0Bytes)
	if err != nil {
		return Metadata{}, nil, err
	}
	if len(rec0) < mobiMinRecord0 || string(rec0[16:20]) != "MOBI" {
		return meta, nil, nil
	}
	be := binary.BigEndian
	decode := decodeCP1252
	if be.Uint32(rec0[28:32]) == mobiUTF8 {
		decode = decodeUTF8
	}
	if title := slice(rec0, be.Uint32(rec0[84:88]), be.Uint32(rec0[88:92])); len(title) > 0 {
		meta.Title = decode(title)
	}
	coverOffset := uint32(noCover)
	if be.Uint32(rec0[128:132])&exthFlag != 0 {
		exth := slice(rec0, mobiHeaderOffset+be.Uint32(rec0[20:24]), uint32(len(rec0)))
		coverOffset = applyEXTH(&meta, exth, decode)
	}
	var cover *Cover
	if withCover && coverOffset != noCover {
		cover = file.cover(int(be.Uint32(rec0[108:112])) + int(coverOffset))
	}
	return meta, cover, nil
}

// KindleID returns the identifier (ASIN) and the document type (EBOK or
// PDOC) the MOBI or AZW3 file at path declares, which a Kindle uses to
// name the files it keeps for the book. Both are empty when the file
// declares no identifier.
func KindleID(path string) (asin, kind string) {
	file, err := openMOBI(path)
	if err != nil {
		return "", ""
	}
	defer file.f.Close()
	rec0, err := file.record(0, maxRecord0Bytes)
	if err != nil || file.kind != "BOOKMOBI" || len(rec0) < mobiMinRecord0 || string(rec0[16:20]) != "MOBI" {
		return "", ""
	}
	be := binary.BigEndian
	if be.Uint32(rec0[128:132])&exthFlag == 0 {
		return "", ""
	}
	exth := slice(rec0, mobiHeaderOffset+be.Uint32(rec0[20:24]), uint32(len(rec0)))
	if len(exth) < 12 || string(exth[:4]) != "EXTH" {
		return "", ""
	}
	count := be.Uint32(exth[8:12])
	pos := 12
	for i := uint32(0); i < count && pos+8 <= len(exth); i++ {
		recordKind := be.Uint32(exth[pos:])
		size := int(be.Uint32(exth[pos+4:]))
		if size < 8 || pos+size > len(exth) {
			break
		}
		data := string(exth[pos+8 : pos+size])
		pos += size
		switch recordKind {
		case exthASIN:
			asin = strings.TrimSpace(decodeUTF8([]byte(data)))
		case exthDocType:
			kind = strings.TrimSpace(decodeUTF8([]byte(data)))
		}
	}
	return asin, kind
}

// slice returns up to length bytes of b starting at offset, or nil when
// offset is out of range.
func slice(b []byte, offset, length uint32) []byte {
	if int64(offset) >= int64(len(b)) {
		return nil
	}
	end := min(int64(offset)+int64(length), int64(len(b)))
	return b[offset:end]
}

// applyEXTH copies the EXTH records in exth into meta and returns the cover
// offset, or noCover when there is none.
func applyEXTH(meta *Metadata, exth []byte, decode func([]byte) string) uint32 {
	cover := uint32(noCover)
	if len(exth) < 12 || string(exth[:4]) != "EXTH" {
		return cover
	}
	count := binary.BigEndian.Uint32(exth[8:12])
	pos := 12
	for i := uint32(0); i < count && pos+8 <= len(exth); i++ {
		kind := binary.BigEndian.Uint32(exth[pos:])
		size := int(binary.BigEndian.Uint32(exth[pos+4:]))
		if size < 8 || pos+size > len(exth) {
			break
		}
		data := exth[pos+8 : pos+size]
		pos += size
		if kind == exthCoverOffset && len(data) >= 4 {
			cover = binary.BigEndian.Uint32(data)
			continue
		}
		applyEXTHText(meta, kind, decode(data))
	}
	return cover
}

// applyEXTHText stores the text value of an EXTH record of type kind.
func applyEXTHText(meta *Metadata, kind uint32, value string) {
	switch kind {
	case exthAuthor:
		meta.Authors = append(meta.Authors, splitAuthors(value)...)
	case exthPublisher:
		meta.Publisher = value
	case exthDescription:
		meta.Description = stripTags(value)
	case exthISBN:
		meta.ISBN = value
	case exthSubject:
		meta.Tags = append(meta.Tags, value)
	case exthPublished:
		meta.Published = value
	case exthUpdatedTitle:
		meta.Title = value
	case exthLanguage:
		meta.Language = value
	}
}

// cover reads record i as a cover image, returning nil when it is not one.
func (m *mobiFile) cover(i int) *Cover {
	data, err := m.record(i, maxCoverBytes)
	if err != nil {
		return nil
	}
	ext := ImageExt(data)
	if ext == "" {
		return nil
	}
	return &Cover{Data: data, Ext: ext}
}

// decodeUTF8 converts possibly invalid UTF-8 bytes to a string.
func decodeUTF8(b []byte) string {
	return strings.ToValidUTF8(string(b), "")
}

// cp1252High maps the bytes 0x80 to 0x9F of Windows-1252 to Unicode.
var cp1252High = [32]rune{
	0x20AC, 0x81, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x8D, 0x017D, 0x8F,
	0x90, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x9D, 0x017E, 0x0178,
}

// decodeCP1252 converts Windows-1252 bytes to a string.
func decodeCP1252(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for _, c := range b {
		switch {
		case c < utf8.RuneSelf:
			sb.WriteByte(c)
		case c >= 0x80 && c <= 0x9F:
			sb.WriteRune(cp1252High[c-0x80])
		default:
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}
