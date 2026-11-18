// Package preview opens ebooks for reading in the web interface: it lists
// the reading order and table of contents of an EPUB, serves the files
// inside it and keeps the EPUB renditions made for other formats.
package preview

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/manzanit0/athenaeum/internal/meta"
)

// ErrNotFound is returned when an EPUB has no file with the given name.
var ErrNotFound = errors.New("no such file in the book")

// ncxMediaType is the media type of an EPUB 2 table of contents.
const ncxMediaType = "application/x-dtbncx+xml"

// mediaTypes maps the extensions the standard library does not know, or
// knows differently from what an EPUB needs, to media types. EPUB content
// documents are XHTML whatever their extension.
var mediaTypes = map[string]string{
	".xhtml": "application/xhtml+xml",
	".html":  "application/xhtml+xml",
	".htm":   "application/xhtml+xml",
	".css":   "text/css; charset=utf-8",
	".ncx":   ncxMediaType,
	".opf":   "application/oebps-package+xml",
	".otf":   "font/otf",
	".ttf":   "font/ttf",
	".woff":  "font/woff",
	".woff2": "font/woff2",
}

// Section is one document of the reading order of an EPUB.
type Section struct {
	// Href is the URL escaped path of the document inside the EPUB.
	Href string `json:"href"`
	// Title is the heading the table of contents gives the document.
	Title string `json:"title"`
}

// Entry is one line of the table of contents of an EPUB.
type Entry struct {
	// Title is the text of the line.
	Title string `json:"title"`
	// Href is the URL escaped path of the target inside the EPUB, which
	// may end in a fragment.
	Href string `json:"href"`
	// Level is the nesting depth of the line, starting at zero.
	Level int `json:"level"`
}

// Outline is the reading order and the table of contents of an EPUB.
type Outline struct {
	// Sections lists the documents in reading order.
	Sections []Section
	// Contents lists the lines of the table of contents.
	Contents []Entry
}

