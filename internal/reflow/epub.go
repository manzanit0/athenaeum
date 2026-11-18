package reflow

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/manzanit0/athenaeum/internal/meta"
)

// root is the folder inside the EPUB that holds the book.
const root = "OEBPS/"

// container is the META-INF/container.xml of every book.
const container = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`

// mediaTypes maps the extensions of image files to media types.
var mediaTypes = map[string]string{"jpg": "image/jpeg", "png": "image/png", "gif": "image/gif"}

// archive writes the files of an EPUB.
type archive struct {
	// zw is the ZIP file being written.
	zw *zip.Writer
	// err is the first error met while writing.
	err error
}

// newArchive starts an EPUB on w with the files every EPUB begins
// with.
func newArchive(w io.Writer) *archive {
	a := &archive{zw: zip.NewWriter(w)}
	a.add("mimetype", []byte("application/epub+zip"), zip.Store)
	a.add("META-INF/container.xml", []byte(container), zip.Deflate)
	return a
}

// add writes one file. Errors are kept for close to report.
func (a *archive) add(name string, data []byte, method uint16) {
	if a.err != nil {
		return
	}
	w, err := a.zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
	if err == nil {
		_, err = w.Write(data)
	}
	a.err = err
}

// close finishes the EPUB and returns the first error met.
func (a *archive) close() error {
	if err := a.zw.Close(); a.err == nil {
		a.err = err
	}
	return a.err
}

// asset is an image file of the book.
type asset struct {
	// name is the path of the file relative to the book folder.
	name string
	// ext is the extension that tells the type of the image.
	ext string
}

// book holds everything the package files are written from.
type book struct {
	// meta describes the book.
	meta meta.Metadata
	// language is the language tag of the book.
	language string
	// identifier is the unique identifier of the book.
	identifier string
	// modified is the modification time in the form EPUB asks for.
	modified string
	// sections holds the content.
	sections []section
	// entries holds the table of contents.
	entries []entry
	// images lists the image files.
	images []asset
	// cover is the cover image, or nil.
	cover *asset
}

// sectionFile returns the file name of the section with index i.
func sectionFile(i int) string {
	return fmt.Sprintf("section%04d.xhtml", i+1)
}

// href returns the address an entry of the table of contents points
// at.
func (e entry) href() string {
	if e.id == "" {
		return sectionFile(e.section)
	}
	return sectionFile(e.section) + "#" + e.id
}

// levels returns the nesting depth of every entry, corrected so the
// first is zero and none is more than one deeper than the one before.
func levels(entries []entry) []int {
	out := make([]int, len(entries))
	for i, e := range entries {
		if i > 0 {
			out[i] = min(e.level, out[i-1]+1)
		}
	}
	return out
}

// nest writes entries as a nested list: open starts an item for the
// entry with the given index, deeper starts a list inside the current
// item and shallower ends the current item, and with it one nested
// list for every level in excess of one.
func nest(entries []entry, open func(i int), deeper func(), closeItem func(nested bool)) {
	depths := levels(entries)
	for i := range entries {
		switch {
		case i == 0:
		case depths[i] > depths[i-1]:
			deeper()
		default:
			closeItem(false)
			for range depths[i-1] - depths[i] {
				closeItem(true)
			}
		}
		open(i)
	}
	if len(entries) > 0 {
		closeItem(false)
		for range depths[len(depths)-1] {
			closeItem(true)
		}
	}
}

// navigation returns the EPUB 3 navigation document.
func (b *book) navigation() []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, pageHead, b.language, "Contents")
	buf.WriteString("<nav epub:type=\"toc\" id=\"toc\">\n<h1>Contents</h1>\n<ol>\n")
	nest(b.entries,
		func(i int) {
			fmt.Fprintf(&buf, "<li><a href=\"%s\">%s</a>", b.entries[i].href(), escape(b.entries[i].title))
		},
		func() { buf.WriteString("\n<ol>\n") },
		func(nested bool) {
			if nested {
				buf.WriteString("</ol>\n")
			}
			buf.WriteString("</li>\n")
		})
	buf.WriteString("</ol>\n</nav>\n")
	buf.WriteString(pageTail)
	return buf.Bytes()
}

// ncx returns the EPUB 2 table of contents, which older reading systems
// and conversion tools still look for.
func (b *book) ncx() []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<head><meta name="dtb:uid" content="%s"/><meta name="dtb:depth" content="%d"/></head>
<docTitle><text>%s</text></docTitle>
<navMap>
`, escape(b.identifier), tocDepth+1, escape(b.meta.Title))
	nest(b.entries,
		func(i int) {
			fmt.Fprintf(&buf, "<navPoint id=\"nav%[1]d\" playOrder=\"%[1]d\">"+
				"<navLabel><text>%[2]s</text></navLabel><content src=\"%[3]s\"/>\n",
				i+1, escape(b.entries[i].title), b.entries[i].href())
		},
		func() {},
		func(bool) { buf.WriteString("</navPoint>\n") })
	buf.WriteString("</navMap>\n</ncx>\n")
	return buf.Bytes()
}

