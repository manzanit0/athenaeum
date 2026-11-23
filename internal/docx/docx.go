// Package docx turns Word documents into EPUB books. It reads the
// text with its headings, lists, tables, pictures, links and notes, and
// leaves the page layout behind: a book has no pages to keep.
package docx

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/manzanit0/athenaeum/internal/epub"
	"github.com/manzanit0/athenaeum/internal/meta"
)

// Limits on what is read from a document, so that a damaged or hostile
// file cannot take the memory of the machine.
const (
	// maxPartBytes bounds the size of one XML part once unpacked.
	maxPartBytes = 256 << 20
	// maxImageBytes bounds the size of one picture.
	maxImageBytes = 32 << 20
)

// The kinds of things a conversion leaves out, which the report counts.
const (
	// noteTextBox is text in a box placed on the page.
	noteTextBox = "textbox"
	// noteObject is a document of another program embedded in this one.
	noteObject = "object"
	// noteEquation is a formula.
	noteEquation = "equation"
	// noteImage is a picture in a format a book cannot show.
	noteImage = "image"
)

// The ends of the relationship types of the parts a document is read
// from. The beginnings differ between the two forms of the format.
const (
	// documentType ends the type of the main part.
	documentType = "/officeDocument"
	// stylesType ends the type of the styles.
	stylesType = "/styles"
	// numberingType ends the type of the list definitions.
	numberingType = "/numbering"
	// footnotesType ends the type of the footnotes.
	footnotesType = "/footnotes"
	// endnotesType ends the type of the endnotes.
	endnotesType = "/endnotes"
)

// The prefixes that tell the footnotes of a document from its endnotes,
// which number themselves separately.
const (
	// footnoteKey starts the key of a footnote.
	footnoteKey = "f:"
	// endnoteKey starts the key of an endnote.
	endnoteKey = "e:"
)

// ErrNotDocument is returned for a file that is not a Word document.
var ErrNotDocument = errors.New("the file is not a Word document")

// Options controls a conversion.
type Options struct {
	// Metadata overrides what the document declares. Zero fields fall
	// back to the properties of the document, then to its file name.
	Metadata meta.Metadata
	// Cover is the path of a cover image to embed; empty means none.
	Cover string
	// ID identifies the book; empty derives one from its details.
	ID string
}

// Report says what a conversion did and what it had to leave out.
type Report struct {
	// Sections is the number of sections the book is divided into.
	Sections int `json:"sections"`
	// Images is the number of pictures placed in the book.
	Images int `json:"images"`
	// Warnings lists what could not be converted.
	Warnings []string `json:"warnings"`
}

// relationship is a reference from a part of a document to another
// part or to an address outside it.
type relationship struct {
	// kind is the type of the reference.
	kind string
	// target is the path of the part inside the document, or the
	// address outside it.
	target string
	// external reports whether the target is outside the document.
	external bool
}

// pack is a Word document opened for reading.
type pack struct {
	// zr is the ZIP file the document is.
	zr *zip.ReadCloser
}

// open returns the part called name, bounded to limit bytes.
func (p *pack) open(name string, limit int64) (io.ReadCloser, error) {
	f, err := p.zr.Open(name)
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(f, limit), f}, nil
}

// root reads the part called name whole. A missing part gives nil.
func (p *pack) root(name string, notes map[string]int) *node {
	if name == "" {
		return nil
	}
	f, err := p.open(name, maxPartBytes)
	if err != nil {
		return nil
	}
	defer f.Close()
	n, err := readRoot(f, notes)
	if err != nil {
		return nil
	}
	return n
}

// relationships returns the references of the part called name by
// identifier. The references of the document as a whole are those of
// the part with an empty name.
func (p *pack) relationships(name string) map[string]relationship {
	out := map[string]relationship{}
	dir, base := path.Split(name)
	root := p.root(dir+"_rels/"+base+".rels", map[string]int{})
	for _, n := range root.childrenOf() {
		target := n.attr("Target")
		external := strings.EqualFold(n.attr("TargetMode"), "External")
		if !external {
			target = strings.TrimPrefix(path.Join("/", dir, target), "/")
			if strings.HasPrefix(n.attr("Target"), "/") {
				target = strings.TrimPrefix(path.Clean(n.attr("Target")), "/")
			}
		}
		out[n.attr("Id")] = relationship{kind: n.attr("Type"), target: target, external: external}
	}
	return out
}

// partOf returns the path of the part that rels refers to with a type
// ending in suffix, or an empty string.
func partOf(rels map[string]relationship, suffix string) string {
	for _, rel := range rels {
		if !rel.external && strings.HasSuffix(rel.kind, suffix) {
			return rel.target
		}
	}
	return ""
}

// conversion holds the state of one conversion.
type conversion struct {
	// pack is the document being read.
	pack *pack
	// rels holds the references of the main part.
	rels map[string]relationship
	// out receives the files of the book.
	out *epub.Writer
	// notes counts what was left out, by kind.
	notes map[string]int
	// images maps the identifiers of picture references to the names
	// of the files written for them; pictures that cannot be shown map
	// to an empty string.
	images map[string]string
	// footnotes holds the blocks of every note by key.
	footnotes map[string][]block
	// cited lists the keys of the notes referred to, in order.
	cited []string
	// origin maps the key of a cited note to the file that cites it.
	origin map[string]string
	// anchors maps the names of anchors to the files they are in.
	anchors map[string]string
	// headings counts the headings given an identifier.
	headings int
	// language is the language tag of the book.
	language string
}

