package azw3

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/manzanit0/athenaeum/internal/epub"
	"github.com/manzanit0/athenaeum/internal/kf8"
	"github.com/manzanit0/athenaeum/internal/meta"
)

// Addresses used inside KF8 markup.
var (
	// positionAddress matches a link to a place in the book.
	positionAddress = regexp.MustCompile(`kindle:pos:fid:([0-9A-V]{4}):off:([0-9A-V]{10})`)
	// embedAddress matches a reference to an image or font.
	embedAddress = regexp.MustCompile(`kindle:embed:([0-9A-V]{4})(\?mime=[\w/+.-]*)?`)
	// flowAddress matches a reference to a stylesheet or drawing.
	flowReference = regexp.MustCompile(`kindle:flow:([0-9A-V]{4})(\?mime=[\w/+.-]*)?`)
	// aidAttribute matches the Kindle identifier of an element.
	aidAttribute = regexp.MustCompile(`\s+aid=(?:"([^"]*)"|'([^']*)')`)
	// idAttribute matches the identifier of an element.
	idAttribute = regexp.MustCompile(`\s(?:id|name)=(?:"([^"]*)"|'([^']*)')`)
	// tagName matches the name at the start of a tag.
	tagName = regexp.MustCompile(`^<([A-Za-z][\w:.-]*)`)
)

// structural lists the elements that cannot be the target of a link.
var structural = map[string]bool{
	"html": true, "head": true, "meta": true, "title": true, "link": true, "style": true, "body": true,
}

// fontTypes maps the first bytes of a font to its extension and type.
var fontTypes = map[string][2]string{
	"\x00\x01\x00\x00": {"ttf", "font/ttf"},
	"true":             {"ttf", "font/ttf"},
	"ttcf":             {"ttc", "font/collection"},
	"OTTO":             {"otf", "font/otf"},
}

// unpacker holds the state of turning one KF8 book into an EPUB.
type unpacker struct {
	// book is the KF8 book being read.
	book *kf8.Unpacked
	// out is the EPUB being written.
	out *epub.Writer
	// resources maps resource numbers to the names of their files.
	resources map[int]string
	// flows maps flow numbers to the names of their files.
	flows map[int]string
	// targets holds, for every document, the offsets links lead to.
	targets []map[int]bool
	// anchors holds, for every document, the identifier found or made
	// for each of those offsets; empty means the document itself.
	anchors []map[int]string
	// images counts the images written.
	images int
	// warnings collects what could not be kept.
	warnings []string
}

// documentName returns the file name of the document with index i.
func documentName(i int) string {
	return fmt.Sprintf("part%04d.xhtml", i)
}

// font returns the font held by a FONT resource record, which may be
// compressed and have its start scrambled.
func font(record []byte) ([]byte, bool) {
	if len(record) < 24 {
		return nil, false
	}
	flags := binary.BigEndian.Uint32(record[8:])
	start := int(binary.BigEndian.Uint32(record[12:]))
	keyLength, keyStart := int(binary.BigEndian.Uint32(record[16:])), int(binary.BigEndian.Uint32(record[20:]))
	if start > len(record) || keyStart+keyLength > len(record) {
		return nil, false
	}
	data := bytes.Clone(record[start:])
	if flags&2 != 0 && keyLength > 0 {
		key := record[keyStart : keyStart+keyLength]
		for i := range min(len(data), 1040) {
			data[i] ^= key[i%keyLength]
		}
	}
	if flags&1 != 0 {
		zr, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, false
		}
		if data, err = io.ReadAll(zr); err != nil {
			return nil, false
		}
	}
	return data, true
}

// addResources writes the images and fonts of the book into the EPUB.
func (u *unpacker) addResources(ctx context.Context) error {
	for i := range u.book.Resources() {
		if err := ctx.Err(); err != nil {
			return err
		}
		record, err := u.book.Resource(i)
		if err != nil || i == u.book.Thumbnail {
			continue
		}
		if ext := meta.ImageExt(record); ext != "" && imageTypes[ext] != "" {
			u.images++
			u.resources[i] = fmt.Sprintf("images/image%04d.%s", i+1, ext)
			u.out.Add(u.resources[i], imageTypes[ext], record)
			continue
		}
		if !bytes.HasPrefix(record, []byte("FONT")) {
			continue
		}
		data, ok := font(record)
		kind, known := fontTypes[string(data[:min(len(data), 4)])]
		if !ok || !known {
			u.warnings = append(u.warnings, fmt.Sprintf("font %d could not be read and was left out", i+1))
			continue
		}
		u.resources[i] = fmt.Sprintf("fonts/font%04d.%s", i+1, kind[0])
		u.out.Add(u.resources[i], kind[1], data)
	}
	return nil
}

// replaceAddresses points the kindle:embed and kindle:flow references in
// data at the files of the EPUB, which sit in sibling folders.
func (u *unpacker) replaceAddresses(data []byte) []byte {
	swap := func(pattern *regexp.Regexp, names map[int]string) {
		data = pattern.ReplaceAllFunc(data, func(match []byte) []byte {
			number, _ := kf8.ParseAddress(string(pattern.FindSubmatch(match)[1]))
			if name, ok := names[number-1]; ok {
				return []byte("../" + name)
			}
			return []byte("#")
		})
	}
	swap(embedAddress, u.resources)
	swap(flowReference, u.flows)
	return data
}

