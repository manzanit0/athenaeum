package restore

import (
	"embed"
	"fmt"
	"strings"

	"github.com/manzanit0/athenaeum/internal/epub"
)

// fontFamily is the name of the typeface restored books are set in:
// Ibarra Real Nova, a revival of the type Joaquín Ibarra cut for the
// Quijote of 1780, under the SIL Open Font License.
const fontFamily = "Ibarra Real Nova"

// fontFolder is the folder of a book its fonts are kept in.
const fontFolder = "fonts/"

// fontLicence is the file holding the licence of the fonts, which has
// to go wherever they go.
const fontLicence = "OFL.txt"

// fontFiles holds the fonts and their licence.
//
//go:embed fonts/*.ttf fonts/OFL.txt
var fontFiles embed.FS

// face is one cut of the typeface.
type face struct {
	// file is the name of the font file.
	file string
	// weight is the weight of the cut, as CSS names it.
	weight string
	// style is the style of the cut, as CSS names it.
	style string
}

// faces lists the cuts a book carries: enough for text, emphasis and
// headings.
var faces = []face{
	{"IbarraRealNova-Regular.ttf", "normal", "normal"},
	{"IbarraRealNova-Italic.ttf", "normal", "italic"},
	{"IbarraRealNova-Bold.ttf", "bold", "normal"},
	{"IbarraRealNova-BoldItalic.ttf", "bold", "italic"},
}

// fontRules returns the style rules that name the fonts of a book and
// set its text in them. A reader set to its own typeface still uses
// that one.
func fontRules() string {
	var sb strings.Builder
	for _, f := range faces {
		fmt.Fprintf(&sb, "@font-face { font-family: %q; font-weight: %s; font-style: %s; src: url(%s%s); }\n",
			fontFamily, f.weight, f.style, fontFolder, f.file)
	}
	fmt.Fprintf(&sb, "body { font-family: %q, serif; }\n", fontFamily)
	return sb.String()
}

// addFonts adds the fonts and their licence to the book.
func addFonts(out *epub.Writer) error {
	for _, f := range faces {
		data, err := fontFiles.ReadFile(fontFolder + f.file)
		if err != nil {
			return err
		}
		out.Add(fontFolder+f.file, "font/ttf", data)
	}
	licence, err := fontFiles.ReadFile(fontFolder + fontLicence)
	if err != nil {
		return err
	}
	out.Add(fontFolder+fontLicence, "text/plain", licence)
	return nil
}