// Convert turns the Word document at src into an EPUB at dst.
func Convert(ctx context.Context, src, dst string, opts Options) (*Report, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotDocument, err)
	}
	defer zr.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".docx-*.epub")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	c := &conversion{
		pack: &pack{zr: zr}, out: epub.NewWriter(tmp),
		notes: map[string]int{}, images: map[string]string{},
		footnotes: map[string][]block{}, origin: map[string]string{}, anchors: map[string]string{},
	}
	report, err := c.run(ctx, src, opts)
	if err != nil {
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	return report, os.Rename(tmp.Name(), dst)
}

// run reads the document and writes the book.
func (c *conversion) run(ctx context.Context, src string, opts Options) (*Report, error) {
	main := partOf(c.pack.relationships(""), documentType)
	if main == "" {
		main = "word/document.xml"
	}
	c.rels = c.pack.relationships(main)
	styles := readStyles(c.pack.root(partOf(c.rels, stylesType), c.notes))
	r := &reader{
		styles:    styles,
		numbering: readNumbering(c.pack.root(partOf(c.rels, numberingType), c.notes)),
		links:     c.addresses(),
	}
	blocks, err := c.body(ctx, main, r)
	if err != nil {
		return nil, err
	}
	c.readNotes(r, footnotesType, footnoteKey)
	c.readNotes(r, endnotesType, endnoteKey)
	details := meta.Fill(opts.Metadata, declared(src))
	c.language = languageOf(details.Language, styles.language)
	details.Language = c.language
	sections := divide(inferHeadings(blocks))
	contents, err := c.write(ctx, sections, details.Title)
	if err != nil {
		return nil, err
	}
	book := epub.Details{Metadata: details, ID: opts.ID, Contents: contents, Cover: c.cover(opts.Cover)}
	if book.ID == "" {
		book.ID = fallbackID(details)
	}
	if err := c.out.Close(book); err != nil {
		return nil, err
	}
	return &Report{Sections: len(sections), Images: c.shown(), Warnings: c.warnings()}, nil
}

// body reads the blocks of the main part, called name, one element at
// a time, so that the blocks, and not the whole of its XML, are what is
// held at once. A heading that only announces a table of contents goes
// with it: the book gets a table of contents of its own.
func (c *conversion) body(ctx context.Context, name string, r *reader) ([]block, error) {
	f, err := c.pack.open(name, maxPartBytes)
	if err != nil {
		return nil, ErrNotDocument
	}
	defer f.Close()
	dec := newDecoder(f)
	if err := seek(dec, "body"); err != nil {
		return nil, ErrNotDocument
	}
	var blocks []block
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotDocument, err)
		}
		start, opens := tok.(xml.StartElement)
		if _, closes := tok.(xml.EndElement); closes {
			return blocks, nil
		}
		if !opens {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := readNode(dec, start, 1, c.notes)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotDocument, err)
		}
		if n == nil {
			continue
		}
		read := r.blocks([]*node{n})
		if last := len(blocks) - 1; r.takeContents() && last >= 0 && blocks[last].level > 0 {
			blocks = blocks[:last]
		}
		blocks = append(blocks, read...)
	}
}

// seek reads dec up to the start of the first element called name.
func seek(dec *xml.Decoder, name string) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == name {
			return nil
		}
	}
}

// fallbackID returns an identifier derived from the details of a book,
// for books that come without one.
func fallbackID(m meta.Metadata) string {
	return fmt.Sprintf("athenaeum-%08x", crc32.ChecksumIEEE([]byte(m.Title+strings.Join(m.Authors, ","))))
}

// declared returns what the document at src says about itself.
func declared(src string) meta.Metadata {
	m, _ := meta.Read(src, filepath.Base(src))
	return m
}

// addresses returns the addresses outside the document that the main
// part refers to, by identifier.
func (c *conversion) addresses() map[string]string {
	out := map[string]string{}
	for id, rel := range c.rels {
		if rel.external {
			out[id] = rel.target
		}
	}
	return out
}

// readNotes reads the notes of one kind, footnotes or endnotes, from
// the part with the given type and files them under the given prefix.
// The notes Word keeps for the lines that separate notes from the text
// have a type and are passed over.
func (c *conversion) readNotes(r *reader, kind, prefix string) {
	root := c.pack.root(partOf(c.rels, kind), c.notes)
	for _, n := range root.childrenOf() {
		if n.attr("type") == "" || n.attr("type") == "normal" {
			c.footnotes[prefix+n.attr("id")] = r.blocks(n.children)
		}
	}
}

// shown returns the number of pictures written into the book.
func (c *conversion) shown() int {
	count := 0
	for _, name := range c.images {
		if name != "" {
			count++
		}
	}
	return count
}

// cover adds the image at path to the book as its cover and returns
// the name it has there, or an empty string when there is none.
func (c *conversion) cover(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	ext := meta.ImageExt(data)
	if imageTypes[ext] == "" {
		return ""
	}
	c.out.Add("images/cover."+ext, imageTypes[ext], data)
	return "images/cover." + ext
}

// warnings words what was left out of the book.
func (c *conversion) warnings() []string {
	out := []string{}
	for _, kind := range []string{noteImage, noteTextBox, noteEquation, noteObject} {
		if n := c.notes[kind]; n > 0 {
			out = append(out, fmt.Sprintf(noteWording[kind], n, plural(n)))
		}
	}
	return out
}

// noteWording gives, for every kind of thing left out, the sentence
// that says so, with places for the count and for a plural ending.
var noteWording = map[string]string{
	noteImage: "%d picture%s in a format books cannot show, or missing from the document, " +
		"could not be included.",
	noteTextBox:  "%d piece%s of text in a box placed on the page could not be included.",
	noteEquation: "%d equation%s could not be included.",
	noteObject:   "%d embedded object%s could not be included.",
}

// plural returns the ending of a noun for the count n.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
