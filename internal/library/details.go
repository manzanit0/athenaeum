package library

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/manzanit0/athenaeum/internal/meta"
)

// Details is what the details file of a book says about it.
type Details struct {
	// UUID identifies the book across library copies.
	UUID string
	// Title is the title of the book.
	Title string
	// Authors are the authors of the book.
	Authors []string
	// Files lists the files of the book relative to the library folder.
	Files []string
}

// ParseDetails reads the content of a details file, reporting whether it
// describes a book.
func ParseDetails(data []byte) (Details, bool) {
	var sc sidecar
	if json.Unmarshal(data, &sc) != nil || !validID(sc.UUID) {
		return Details{}, false
	}
	return Details{UUID: sc.UUID, Title: sc.Title, Authors: meta.CleanList(sc.Authors), Files: sc.Files}, true
}

// DetailsPath returns the library relative path of the details file of
// the book identified by uuid.
func DetailsPath(uuid string) string {
	return sidecarPath(uuid)
}

// CoverFile returns the library relative path of the cover of a book
// whose cover file is called name.
func CoverFile(name string) string {
	return path.Join(metaDir, name)
}

// Kept reports whether the library relative path rel is one of the files
// the library keeps about a book, which book that is and whether the file
// holds its details rather than its cover.
func Kept(rel string) (uuid string, details, ok bool) {
	dir, name := path.Split(rel)
	if path.Clean(dir) != metaDir {
		return "", false, false
	}
	ext := path.Ext(name)
	uuid = strings.TrimSuffix(name, ext)
	if !validID(uuid) || ext == "" {
		return "", false, false
	}
	return uuid, ext == ".json", true
}
