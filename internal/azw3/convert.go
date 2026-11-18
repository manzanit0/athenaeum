// Package azw3 converts EPUB books to AZW3, the KF8 format Kindles read.
//
// Every document of the EPUB becomes a KF8 file: its markup is kept as
// it is, apart from links, image references and stylesheets, which are
// pointed at their new places inside the book. Fonts are left out; the
// Kindle uses its own.
package azw3

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/manzanit0/athenaeum/internal/kf8"
	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/preview"
)

// maxDocumentBytes bounds the size of one file read from the EPUB.
const maxDocumentBytes = 256 << 20

// ErrProtected is returned for EPUB files whose content is encrypted.
var ErrProtected = errors.New("the EPUB is protected by DRM and cannot be converted")

// fontObfuscation lists the encryption algorithms that only scramble
// embedded fonts and do not protect the text.
var fontObfuscation = []string{"http://www.idpf.org/2008/embedding", "http://ns.adobe.com/pdf/enc#RC"}

// Patterns used while converting.
var (
	// algorithm matches the algorithm of an entry of encryption.xml.
	algorithm = regexp.MustCompile(`Algorithm\s*=\s*["']([^"']+)["']`)
	// placeholder matches a link waiting for its final address.
	placeholder = regexp.MustCompile(`kindle:pos:fid:0000:off:(\d{10})`)
)

// Options controls a conversion.
type Options struct {
	// Metadata overrides what the EPUB declares; empty fields fall back
	// to the EPUB.
	Metadata meta.Metadata
	// Cover is the path of the cover image; empty uses the EPUB's.
	Cover string
	// ID identifies the book on the Kindle; empty derives it from the
	// title.
	ID string
	// Created is the creation time written into the file; zero means
	// now.
	Created time.Time
}

// Report says what a conversion did and what it had to leave out.
type Report struct {
	// Files is the number of documents converted.
	Files int `json:"files"`
	// Images is the number of images embedded.
	Images int `json:"images"`
	// Warnings lists what was left out or could not be kept.
	Warnings []string `json:"warnings"`
}

// converter holds the state of one conversion.
type converter struct {
	// zr is the EPUB being read.
	zr *zip.Reader
	// book is the KF8 book being built.
	book *kf8.Book
	// files maps the documents of the reading order to their index.
	files map[string]int
	// flows maps stylesheets to their flow number; zero means unusable.
	flows map[string]int
	// resources maps images to their index; -1 means unusable.
	resources map[string]int
	// anchors maps identified elements to where they ended up.
	anchors map[string]kf8.Position
	// links holds the targets of the placeholder links, by number.
	links []target
	// aids counts the Kindle identifiers handed out.
	aids int
	// warnings collects what was left out.
	warnings []string
}

// warn records something that was left out, once.
func (c *converter) warn(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	for _, existing := range c.warnings {
		if existing == message {
			return
		}
	}
	c.warnings = append(c.warnings, message)
}

// read returns the content of the file name inside the EPUB.
func (c *converter) read(name string) ([]byte, error) {
	f, err := c.zr.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxDocumentBytes))
}

// protected reports whether the EPUB encrypts more than its fonts.
func (c *converter) protected() bool {
	data, err := c.read("META-INF/encryption.xml")
	if err != nil {
		return false
	}
	for _, match := range algorithm.FindAllStringSubmatch(string(data), -1) {
		known := false
		for _, harmless := range fontObfuscation {
			known = known || match[1] == harmless
		}
		if !known && strings.Contains(match[1], "xmlenc") {
			return true
		}
	}
	return false
}

// merged returns the details of the book at src: those given in the
// options, falling back to what the file itself declares. It also
// returns the cover the file declares.
func merged(src string, opts Options) (meta.Metadata, *meta.Cover) {
	declared, cover := meta.Read(src, filepath.Base(src))
	m := opts.Metadata
	pick := func(wanted, fallback string) string {
		if wanted != "" {
			return wanted
		}
		return fallback
	}
	m.Title = pick(m.Title, declared.Title)
	m.Publisher = pick(m.Publisher, declared.Publisher)
	m.Description = pick(m.Description, declared.Description)
	m.ISBN = pick(m.ISBN, declared.ISBN)
	m.Published = pick(m.Published, declared.Published)
	m.Language = pick(m.Language, declared.Language)
	if len(m.Authors) == 0 {
		m.Authors = declared.Authors
	}
	if len(m.Tags) == 0 {
		m.Tags = declared.Tags
	}
	if m.Series == "" {
		m.Series, m.SeriesIndex = declared.Series, declared.SeriesIndex
	}
	return m, cover
}

// fallbackID returns an identifier derived from the details of a book,
// for books that come without one.
func fallbackID(m meta.Metadata) string {
	return fmt.Sprintf("athenaeum-%08x", crc32.ChecksumIEEE([]byte(m.Title+strings.Join(m.Authors, ","))))
}

