package meta

import (
	"archive/zip"
	"errors"
)

// The files of a Pages document read or looked for here.
const (
	// pagesDocument is the file every Pages document holds its text in.
	pagesDocument = "Index/Document.iwa"
	// pagesPreview is the picture of the first page Pages saves with a
	// document.
	pagesPreview = "preview.jpg"
)

// errNotPages is returned for a file that is not a Pages document.
var errNotPages = errors.New("pages: not a Pages document")

// readPages returns the picture of the first page of the Pages document
// at path as its cover. Pages keeps the title and the author of a
// document where only Pages reads them, so no metadata comes with it.
func readPages(path string) (Metadata, *Cover, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Metadata{}, nil, err
	}
	defer zr.Close()
	if _, err := zr.Open(pagesDocument); err != nil {
		return Metadata{}, nil, errNotPages
	}
	return Metadata{}, zipImage(&zr.Reader, pagesPreview), nil
}
