// Command fixtures writes the sample ebooks used by the end-to-end tests
// into the folder given as its only argument.
package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"

	"github.com/manzanit0/athenaeum/internal/booktest"
)

// main writes every fixture file.
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: fixtures <folder>")
		os.Exit(2)
	}
	teal := booktest.PNG(color.RGBA{R: 24, G: 96, B: 110, A: 255})
	files := map[string][]byte{
		"left-hand.epub": booktest.EPUB(booktest.Book{
			Title: "The Left Hand of Darkness", Author: "Ursula K. Le Guin", Publisher: "Ace Books",
			Language: "en", ISBN: "9780441478125", Series: "Hainish Cycle",
			Description: "A lone envoy visits the planet Winter.",
			Tags:        []string{"Science Fiction"}, Cover: teal,
		}),
		"almanac.epub": booktest.EPUB(booktest.Book{
			Title: "The Tidewater Almanac", Author: "Ines Marlow", Language: "en",
			Chapters: []string{"Spring Tides", "Neap Tides", "Storm Surge"},
		}),
		"tide-tables.pdf": booktest.TextPDF(booktest.Book{
			Title: "Tide Tables", Author: "Ines Marlow",
			Chapters: []string{"Spring Tides", "Neap Tides", "Storm Surge"},
		}),
		"kindred.epub":     booktest.EPUB(booktest.Book{Title: "Kindred", Author: "Octavia E. Butler", Language: "en"}),
		"piranesi.epub":    booktest.EPUB(booktest.Book{Title: "Piranesi", Author: "Susanna Clarke", Language: "en"}),
		"stoner.epub":      booktest.EPUB(booktest.Book{Title: "Stoner", Author: "John Williams", Language: "en"}),
		"beloved.epub":     booktest.EPUB(booktest.Book{Title: "Beloved", Author: "Toni Morrison", Language: "en"}),
		"ficciones.epub":   booktest.EPUB(booktest.Book{Title: "Ficciones", Author: "Jorge Luis Borges", Language: "es"}),
		"middlemarch.epub": booktest.EPUB(booktest.Book{Title: "Middlemarch", Author: "George Eliot", Language: "en"}),
		"cosmicomics.epub": booktest.EPUB(booktest.Book{Title: "Cosmicomics", Author: "Italo Calvino", Language: "en"}),
		"lighthouse.epub":  booktest.EPUB(booktest.Book{Title: "The Lighthouse Keepers", Author: "Mara Quill", Language: "en"}),
		"faros.epub":       booktest.EPUB(booktest.Book{Title: "Faros del norte", Author: "Mara Quill", Language: "es"}),
		"knots.pdf":        booktest.PDF(booktest.Book{Title: "Knots and Splices", Author: "Eli Tarn"}),
		"harbour.epub":     booktest.EPUB(booktest.Book{Title: "Harbour Lights", Author: "Nell Ward", Language: "en"}),
		"anchors.epub":     booktest.EPUB(booktest.Book{Title: "Anchors Aweigh", Author: "Abel Finch", Language: "en"}),
		"mooring.epub":     booktest.EPUB(booktest.Book{Title: "Mooring Lines", Author: "Cora Pike", Language: "en"}),
		"skerries.epub":    booktest.EPUB(booktest.Book{Title: "Letters from the Skerries", Author: "Tove Aldane", Language: "en"}),
		"solaris.mobi":     booktest.MOBI(booktest.Book{Title: "Solaris", Author: "Stanislaw Lem", Tags: []string{"Science Fiction"}}),
		"roadside.mobi":    booktest.MOBI(booktest.Book{Title: "Roadside Picnic", Author: "Arkady Strugatsky"}),
		"handbook.pdf":     booktest.PDF(booktest.Book{Title: "Typography Handbook", Author: "Robert Bringhurst"}),
		"cover.png":        booktest.PNG(color.RGBA{R: 150, G: 40, B: 60, A: 255}),
		"malware.exe":      []byte("MZ"),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(os.Args[1], name), data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
