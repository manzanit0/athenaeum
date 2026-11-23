package meta

import (
	"archive/zip"
	"io"
	"path"
	"regexp"
	"strings"
)

// The ends of the relationship types that lead from the root of a Word
// document to the parts read here. The beginnings differ between the
// two forms of the format, so only the ends are compared.
const (
	// corePropertiesType ends the type of the part with the title and
	// author of the document.
	corePropertiesType = "/metadata/core-properties"
	// thumbnailType ends the type of the preview picture.
	thumbnailType = "/metadata/thumbnail"
)

// The parts of a Word document at their usual places, tried when the
// root of the document does not say where they are.
const (
	// corePropertiesPart is the usual place of the core properties.
	corePropertiesPart = "docProps/core.xml"
	// thumbnailPart is the usual place of the preview picture.
	thumbnailPart = "docProps/thumbnail.jpeg"
)

// opcRelationships is a list of the parts a part of a Word document
// refers to.
type opcRelationships struct {
	// Items holds the references.
	Items []opcRelationship `xml:"Relationship"`
}

// opcRelationship is one reference from a part to another.
type opcRelationship struct {
	// Type says what the target is to the part that refers to it.
	Type string `xml:"Type,attr"`
	// Target is the path of the target.
	Target string `xml:"Target,attr"`
}

// coreProperties is the part of a Word document that describes it.
type coreProperties struct {
	// Title is the dc:title element.
	Title string `xml:"title"`
	// Creator is the dc:creator element, which may name several people.
	Creator string `xml:"creator"`
	// Keywords is the cp:keywords element, a list in one string.
	Keywords string `xml:"keywords"`
	// Description is the dc:description element.
	Description string `xml:"description"`
	// Language is the dc:language element.
	Language string `xml:"language"`
}

// keywordSeparator matches what separates the keywords of a document.
var keywordSeparator = regexp.MustCompile(`\s*[,;]\s*`)

// readDOCX extracts metadata and the preview picture from the Word
// document at path.
func readDOCX(path string) (Metadata, *Cover, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Metadata{}, nil, err
	}
	defer zr.Close()
	var root opcRelationships
	_ = decodeZipXML(&zr.Reader, "_rels/.rels", &root)
	var core coreProperties
	if err := decodeZipXML(&zr.Reader, root.target(corePropertiesType, corePropertiesPart), &core); err != nil {
		core = coreProperties{}
	}
	m := Metadata{
		Title:       core.Title,
		Authors:     splitAuthors(core.Creator),
		Description: core.Description,
		Language:    core.Language,
		Tags:        keywordSeparator.Split(core.Keywords, -1),
	}
	return m, zipImage(&zr.Reader, root.target(thumbnailType, thumbnailPart)), nil
}

// target returns the path of the part whose relationship type ends in
// suffix, or fallback when there is none.
func (r opcRelationships) target(suffix, fallback string) string {
	for _, item := range r.Items {
		if strings.HasSuffix(item.Type, suffix) {
			return strings.TrimPrefix(path.Clean("/"+item.Target), "/")
		}
	}
	return fallback
}

// zipImage returns the image stored under name in zr, or nil when there
// is none or it is not an image a cover can be.
func zipImage(zr *zip.Reader, name string) *Cover {
	f, err := zr.Open(name)
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