// addFlows writes the stylesheets and drawings of the book.
func (u *unpacker) addFlows() {
	for i, flow := range u.book.Flows {
		if bytes.Contains(flow[:min(len(flow), 512)], []byte("<svg")) {
			u.flows[i] = fmt.Sprintf("images/drawing%04d.svg", i+1)
			u.out.Add(u.flows[i], "image/svg+xml", u.replaceAddresses(flow))
			continue
		}
		u.flows[i] = fmt.Sprintf("styles/style%04d.css", i+1)
	}
	for i, flow := range u.book.Flows {
		if name := u.flows[i]; filepath.Ext(name) == ".css" {
			u.out.Add(name, "text/css", u.replaceAddresses(flow))
		}
	}
}

// position returns the place a kindle:pos address leads to.
func (u *unpacker) position(match [][]byte) (kf8.Position, bool) {
	chunk, _ := kf8.ParseAddress(string(match[1]))
	offset, _ := kf8.ParseAddress(string(match[2]))
	at, ok := u.book.Position(chunk, offset)
	return at, ok && at.File < len(u.targets)
}

// collectTargets notes every place a link or a line of the table of
// contents leads to.
func (u *unpacker) collectTargets() {
	for range u.book.Files {
		u.targets = append(u.targets, map[int]bool{})
		u.anchors = append(u.anchors, map[int]string{})
	}
	for _, doc := range u.book.Files {
		for _, match := range positionAddress.FindAllSubmatch(doc, -1) {
			if at, ok := u.position(match); ok {
				u.targets[at.File][at.Offset] = true
			}
		}
	}
	for _, entry := range u.book.Contents {
		if entry.Target.File < len(u.targets) {
			u.targets[entry.Target.File][entry.Target.Offset] = true
		}
	}
}

// tagEnd returns the offset just past the tag starting at start, or -1.
// A greater-than sign inside a quoted attribute value does not end it.
func tagEnd(doc []byte, start int) int {
	var quote byte
	for i := start + 1; i < len(doc); i++ {
		switch c := doc[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i + 1
		}
	}
	return -1
}

// declarations lists how comments, character data sections, processing
// instructions and other declarations start and end, most specific
// first.
var declarations = [][2]string{{"<!--", "-->"}, {"<![CDATA[", "]]>"}, {"<?", "?>"}, {"<!", ">"}}

// skip returns the offset just past the comment, instruction or other
// markup declaration starting at start, or -1 when it is a tag.
func skip(doc []byte, start int) int {
	for _, d := range declarations {
		if !bytes.HasPrefix(doc[start:], []byte(d[0])) {
			continue
		}
		if end := bytes.Index(doc[start:], []byte(d[1])); end >= 0 {
			return start + end + len(d[1])
		}
		return len(doc)
	}
	return -1
}

// identify returns the tag with its Kindle identifier removed and the
// identifier links can use to reach it. When the tag has none and
// create is set, an identifier is added to it.
func identify(tag []byte, wanted, create bool, offset int) ([]byte, string) {
	aid := ""
	if match := aidAttribute.FindSubmatch(tag); match != nil {
		aid = string(match[1]) + string(match[2])
		tag = aidAttribute.ReplaceAll(tag, nil)
	}
	if !wanted {
		return tag, ""
	}
	if match := idAttribute.FindSubmatch(tag); match != nil {
		return tag, string(match[1]) + string(match[2])
	}
	if !create {
		return tag, ""
	}
	id := fmt.Sprintf("kp%d", offset)
	if aid != "" {
		id = "kx" + aid
	}
	name := tagName.Find(tag)
	return append(append(bytes.Clone(name), []byte(` id="`+id+`"`)...), tag[len(name):]...), id
}

// anchoring walks a document, rewriting its tags and finding or making
// the identifiers of the elements links lead to.
type anchoring struct {
	// doc is the document.
	doc []byte
	// out collects the rewritten document.
	out bytes.Buffer
	// written is how much of the document has been copied to out.
	written int
	// pending holds the offsets still to be matched to an element, in
	// ascending order.
	pending []int
	// found maps the matched offsets to identifiers.
	found map[int]string
	// leading reports whether nothing but opening tags has been seen in
	// the body so far, so an element here stands for the document.
	leading bool
}

// tag handles the start tag spanning the document from at to end.
func (a *anchoring) tag(name string, at, end int) {
	if name == "body" {
		a.leading = true
	}
	content := !structural[name]
	wanted := content && len(a.pending) > 0 && a.pending[0] <= at
	tag, id := identify(a.doc[at:end], wanted, !a.leading, at)
	for wanted && len(a.pending) > 0 && a.pending[0] <= at {
		a.found[a.pending[0]] = id
		a.pending = a.pending[1:]
	}
	if content && (voidElements[name] || bytes.HasSuffix(tag, []byte("/>"))) {
		a.leading = false
	}
	a.out.Write(a.doc[a.written:at])
	a.out.Write(tag)
	a.written = end
}