// metadata returns the metadata element of the package document.
func (b *book) metadata() string {
	var sb strings.Builder
	element := func(name, value string) {
		if value = strings.TrimSpace(value); value != "" {
			fmt.Fprintf(&sb, "    <dc:%[1]s>%[2]s</dc:%[1]s>\n", name, escape(value))
		}
	}
	sb.WriteString("  <metadata xmlns:dc=\"http://purl.org/dc/elements/1.1/\">\n")
	fmt.Fprintf(&sb, "    <dc:identifier id=\"pub-id\">%s</dc:identifier>\n", escape(b.identifier))
	element("title", b.meta.Title)
	element("language", b.language)
	for _, author := range b.meta.Authors {
		element("creator", author)
	}
	element("publisher", b.meta.Publisher)
	element("description", b.meta.Description)
	element("date", b.meta.Published)
	for _, tag := range b.meta.Tags {
		element("subject", tag)
	}
	if isbn := strings.TrimSpace(b.meta.ISBN); isbn != "" {
		element("identifier", "urn:isbn:"+isbn)
	}
	fmt.Fprintf(&sb, "    <meta property=\"dcterms:modified\">%s</meta>\n", b.modified)
	if b.cover != nil {
		sb.WriteString("    <meta name=\"cover\" content=\"cover-image\"/>\n")
	}
	if b.meta.Series != "" {
		fmt.Fprintf(&sb, "    <meta name=\"calibre:series\" content=\"%s\"/>\n", escape(b.meta.Series))
		fmt.Fprintf(&sb, "    <meta name=\"calibre:series_index\" content=\"%s\"/>\n",
			strconv.FormatFloat(b.meta.SeriesIndex, 'f', -1, 64))
	}
	sb.WriteString("  </metadata>\n")
	return sb.String()
}

// manifest returns the manifest element of the package document.
func (b *book) manifest() string {
	var sb strings.Builder
	item := func(id, href, mediaType, properties string) {
		fmt.Fprintf(&sb, "    <item id=\"%s\" href=\"%s\" media-type=\"%s\"", id, href, mediaType)
		if properties != "" {
			fmt.Fprintf(&sb, " properties=\"%s\"", properties)
		}
		sb.WriteString("/>\n")
	}
	sb.WriteString("  <manifest>\n")
	item("nav", "nav.xhtml", "application/xhtml+xml", "nav")
	item("ncx", "toc.ncx", "application/x-dtbncx+xml", "")
	item("css", "style.css", "text/css", "")
	if b.cover != nil {
		item("cover-image", b.cover.name, mediaTypes[b.cover.ext], "cover-image")
		item("cover", "cover.xhtml", "application/xhtml+xml", "")
	}
	for i := range b.sections {
		item(fmt.Sprintf("section%04d", i+1), sectionFile(i), "application/xhtml+xml", "")
	}
	for i, image := range b.images {
		item(fmt.Sprintf("image%04d", i+1), image.name, mediaTypes[image.ext], "")
	}
	sb.WriteString("  </manifest>\n")
	return sb.String()
}

// spine returns the spine and guide elements of the package document.
func (b *book) spine() string {
	var sb strings.Builder
	sb.WriteString("  <spine toc=\"ncx\">\n")
	if b.cover != nil {
		sb.WriteString("    <itemref idref=\"cover\"/>\n")
	}
	for i := range b.sections {
		fmt.Fprintf(&sb, "    <itemref idref=\"section%04d\"/>\n", i+1)
	}
	sb.WriteString("  </spine>\n")
	if b.cover != nil {
		sb.WriteString("  <guide>\n    <reference type=\"cover\" title=\"Cover\" href=\"cover.xhtml\"/>\n")
		sb.WriteString("  </guide>\n")
	}
	return sb.String()
}

// write adds the package files and the sections of the book to a.
func (b *book) write(a *archive) {
	a.add(root+"style.css", []byte(stylesheet), zip.Deflate)
	if b.cover != nil {
		a.add(root+"cover.xhtml", renderCover(b.cover.name, b.language), zip.Deflate)
	}
	for i, sec := range b.sections {
		a.add(root+sectionFile(i), renderSection(sec, b.language), zip.Deflate)
	}
	a.add(root+"nav.xhtml", b.navigation(), zip.Deflate)
	a.add(root+"toc.ncx", b.ncx(), zip.Deflate)
	opf := "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n" +
		"<package xmlns=\"http://www.idpf.org/2007/opf\" version=\"3.0\" unique-identifier=\"pub-id\">\n" +
		b.metadata() + b.manifest() + b.spine() + "</package>\n"
	a.add(root+"content.opf", []byte(opf), zip.Deflate)
}
