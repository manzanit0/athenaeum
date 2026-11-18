package kf8

import (
	"strings"
)

// Header layout.
const (
	// mobiHeaderLength is the length of the MOBI header of a KF8 book.
	mobiHeaderLength = 264
	// headerPadding is the spare room left after the header, as calibre
	// and KindleGen leave it.
	headerPadding = 8192
	// exthFlags says the header is followed by an EXTH block.
	exthFlags = 0x50
	// multibyteFlag says text records end with character overlap bytes.
	multibyteFlag = 1
	// indexingFlag says text records end with an indexing entry.
	indexingFlag = 2
	// bookType marks the file as a book rather than a periodical.
	bookType = 2
	// utf8Encoding is the code page number of UTF-8.
	utf8Encoding = 65001
	// formatVersion is the version number of the KF8 format.
	formatVersion = 8
	// palmdocCompression is the code of PalmDOC compression.
	palmdocCompression = 2
)

// EXTH record types.
const (
	// exthAuthor names an author.
	exthAuthor = 100
	// exthPublisher names the publisher.
	exthPublisher = 101
	// exthDescription holds the summary.
	exthDescription = 103
	// exthISBN holds the ISBN.
	exthISBN = 104
	// exthSubject names a subject.
	exthSubject = 105
	// exthPublished holds the publication date.
	exthPublished = 106
	// exthSource identifies the book the file was made from.
	exthSource = 112
	// exthASIN identifies the book on the device.
	exthASIN = 113
	// exthResourceCount holds the number of resource records.
	exthResourceCount = 125
	// exthCoverURI holds the kindle:embed address of the cover.
	exthCoverURI = 129
	// exthUnknownCount holds a counter KindleGen always writes as zero.
	exthUnknownCount = 131
	// exthCoverOffset holds the index of the cover among the resources.
	exthCoverOffset = 201
	// exthThumbOffset holds the index of the thumbnail.
	exthThumbOffset = 202
	// exthFakeCover says whether the cover is a generated one.
	exthFakeCover = 203
	// exthDocType says what kind of document the file is.
	exthDocType = 501
	// exthUpdatedTitle holds the full title.
	exthUpdatedTitle = 503
	// exthLanguage holds the language code.
	exthLanguage = 524
	// exthCreatorSoftware identifies the program that made the file.
	exthCreatorSoftware = 204
	// exthCreatorMajor holds its major version.
	exthCreatorMajor = 205
	// exthCreatorMinor holds its minor version.
	exthCreatorMinor = 206
	// exthCreatorBuild holds its build number.
	exthCreatorBuild = 207
	// exthCreatorRevision holds its build revision.
	exthCreatorRevision = 535
)

// docType marks the file as an ebook.
const docType = "EBOK"

// The generator the file claims: KindleGen 2.9, as calibre does, so the
// Kindle treats the book like any other KF8 file.
const (
	// creatorSoftware is the code of KindleGen on Linux.
	creatorSoftware = 201
	// creatorMajor is the major version of KindleGen.
	creatorMajor = 2
	// creatorMinor is the minor version of KindleGen.
	creatorMinor = 9
	// creatorRevision is the build revision of KindleGen 2.9.
	creatorRevision = "0730-890adc2"
)

// locales maps language codes to the language numbers of the header.
var locales = map[string]int{
	"ar": 1, "bg": 2, "ca": 3, "zh": 4, "cs": 5, "da": 6, "de": 7, "el": 8, "en": 9, "es": 10,
	"fi": 11, "fr": 12, "he": 13, "hu": 14, "it": 16, "ja": 17, "ko": 18, "nl": 19, "no": 20,
	"pl": 21, "pt": 22, "ro": 24, "ru": 25, "sv": 29, "tr": 31, "uk": 34,
}

// longCodes maps three letter language codes to their two letter form.
var longCodes = map[string]string{
	"ara": "ar", "bul": "bg", "cat": "ca", "zho": "zh", "chi": "zh", "ces": "cs", "cze": "cs",
	"dan": "da", "deu": "de", "ger": "de", "ell": "el", "gre": "el", "eng": "en", "spa": "es",
	"fin": "fi", "fra": "fr", "fre": "fr", "heb": "he", "hun": "hu", "ita": "it", "jpn": "ja",
	"kor": "ko", "nld": "nl", "dut": "nl", "nor": "no", "pol": "pl", "por": "pt", "ron": "ro",
	"rum": "ro", "rus": "ru", "swe": "sv", "tur": "tr", "ukr": "uk",
}