// container is the META-INF/container.xml document of an EPUB.
type container struct {
	// Rootfiles lists the package documents of the publication.
	Rootfiles []struct {
		// FullPath is the path of the package document.
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

// packageDoc is the part of an OPF package document needed for reading.
type packageDoc struct {
	// Manifest lists the resources of the publication.
	Manifest []item `xml:"manifest>item"`
	// Spine is the reading order.
	Spine spine `xml:"spine"`
}

// item is a manifest entry of an OPF package document.
type item struct {
	// ID is the manifest identifier of the resource.
	ID string `xml:"id,attr"`
	// Href is the resource location relative to the package document.
	Href string `xml:"href,attr"`
	// MediaType is the MIME type of the resource.
	MediaType string `xml:"media-type,attr"`
	// Properties holds the space separated EPUB 3 item properties.
	Properties string `xml:"properties,attr"`
}

// spine is the spine element of an OPF package document.
type spine struct {
	// TOC is the manifest identifier of the EPUB 2 table of contents.
	TOC string `xml:"toc,attr"`
	// Refs names the manifest items in reading order.
	Refs []struct {
		// IDRef is the manifest identifier of the document.
		IDRef string `xml:"idref,attr"`
	} `xml:"itemref"`
}

// ncx is an EPUB 2 table of contents.
type ncx struct {
	// Points holds the top level lines.
	Points []navPoint `xml:"navMap>navPoint"`
}

// navPoint is one line of an EPUB 2 table of contents.
type navPoint struct {
	// Label is the text of the line.
	Label string `xml:"navLabel>text"`
	// Content points at the target of the line.
	Content struct {
		// Src is the target relative to the table of contents file.
		Src string `xml:"src,attr"`
	} `xml:"content"`
	// Points holds the lines nested below this one.
	Points []navPoint `xml:"navPoint"`
}

// ReadOutline returns the reading order and table of contents of the EPUB
// at file.
func ReadOutline(file string) (*Outline, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil, fmt.Errorf("epub: %w", err)
	}
	defer zr.Close()
	var c container
	if err := decode(&zr.Reader, "META-INF/container.xml", &c); err != nil {
		return nil, err
	}
	if len(c.Rootfiles) == 0 {
		return nil, errors.New("epub: no package document")
	}
	opfPath := c.Rootfiles[0].FullPath
	var pkg packageDoc
	if err := decode(&zr.Reader, opfPath, &pkg); err != nil {
		return nil, err
	}
	out := &Outline{Sections: []Section{}, Contents: pkg.contents(&zr.Reader, opfPath)}
	titles := map[string]string{}
	for _, entry := range slices.Backward(out.Contents) {
		target, _, _ := strings.Cut(entry.Href, "#")
		titles[target] = entry.Title
	}
	items := map[string]item{}
	for _, it := range pkg.Manifest {
		items[it.ID] = it
	}
	for _, ref := range pkg.Spine.Refs {
		if href, ok := resolve(opfPath, items[ref.IDRef].Href); ok {
			out.Sections = append(out.Sections, Section{Href: href, Title: titles[href]})
		}
	}
	if len(out.Sections) == 0 {
		return nil, errors.New("epub: the book has nothing to read")
	}
	return out, nil
}

// decode decodes the XML file name inside zr into v.
func decode(zr *zip.Reader, name string, v any) error {
	f, err := zr.Open(name)
	if err != nil {
		return fmt.Errorf("epub: %w", err)
	}
	defer f.Close()
	if err := meta.NewXMLDecoder(f).Decode(v); err != nil {
		return fmt.Errorf("epub: %s: %w", name, err)
	}
	return nil
}

// resolve turns href, a reference found in the file base, into the URL
// escaped path of its target inside the EPUB, keeping any fragment. It
// reports false for references that are empty or leave the EPUB.
func resolve(base, href string) (string, bool) {
	target, fragment, hasFragment := strings.Cut(href, "#")
	if unescaped, err := url.PathUnescape(target); err == nil {
		target = unescaped
	}
	if target == "" || strings.Contains(target, "://") {
		return "", false
	}
	name := path.Join(path.Dir(base), target)
	if !fs.ValidPath(name) || name == "." {
		return "", false
	}
	parts := strings.Split(name, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	escaped := strings.Join(parts, "/")
	if hasFragment {
		escaped += "#" + fragment
	}
	return escaped, true
}

// contents returns the table of contents declared by the package at
// opfPath, preferring the EPUB 3 navigation document over the EPUB 2 one.
func (p *packageDoc) contents(zr *zip.Reader, opfPath string) []Entry {
	var navigation, legacy string
	for _, it := range p.Manifest {
		switch {
		case slices.Contains(strings.Fields(it.Properties), "nav"):
			navigation = it.Href
		case it.ID == p.Spine.TOC && it.ID != "", it.MediaType == ncxMediaType && legacy == "":
			legacy = it.Href
		}
	}
	if entries := readNavigation(zr, opfPath, navigation); len(entries) > 0 {
		return entries
	}
	return readNCX(zr, opfPath, legacy)
}

// archivePath returns the path inside the EPUB of href, a reference found
// in the file base.
func archivePath(base, href string) (string, bool) {
	escaped, ok := resolve(base, href)
	if !ok {
		return "", false
	}
	target, _, _ := strings.Cut(escaped, "#")
	name, err := url.PathUnescape(target)
	return name, err == nil
}

// readNCX returns the lines of the EPUB 2 table of contents at href, a
// reference found in the package document at opfPath.
func readNCX(zr *zip.Reader, opfPath, href string) []Entry {
	entries := []Entry{}
	name, ok := archivePath(opfPath, href)
	if !ok {
		return entries
	}
	var doc ncx
	if err := decode(zr, name, &doc); err != nil {
		return entries
	}
	return flatten(entries, doc.Points, name, 0)
}

// flatten appends points and the lines nested below them to entries.
func flatten(entries []Entry, points []navPoint, base string, level int) []Entry {
	for _, point := range points {
		if href, ok := resolve(base, point.Content.Src); ok {
			entries = append(entries, Entry{Title: clean(point.Label), Href: href, Level: level})
		}
		entries = flatten(entries, point.Points, base, level+1)
	}
	return entries
}

// clean collapses the white space of a title.
func clean(title string) string {
	return strings.Join(strings.Fields(title), " ")
}

// File is one file of an EPUB opened for reading.
type File struct {
	io.Reader
	// Size is the length of the file in bytes.
	Size int64
	// MediaType is the MIME type of the file.
	MediaType string
	// archive is the EPUB the file is read from.
	archive *zip.ReadCloser
}

// Close releases the file and the EPUB it is read from.
func (f *File) Close() error {
	return f.archive.Close()
}

// OpenFile opens the file name inside the EPUB at epub.
func OpenFile(epub, name string) (*File, error) {
	zr, err := zip.OpenReader(epub)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	f, err := zr.Open(name)
	if err != nil {
		zr.Close()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		zr.Close()
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return &File{Reader: f, Size: info.Size(), MediaType: mediaType(name), archive: zr}, nil
}

// mediaType returns the MIME type to serve the file name with.
func mediaType(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if known, ok := mediaTypes[ext]; ok {
		return known
	}
	if guessed := mime.TypeByExtension(ext); guessed != "" {
		return guessed
	}
	return "application/octet-stream"
}
