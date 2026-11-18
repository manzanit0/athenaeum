// Package epub writes EPUB files: reflowable EPUB 3 books that also
// carry the EPUB 2 table of contents older reading systems look for.
package epub

import (
	"archive/zip"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"

	"github.com/manzanit0/athenaeum/internal/meta"
)

// root is the folder inside the EPUB that holds the book.
const root = "OEBPS/"

// modified is the modification date written into every book, fixed so
// that the same input always gives the same file.
const modified = "2000-01-01T00:00:00Z"

// container is the META-INF/container.xml of every book.
const container = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`

// Entry is one line of the table of contents.
type Entry struct {
	// Title is the text of the line.
	Title string
	// Href is the target, relative to the folder holding the book.
	Href string
	// Level is the nesting depth, starting at zero.
	Level int
}

// Details describes a finished book.
type Details struct {
	// Metadata is the description of the book.
	Metadata meta.Metadata
	// ID identifies the book.
	ID string
	// Contents is the table of contents.
	Contents []Entry
	// Cover is the name of the cover image among the files added, or
	// empty when there is none.
	Cover string
}

// item is a file of the book listed in its manifest.
type item struct {
	// name is the path of the file relative to the folder of the book.
	name string
	// mediaType is the MIME type of the file.
	mediaType string
	// document reports whether the file is part of the reading order.
	document bool
}

// Writer writes the files of an EPUB one at a time.
type Writer struct {
	// zw is the ZIP file being written.
	zw *zip.Writer
	// items lists the files added so far.
	items []item
	// err is the first error met while writing.
	err error
}

// NewWriter starts an EPUB on w.
func NewWriter(w io.Writer) *Writer {
	ew := &Writer{zw: zip.NewWriter(w)}
	ew.store("mimetype", []byte("application/epub+zip"), zip.Store)
	ew.store("META-INF/container.xml", []byte(container), zip.Deflate)
	return ew
}

// store writes one file of the archive, keeping the first error.
func (w *Writer) store(name string, data []byte, method uint16) {
	if w.err != nil {
		return
	}
	f, err := w.zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
	if err == nil {
		_, err = f.Write(data)
	}
	w.err = err
}

// Add adds a stylesheet, image or font under name, a path relative to
// the folder of the book.
func (w *Writer) Add(name, mediaType string, data []byte) {
	method := uint16(zip.Deflate)
	if strings.HasPrefix(mediaType, "image/") && mediaType != "image/svg+xml" {
		method = zip.Store
	}
	w.store(root+name, data, method)
	w.items = append(w.items, item{name: name, mediaType: mediaType})
}

// AddDocument adds an XHTML document as the next one in reading order.
func (w *Writer) AddDocument(name string, data []byte) {
	w.store(root+name, data, zip.Deflate)
	w.items = append(w.items, item{name: name, mediaType: "application/xhtml+xml", document: true})
}

// Close writes the package document and the tables of contents and
// finishes the EPUB.
func (w *Writer) Close(d Details) error {
	if d.Metadata.Title == "" {
		d.Metadata.Title = "Unknown"
	}
	w.store(root+"nav.xhtml", []byte(navigation(d)), zip.Deflate)
	w.store(root+"toc.ncx", []byte(ncx(d)), zip.Deflate)
	w.store(root+"content.opf", []byte(w.packageDoc(d)), zip.Deflate)
	if err := w.zw.Close(); w.err == nil {
		w.err = err
	}
	return w.err
}

// escape escapes s for use in XML text and attribute values.
func escape(s string) string {
	return html.EscapeString(s)
}

// language returns the language of the book, or the code for an
// undetermined one.
func language(m meta.Metadata) string {
	if m.Language == "" {
		return "und"
	}
	return m.Language
}

// metadata returns the metadata element of the package document.
func (w *Writer) metadata(d Details) string {
	var sb strings.Builder
	m := d.Metadata
	element := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&sb, "    <dc:%s>%s</dc:%s>\n", name, escape(value), name)
		}
	}
	sb.WriteString("  <metadata xmlns:dc=\"http://purl.org/dc/elements/1.1/\">\n")
	fmt.Fprintf(&sb, "    <dc:identifier id=\"id\">%s</dc:identifier>\n", escape(d.ID))
	element("title", m.Title)
	element("language", language(m))
	for _, author := range m.Authors {
		element("creator", author)
	}
	element("publisher", m.Publisher)
	element("description", m.Description)
	element("date", m.Published)
	for _, tag := range m.Tags {
		element("subject", tag)
	}
	if m.ISBN != "" {
		element("identifier", "urn:isbn:"+m.ISBN)
	}
	fmt.Fprintf(&sb, "    <meta property=\"dcterms:modified\">%s</meta>\n", modified)
	if m.Series != "" {
		fmt.Fprintf(&sb, "    <meta name=\"calibre:series\" content=\"%s\"/>\n", escape(m.Series))
		fmt.Fprintf(&sb, "    <meta name=\"calibre:series_index\" content=\"%s\"/>\n",
			strconv.FormatFloat(m.SeriesIndex, 'f', -1, 64))
	}
	if id := w.id(d.Cover); id != "" {
		fmt.Fprintf(&sb, "    <meta name=\"cover\" content=\"%s\"/>\n", id)
	}
	sb.WriteString("  </metadata>\n")
	return sb.String()
}

// id returns the manifest identifier of the file called name, or an
// empty string when no such file was added.
func (w *Writer) id(name string) string {
	for i, it := range w.items {
		if it.name == name && name != "" {
			return "item" + strconv.Itoa(i+1)
		}
	}
	return ""
}

// packageDoc returns the package document.
func (w *Writer) packageDoc(d Details) string {
	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<package xmlns=\"http://www.idpf.org/2007/opf\" version=\"3.0\" unique-identifier=\"id\">\n")
	sb.WriteString(w.metadata(d))
	sb.WriteString("  <manifest>\n")
	sb.WriteString("    <item id=\"nav\" href=\"nav.xhtml\" media-type=\"application/xhtml+xml\" properties=\"nav\"/>\n")
	sb.WriteString("    <item id=\"ncx\" href=\"toc.ncx\" media-type=\"application/x-dtbncx+xml\"/>\n")
	for i, it := range w.items {
		properties := ""
		if it.name == d.Cover {
			properties = ` properties="cover-image"`
		}
		fmt.Fprintf(&sb, "    <item id=\"item%d\" href=\"%s\" media-type=\"%s\"%s/>\n",
			i+1, escape(it.name), it.mediaType, properties)
	}
	sb.WriteString("  </manifest>\n  <spine toc=\"ncx\">\n")
	for i, it := range w.items {
		if it.document {
			fmt.Fprintf(&sb, "    <itemref idref=\"item%d\"/>\n", i+1)
		}
	}
	sb.WriteString("  </spine>\n</package>\n")
	return sb.String()
}

// nest writes the lines of a table of contents as nested elements: open
// starts a line, deeper and shallower surround the lines nested in it
// and closeLine ends it.
func nest(entries []Entry, open func(e Entry, number int), deeper, shallower, closeLine func()) {
	depth := 0
	for i, e := range entries {
		level := min(max(e.Level, 0), depth+1)
		if i == 0 {
			level = 0
		}
		switch {
		case i == 0:
		case level > depth:
			deeper()
		default:
			closeLine()
			for ; depth > level; depth-- {
				shallower()
				closeLine()
			}
		}
		depth = level
		open(e, i+1)
	}
	if len(entries) == 0 {
		return
	}
	closeLine()
	for ; depth > 0; depth-- {
		shallower()
		closeLine()
	}
}

// navigation returns the EPUB 3 navigation document.
func navigation(d Details) string {
	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprintf(&sb, "<html xmlns=\"http://www.w3.org/1999/xhtml\" xmlns:epub=\"http://www.idpf.org/2007/ops\" lang=\"%s\">\n",
		escape(language(d.Metadata)))
	fmt.Fprintf(&sb, "<head><title>%s</title></head>\n<body>\n<nav epub:type=\"toc\"><h1>Contents</h1>\n<ol>\n",
		escape(d.Metadata.Title))
	nest(d.Contents,
		func(e Entry, _ int) { fmt.Fprintf(&sb, "<li><a href=\"%s\">%s</a>", escape(e.Href), escape(e.Title)) },
		func() { sb.WriteString("\n<ol>\n") },
		func() { sb.WriteString("</ol>\n") },
		func() { sb.WriteString("</li>\n") })
	if len(d.Contents) == 0 {
		sb.WriteString("<li><span>Contents</span></li>\n")
	}
	sb.WriteString("</ol>\n</nav>\n</body>\n</html>\n")
	return sb.String()
}

// ncx returns the EPUB 2 table of contents.
func ncx(d Details) string {
	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString("<ncx xmlns=\"http://www.daisy.org/z3986/2005/ncx/\" version=\"2005-1\">\n")
	fmt.Fprintf(&sb, "<head><meta name=\"dtb:uid\" content=\"%s\"/></head>\n", escape(d.ID))
	fmt.Fprintf(&sb, "<docTitle><text>%s</text></docTitle>\n<navMap>\n", escape(d.Metadata.Title))
	nest(d.Contents,
		func(e Entry, number int) {
			fmt.Fprintf(&sb, "<navPoint id=\"nav%d\" playOrder=\"%d\"><navLabel><text>%s</text></navLabel><content src=\"%s\"/>",
				number, number, escape(e.Title), escape(e.Href))
		},
		func() { sb.WriteString("\n") },
		func() {},
		func() { sb.WriteString("</navPoint>\n") })
	sb.WriteString("</navMap>\n</ncx>\n")
	return sb.String()
}