// language returns the two letter form of the language of the book, or
// the code as given when it is not a known one.
func (b *Book) language() string {
	code, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(b.Language)), "-")
	code, _, _ = strings.Cut(code, "_")
	if short, ok := longCodes[code]; ok {
		return short
	}
	return code
}

// exth collects the records of an EXTH block.
type exth struct {
	// records holds the encoded records.
	records buffer
	// count is the number of records.
	count int
}

// text adds a record for every non-empty value.
func (e *exth) text(kind int, values ...string) {
	for _, value := range values {
		if value == "" {
			continue
		}
		e.records.u32(kind, 8+len(value))
		e.records.WriteString(value)
		e.count++
	}
}

// number adds a record holding one integer.
func (e *exth) number(kind, value int) {
	e.records.u32(kind, 12, value)
	e.count++
}

// bytes returns the finished block, padded as KindleGen pads it.
func (e *exth) bytes() []byte {
	var b buffer
	b.WriteString("EXTH")
	b.u32(12+e.records.Len(), e.count)
	b.Write(e.records.Bytes())
	b.fill(0, 4-e.records.Len()%4)
	return b.Bytes()
}

// metadata returns the EXTH block describing the book.
func (b *Book) metadata() []byte {
	var e exth
	e.text(exthAuthor, b.Authors...)
	e.text(exthPublisher, b.Publisher)
	e.text(exthDescription, b.Description)
	e.text(exthISBN, b.ISBN)
	e.text(exthSubject, b.Subjects...)
	e.text(exthPublished, b.Published)
	e.text(exthSource, b.ID)
	e.text(exthASIN, b.ID)
	e.text(exthDocType, docType)
	e.text(exthUpdatedTitle, b.Title)
	e.text(exthLanguage, b.language())
	if len(b.Resources) > 0 {
		e.number(exthResourceCount, len(b.Resources))
		e.number(exthUnknownCount, 0)
	}
	if b.Cover >= 0 && b.Cover < len(b.Resources) {
		e.number(exthCoverOffset, b.Cover)
		e.number(exthFakeCover, 0)
		e.text(exthCoverURI, "kindle:embed:"+Base32(b.Cover+1, 4))
	}
	if b.Thumbnail >= 0 && b.Thumbnail < len(b.Resources) {
		e.number(exthThumbOffset, b.Thumbnail)
	}
	e.number(exthCreatorSoftware, creatorSoftware)
	e.number(exthCreatorMajor, creatorMajor)
	e.number(exthCreatorMinor, creatorMinor)
	e.number(exthCreatorBuild, 0)
	e.text(exthCreatorRevision, creatorRevision)
	return e.bytes()
}

// header returns the first record of the book: the PalmDOC header, the
// MOBI header with the KF8 fields, the EXTH block and the full title.
func (b *Book) header(l layout) []byte {
	meta := b.metadata()
	title := b.Title
	if title == "" {
		title = "Unknown"
	}
	var h buffer
	h.u16(palmdocCompression, 0)
	h.u32(l.textLength)
	h.u16(l.textRecords, recordSize, 0, 0)
	h.WriteString("MOBI")
	h.u32(mobiHeaderLength, bookType, utf8Encoding, b.uid(), formatVersion, null, null)
	h.fill(0xFF, 32)
	h.u32(l.firstNonText, 16+mobiHeaderLength+len(meta), len(title), locales[b.language()], 0, 0)
	h.u32(formatVersion, l.firstResource, 0, 0, 0, 0, exthFlags)
	h.fill(0, 32)
	h.u32(null, null, 0, 0, 0)
	h.fill(0, 8)
	h.u32(l.flowTable, l.flowCount, l.fcis, 1, l.flis, 1)
	h.fill(0, 8)
	flags := multibyteFlag
	if l.indexed {
		flags |= indexingFlag
	}
	h.u32(null, 0, null, null, flags)
	h.u32(l.contents, l.chunks, l.skeletons, null, null)
	h.u32(null, 0, null, 0)
	h.Write(meta)
	h.WriteString(title)
	h.align()
	h.fill(0, headerPadding)
	return h.Bytes()
}