// describe fills in the details of the book from the options, falling
// back to what the EPUB at src declares, and returns the EPUB's cover.
func (c *converter) describe(src string, opts Options) *meta.Cover {
	m, cover := merged(src, opts)
	b := c.book
	b.Title, b.Publisher, b.Description, b.ISBN = m.Title, m.Publisher, m.Description, m.ISBN
	b.Published, b.Language, b.Authors, b.Subjects = m.Published, m.Language, m.Authors, m.Tags
	b.ID = opts.ID
	if b.ID == "" {
		b.ID = fallbackID(m)
	}
	b.Created = opts.Created
	if b.Created.IsZero() {
		b.Created = time.Now()
	}
	return cover
}

// position returns where the target ended up in the book. A target whose
// identifier is unknown leads to the start of its document.
func (c *converter) position(t target) kf8.Position {
	if position, ok := c.anchors[t.key()]; ok && t.fragment != "" {
		return position
	}
	index := c.files[t.name]
	return kf8.Position{File: index, Offset: c.book.Files[index].Chunks[0].InsertAt}
}

// settleLinks replaces the placeholder links with their final addresses,
// which have the same length.
func (c *converter) settleLinks() {
	for _, file := range c.book.Files {
		for _, chunk := range file.Chunks {
			for _, at := range placeholder.FindAllSubmatchIndex(chunk.Raw, -1) {
				number, err := strconv.Atoi(string(chunk.Raw[at[2]:at[3]]))
				if err != nil || number >= len(c.links) {
					continue
				}
				copy(chunk.Raw[at[0]:at[1]], c.book.Link(c.position(c.links[number])))
			}
		}
	}
}

// contents builds the table of contents of the book from the EPUB's.
func (c *converter) contents(entries []preview.Entry) {
	for _, e := range entries {
		href, fragment, _ := strings.Cut(e.Href, "#")
		name, err := url.PathUnescape(href)
		if _, known := c.files[name]; err != nil || !known || e.Title == "" {
			continue
		}
		c.book.Contents = append(c.book.Contents, kf8.Entry{
			Title: e.Title, Level: e.Level, Target: c.position(target{name, fragment}),
		})
	}
	if len(c.book.Contents) == 0 {
		c.book.Contents = []kf8.Entry{{Title: c.book.Title, Target: c.position(target{name: c.first()})}}
	}
}

// first returns the name of the first document of the book.
func (c *converter) first() string {
	for name, index := range c.files {
		if index == 0 {
			return name
		}
	}
	return ""
}

// documents converts the documents of the reading order.
func (c *converter) documents(ctx context.Context, sections []preview.Section) error {
	var names []string
	for _, s := range sections {
		name, err := url.PathUnescape(s.Href)
		if _, seen := c.files[name]; err != nil || seen {
			continue
		}
		c.files[name] = len(names)
		names = append(names, name)
	}
	for index, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := c.document(index, name)
		if err != nil {
			return err
		}
		c.book.Files = append(c.book.Files, file)
	}
	return nil
}

// cover stores the cover: the image at the given path, or the one the
// EPUB declares.
func (c *converter) cover(path string, declared *meta.Cover) {
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			c.setCover(data)
			return
		}
	}
	if declared != nil {
		c.setCover(declared.Data)
	}
}

// build reads the EPUB at src into a KF8 book.
func build(ctx context.Context, src string, opts Options) (*kf8.Book, *Report, error) {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return nil, nil, err
	}
	defer zr.Close()
	c := &converter{
		zr:        &zr.Reader,
		book:      &kf8.Book{Cover: -1, Thumbnail: -1},
		files:     map[string]int{},
		flows:     map[string]int{},
		resources: map[string]int{},
		anchors:   map[string]kf8.Position{},
		warnings:  []string{},
	}
	if c.protected() {
		return nil, nil, ErrProtected
	}
	outline, err := preview.ReadOutline(src)
	if err != nil {
		return nil, nil, err
	}
	declared := c.describe(src, opts)
	if err := c.documents(ctx, outline.Sections); err != nil {
		return nil, nil, err
	}
	c.settleLinks()
	c.contents(outline.Contents)
	images := len(c.book.Resources)
	c.cover(opts.Cover, declared)
	return c.book, &Report{Files: len(c.book.Files), Images: images, Warnings: c.warnings}, nil
}

// Convert writes the EPUB at src as an AZW3 book at dst.
func Convert(ctx context.Context, src, dst string, opts Options) (*Report, error) {
	book, report, err := build(ctx, src, opts)
	if err != nil {
		return nil, fmt.Errorf("azw3: %w", err)
	}
	out, err := os.Create(dst)
	if err != nil {
		return nil, err
	}
	if err := book.Write(out); err != nil {
		out.Close()
		return nil, fmt.Errorf("azw3: %w", err)
	}
	return report, out.Close()
}