// run walks the document and returns it rewritten.
func (a *anchoring) run() []byte {
	doc := a.doc
	previous := 0
	for at := bytes.IndexByte(doc, '<'); at >= 0 && at < len(doc); {
		if len(bytes.TrimSpace(doc[previous:at])) > 0 {
			a.leading = false
		}
		end := skip(doc, at)
		name := tagName.FindSubmatch(doc[at:])
		switch {
		case end >= 0:
		case name == nil:
			a.leading = false
			end = at + 1
		default:
			if end = tagEnd(doc, at); end < 0 {
				end = len(doc)
				break
			}
			a.tag(string(name[1]), at, end)
		}
		previous = end
		next := bytes.IndexByte(doc[min(end, len(doc)):], '<')
		if next < 0 {
			break
		}
		at = end + next
	}
	a.out.Write(doc[a.written:])
	return a.out.Bytes()
}

// anchor rewrites the document with index i without its Kindle
// identifiers, and finds or makes the identifiers its targets need. A
// target is reached through the first element that starts at or after
// it; an element at the very start of the body stands for the document
// itself unless it has an identifier of its own.
func (u *unpacker) anchor(i int) []byte {
	a := &anchoring{doc: u.book.Files[i], found: u.anchors[i]}
	for offset := range u.targets[i] {
		a.pending = append(a.pending, offset)
	}
	sort.Ints(a.pending)
	return a.run()
}

// href returns the address of a place in the book as seen from the
// document with index from.
func (u *unpacker) href(at kf8.Position, from int) string {
	id := u.anchors[at.File][at.Offset]
	switch {
	case id == "":
		return documentName(at.File)
	case at.File == from:
		return "#" + id
	}
	return documentName(at.File) + "#" + id
}

// addDocuments writes the documents of the book with their links
// restored.
func (u *unpacker) addDocuments(ctx context.Context) error {
	docs := make([][]byte, len(u.book.Files))
	for i := range u.book.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		docs[i] = u.anchor(i)
	}
	for i, doc := range docs {
		doc = positionAddress.ReplaceAllFunc(doc, func(match []byte) []byte {
			if at, ok := u.position(positionAddress.FindSubmatch(match)); ok {
				return []byte(u.href(at, i))
			}
			return []byte("#")
		})
		doc = u.replaceAddresses(doc)
		if !wellFormedXML(doc) {
			u.warnings = append(u.warnings, fmt.Sprintf("part %d is not well formed and may not display", i+1))
		}
		u.out.AddDocument("text/"+documentName(i), doc)
	}
	return nil
}

// wellFormedXML reports whether doc parses as XML.
func wellFormedXML(doc []byte) bool {
	d := xml.NewDecoder(bytes.NewReader(doc))
	d.Entity = xml.HTMLEntity
	for {
		if _, err := d.Token(); err != nil {
			return err == io.EOF
		}
	}
}

// contents returns the table of contents of the EPUB.
func (u *unpacker) contents() []epub.Entry {
	var out []epub.Entry
	for _, entry := range u.book.Contents {
		if entry.Target.File >= len(u.anchors) || entry.Title == "" {
			continue
		}
		href := "text/" + documentName(entry.Target.File)
		if id := u.anchors[entry.Target.File][entry.Target.Offset]; id != "" {
			href += "#" + id
		}
		out = append(out, epub.Entry{Title: entry.Title, Href: href, Level: entry.Level})
	}
	return out
}

// cover returns the name of the cover image of the EPUB: the image at
// the given path, added to the book, or else the cover of the KF8 book.
func (u *unpacker) cover(path string) string {
	if data, err := os.ReadFile(path); err == nil && path != "" {
		if ext := meta.ImageExt(data); imageTypes[ext] != "" {
			u.out.Add("images/cover."+ext, imageTypes[ext], data)
			return "images/cover." + ext
		}
	}
	return u.resources[u.book.Cover]
}

// Unpack writes the AZW3 book at src as an EPUB at dst.
func Unpack(ctx context.Context, src, dst string, opts Options) (*Report, error) {
	book, err := kf8.Read(src)
	if err != nil {
		return nil, err
	}
	defer book.Close()
	file, err := os.Create(dst)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	u := &unpacker{
		book: book, out: epub.NewWriter(file),
		resources: map[int]string{}, flows: map[int]string{}, warnings: []string{},
	}
	if err := u.addResources(ctx); err != nil {
		return nil, err
	}
	u.addFlows()
	u.collectTargets()
	if err := u.addDocuments(ctx); err != nil {
		return nil, err
	}
	details, _ := merged(src, opts)
	id := opts.ID
	if id == "" {
		id = fallbackID(details)
	}
	err = u.out.Close(epub.Details{Metadata: details, ID: id, Contents: u.contents(), Cover: u.cover(opts.Cover)})
	if err != nil {
		return nil, err
	}
	return &Report{Files: len(book.Files), Images: u.images, Warnings: u.warnings}, file.Close()
}
