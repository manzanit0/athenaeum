package meta

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// opfContainer is the META-INF/container.xml document of an EPUB.
type opfContainer struct {
	// Rootfiles lists the package documents of the publication.
	Rootfiles []struct {
		// FullPath is the archive path of the package document.
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

// opfPackage is the OPF package document of an EPUB.
type opfPackage struct {
	// Metadata holds the Dublin Core and meta elements.
	Metadata opfMetadata `xml:"metadata"`
	// Manifest lists the resources of the publication.
	Manifest []opfItem `xml:"manifest>item"`
}

// opfMetadata is the metadata element of an OPF package document.
type opfMetadata struct {
	// Titles holds the dc:title elements.
	Titles []string `xml:"title"`
	// Creators holds the dc:creator elements.
	Creators []string `xml:"creator"`
	// Publisher holds the dc:publisher element.
	Publisher string `xml:"publisher"`
	// Languages holds the dc:language elements.
	Languages []string `xml:"language"`
	// Identifiers holds the dc:identifier elements.
	Identifiers []opfIdentifier `xml:"identifier"`
	// Description holds the dc:description element.
	Description string `xml:"description"`
	// Subjects holds the dc:subject elements.
	Subjects []string `xml:"subject"`
	// Dates holds the dc:date elements.
	Dates []string `xml:"date"`
	// Metas holds the generic meta elements.
	Metas []opfMeta `xml:"meta"`
}

// opfIdentifier is a dc:identifier element.
type opfIdentifier struct {
	// Scheme names the identifier system, such as ISBN.
	Scheme string `xml:"scheme,attr"`
	// ID is the XML id of the element.
	ID string `xml:"id,attr"`
	// Value is the identifier itself.
	Value string `xml:",chardata"`
}

// opfMeta is a meta element in either its EPUB 2 or EPUB 3 form.
type opfMeta struct {
	// Name is the EPUB 2 property name.
	Name string `xml:"name,attr"`
	// Content is the EPUB 2 property value.
	Content string `xml:"content,attr"`
	// Property is the EPUB 3 property name.
	Property string `xml:"property,attr"`
	// Value is the EPUB 3 property value.
	Value string `xml:",chardata"`
}

// opfItem is a manifest entry of an OPF package document.
type opfItem struct {
	// ID is the manifest identifier of the resource.
	ID string `xml:"id,attr"`
	// Href is the resource location relative to the package document.
	Href string `xml:"href,attr"`
	// MediaType is the MIME type of the resource.
	MediaType string `xml:"media-type,attr"`
	// Properties holds the space separated EPUB 3 item properties.
	Properties string `xml:"properties,attr"`
}

// readEPUB extracts metadata and the cover from the EPUB at path.
func readEPUB(path string) (Metadata, *Cover, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Metadata{}, nil, err
	}
	defer zr.Close()
	var container opfContainer
	if err := decodeZipXML(&zr.Reader, "META-INF/container.xml", &container); err != nil {
		return Metadata{}, nil, err
	}
	if len(container.Rootfiles) == 0 {
		return Metadata{}, nil, errors.New("epub: no package document")
	}
	opfPath := container.Rootfiles[0].FullPath
	var pkg opfPackage
	if err := decodeZipXML(&zr.Reader, opfPath, &pkg); err != nil {
		return Metadata{}, nil, err
	}
	return pkg.metadata(), pkg.cover(&zr.Reader, opfPath), nil
}

// decodeZipXML decodes the XML file name inside zr into v.
func decodeZipXML(zr *zip.Reader, name string, v any) error {
	f, err := zr.Open(name)
	if err != nil {
		return fmt.Errorf("epub: %w", err)
	}
	defer f.Close()
	return NewXMLDecoder(f).Decode(v)
}

// NewXMLDecoder returns a decoder for the XML documents found in ebooks,
// which tolerates HTML entities, loose markup and Western encodings.
func NewXMLDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(r)
	d.Strict = false
	d.Entity = xml.HTMLEntity
	d.CharsetReader = charsetReader
	return d
}

// charsetReader lets the XML decoder read single byte Western encodings.
func charsetReader(label string, r io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return r, nil
	case "iso-8859-1", "latin1", "windows-1252", "cp1252":
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		return strings.NewReader(decodeCP1252(data)), nil
	}
	return nil, fmt.Errorf("unsupported charset %q", label)
}

// metadata converts the package metadata into a Metadata value.
func (p *opfPackage) metadata() Metadata {
	md := p.Metadata
	m := Metadata{
		Authors:     md.Creators,
		Publisher:   md.Publisher,
		Description: stripTags(md.Description),
		Tags:        md.Subjects,
		ISBN:        md.isbn(),
	}
	if len(md.Titles) > 0 {
		m.Title = md.Titles[0]
	}
	if len(md.Languages) > 0 {
		m.Language = md.Languages[0]
	}
	if len(md.Dates) > 0 {
		m.Published = md.Dates[0]
	}
	m.Series, m.SeriesIndex = md.series()
	return m
}

// isbn returns the first identifier that is an ISBN.
func (md *opfMetadata) isbn() string {
	for _, id := range md.Identifiers {
		value := strings.TrimSpace(id.Value)
		lower := strings.ToLower(value)
		switch {
		case strings.HasPrefix(lower, "urn:isbn:"):
			return value[len("urn:isbn:"):]
		case strings.HasPrefix(lower, "isbn:"):
			return value[len("isbn:"):]
		case strings.EqualFold(id.Scheme, "isbn"), strings.Contains(strings.ToLower(id.ID), "isbn"):
			return value
		}
	}
	return ""
}

// series returns the series name and index declared in the meta elements.
func (md *opfMetadata) series() (string, float64) {
	var name string
	var index float64
	for _, meta := range md.Metas {
		switch {
		case meta.Name == "calibre:series":
			name = meta.Content
		case meta.Name == "calibre:series_index":
			index, _ = strconv.ParseFloat(meta.Content, 64)
		case meta.Property == "belongs-to-collection" && name == "":
			name = meta.Value
		case meta.Property == "group-position" && index == 0:
			index, _ = strconv.ParseFloat(strings.TrimSpace(meta.Value), 64)
		}
	}
	return name, index
}

// coverItem returns the manifest entry of the cover image, if any.
func (p *opfPackage) coverItem() (opfItem, bool) {
	var coverID string
	for _, meta := range p.Metadata.Metas {
		if meta.Name == "cover" {
			coverID = meta.Content
		}
	}
	var fallback *opfItem
	for i, item := range p.Manifest {
		if !strings.HasPrefix(item.MediaType, "image/") {
			continue
		}
		if strings.Contains(item.Properties, "cover-image") || (coverID != "" && item.ID == coverID) {
			return item, true
		}
		if fallback == nil && strings.Contains(strings.ToLower(item.ID+item.Href), "cover") {
			fallback = &p.Manifest[i]
		}
	}
	if fallback != nil {
		return *fallback, true
	}
	return opfItem{}, false
}

// cover reads the cover image referenced by the package document.
func (p *opfPackage) cover(zr *zip.Reader, opfPath string) *Cover {
	item, ok := p.coverItem()
	if !ok {
		return nil
	}
	href, err := url.PathUnescape(item.Href)
	if err != nil {
		href = item.Href
	}
	f, err := zr.Open(path.Join(path.Dir(opfPath), href))
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCoverBytes))
	if err != nil {
		return nil
	}
	ext := ImageExt(data)
	if ext == "" {
		return nil
	}
	return &Cover{Data: data, Ext: ext}
}
