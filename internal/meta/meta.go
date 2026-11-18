// Package meta reads the metadata and cover images embedded in ebook files.
package meta

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Metadata describes a book independently of any file format.
type Metadata struct {
	// Title is the book title.
	Title string `json:"title"`
	// Authors lists the book authors in display order.
	Authors []string `json:"authors"`
	// Series is the name of the series the book belongs to.
	Series string `json:"series"`
	// SeriesIndex is the position of the book within its series.
	SeriesIndex float64 `json:"series_index"`
	// Publisher is the name of the publisher.
	Publisher string `json:"publisher"`
	// Language is the language code or name of the book.
	Language string `json:"language"`
	// ISBN is the International Standard Book Number.
	ISBN string `json:"isbn"`
	// Description is a plain text summary of the book.
	Description string `json:"description"`
	// Tags lists the subjects the book is filed under.
	Tags []string `json:"tags"`
	// Published is the publication date: a year, a year and month or a
	// full day, written "2006", "2006-01" or "2006-01-02".
	Published string `json:"published"`
}

// Cover is a cover image extracted from an ebook file.
type Cover struct {
	// Data holds the encoded image bytes.
	Data []byte
	// Ext is the file extension matching the image type, without a dot.
	Ext string
}

// maxCoverBytes bounds how much image data is read for a cover.
const maxCoverBytes = 20 << 20

// importFormats lists the file extensions accepted into the library.
var importFormats = []string{
	"azw", "azw3", "azw4", "cbr", "cbz", "djvu", "docx", "epub", "fb2",
	"htmlz", "kepub", "kfx", "lit", "mobi", "odt", "pdf", "prc", "rtf", "txt",
}

// ImportFormats returns the file extensions accepted into the library.
func ImportFormats() []string {
	return slices.Clone(importFormats)
}

// FormatOf returns the lower case format of a file name and whether the
// library accepts it.
func FormatOf(name string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	return ext, slices.Contains(importFormats, ext)
}

// Read extracts metadata and the cover from the ebook at path. Fields the
// file does not provide are guessed from name, the original file name.
func Read(path, name string) (Metadata, *Cover) {
	format, _ := FormatOf(name)
	var m Metadata
	var cover *Cover
	var err error
	switch format {
	case "epub", "kepub":
		m, cover, err = readEPUB(path)
	case "mobi", "azw", "azw3", "prc":
		m, cover, err = readMOBI(path, true)
	case "pdf":
		m, err = readPDF(path)
	}
	if err != nil {
		m, cover = Metadata{}, nil
	}
	return fillFromName(clean(m), name), cover
}

// ReadHeader extracts metadata from the ebook at path reading only its
// header, which is cheap enough for listing files on slow devices. Formats
// that would need a full scan fall back to the file name.
func ReadHeader(path, name string) Metadata {
	var m Metadata
	if format, _ := FormatOf(name); isMOBI(format) {
		if parsed, _, err := readMOBI(path, false); err == nil {
			m = parsed
		}
	}
	return fillFromName(clean(m), name)
}

// isMOBI reports whether format is stored in a MOBI container.
func isMOBI(format string) bool {
	return slices.Contains([]string{"mobi", "azw", "azw3", "prc"}, format)
}

// clean trims whitespace and drops empty entries from m.
func clean(m Metadata) Metadata {
	m.Title = strings.TrimSpace(m.Title)
	m.Series = strings.TrimSpace(m.Series)
	m.Publisher = strings.TrimSpace(m.Publisher)
	m.Language = strings.TrimSpace(m.Language)
	m.ISBN = strings.TrimSpace(m.ISBN)
	m.Description = strings.TrimSpace(m.Description)
	m.Published = Date(m.Published)
	m.Authors = CleanList(m.Authors)
	m.Tags = CleanList(m.Tags)
	return m
}

// CleanList trims the entries of in and removes blanks and duplicates. The
// result is never nil so it encodes as an empty JSON array.
func CleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// fillFromName guesses a missing title and author from a file name shaped
// like "Title - Author.ext".
func fillFromName(m Metadata, name string) Metadata {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	title, author, found := strings.Cut(base, " - ")
	if m.Title == "" {
		m.Title = strings.TrimSpace(title)
	}
	if m.Title == "" {
		m.Title = "Untitled"
	}
	if len(m.Authors) == 0 && found {
		m.Authors = splitAuthors(author)
	}
	return m
}

// authorSeparator matches the separators used between author names.
var authorSeparator = regexp.MustCompile(`\s*[;&]\s*`)

// splitAuthors splits a combined author string into individual names.
func splitAuthors(s string) []string {
	return CleanList(authorSeparator.Split(s, -1))
}

// htmlTag matches an HTML or XML tag.
var htmlTag = regexp.MustCompile(`<[^>]*>`)

// stripTags removes markup from s, leaving its text.
func stripTags(s string) string {
	return strings.TrimSpace(htmlTag.ReplaceAllString(s, ""))
}

// ImageExt returns the file extension for the image type of data, or an
// empty string when data is not a recognised image.
func ImageExt(data []byte) string {
	switch {
	case len(data) > 3 && data[0] == 0xFF && data[1] == 0xD8:
		return "jpg"
	case strings.HasPrefix(string(data[:min(len(data), 8)]), "\x89PNG"):
		return "png"
	case strings.HasPrefix(string(data[:min(len(data), 8)]), "GIF8"):
		return "gif"
	case len(data) > 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "webp"
	}
	return ""
}
