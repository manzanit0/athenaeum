package restore

import (
	"archive/zip"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/pdf"
)

// textRun returns a run of text at the given position.
func textRun(x, y float64, text string, bold, italic bool) pdf.Run {
	return pdf.Run{Text: text, X: x, Y: y, Width: float64(len(text)) * 6, Size: 12, Bold: bold, Italic: italic, Upright: true}
}

func TestPageTextKeepsEmphasisGapsAndTheSpacesBetweenWords(t *testing.T) {
	big := textRun(72, 80, "A Title", false, false)
	big.Size = 24
	got := pageText([]pdf.Run{
		big,
		textRun(72, 120, "I. Part one", true, false),
		textRun(72, 160, "a)\t  First ", false, false),
		textRun(138, 160, "item", false, false),
		textRun(72, 174, "plain ", false, false),
		textRun(108, 174, "slanted words", false, true),
		textRun(186, 174, " and ", false, false),
		textRun(216, 174, "both", true, true),
		textRun(240, 174, ".", false, false),
	}, 12)
	want := "(size 24) A Title\n\n**I. Part one**\n\na) First item\nplain *slanted words* and ***both***.\n"
	if got != want {
		t.Errorf("pageText =\n%q\nwant\n%q", got, want)
	}
}

// written writes the blocks as a book and returns its files by name.
func writtenFiles(t *testing.T, blocks []Block, details meta.Metadata) map[string]string {
	t.Helper()
	d := newDraft()
	if err := d.save(1, blocks, nil); err != nil {
		t.Fatal(err)
	}
	d.assemble()
	dst := filepath.Join(t.TempDir(), "out.epub")
	if _, err := writeBook(dst, d.elements, details, "", ""); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(data)
	}
	return out
}

func TestBookOpensWithATitlePageMadeFromWhatTheBookPrints(t *testing.T) {
	book := writtenFiles(t, []Block{
		{Kind: kindAuthor, Text: "San Claudio de la Colombière"},
		{Kind: kindTitle, Text: "**El Abandono confiado a la Divina Providencia**"},
		{Kind: kindTitle, Text: "El abandono confiado a la Divina Providencia"},
		{Kind: kindImprint, Text: "Oviedo, 1892"},
		{Kind: kindEpigraph, Text: "*Ductus est Jesus ut tentaretur.*"},
		{Kind: kindEpigraph, Text: "Matth. IV, 1"},
		{Kind: kindDetail, Text: "Traducción de *N. N.*"},
		{Kind: kindHeading, Level: 1, Text: "I. Verdades consoladoras."},
		{Kind: kindParagraph, Text: "Una de las **verdades** mejor *establecidas*."},
	}, meta.Metadata{Title: "El abandono", Authors: []string{"Someone Else"}, Language: "es"})
	page := book["OEBPS/title.xhtml"]
	for _, want := range []string{
		`<h1 class="title">El Abandono confiado a la Divina Providencia</h1>` + "\n" + `<p class="author">San Claudio de la Colombière</p>`,
		`<p class="detail">Traducción de <i>N. N.</i></p>` + "\n" +
			`<blockquote class="epigraph" epub:type="epigraph">` + "\n" +
			`<p><i>Ductus est Jesus ut tentaretur.</i></p>` + "\n" + `<p>Matth. IV, 1</p>` + "\n" + `</blockquote>` + "\n" +
			`<p class="imprint first">Oviedo, 1892</p>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the title page lacks %s:\n%s", want, page)
		}
	}
	if strings.Count(page, "<h1") != 1 || strings.Contains(page, "Someone Else") {
		t.Errorf("the title page repeats the title or takes the author from elsewhere:\n%s", page)
	}
	first := book["OEBPS/section0001.xhtml"]
	if strings.Contains(first, "Colombière") || !strings.Contains(first, "Una de las <b>verdades</b> mejor <i>establecidas</i>.") {
		t.Errorf("the first chapter is not the text with its emphasis:\n%s", first)
	}
	if opf := book["OEBPS/content.opf"]; strings.Index(opf, `idref="title`) > strings.Index(opf, `idref="section0001`) {
		t.Errorf("the title page does not come first:\n%s", opf)
	}
}

func TestBookTakesItsTitlePageFromItsDetailsWhenNoneIsPrinted(t *testing.T) {
	book := writtenFiles(t, []Block{{Kind: kindParagraph, Text: "Text."}},
		meta.Metadata{Title: "Soundings", Authors: []string{"Hugh Carrow"}, Publisher: "Quay Press", Published: "1871-03"})
	page := book["OEBPS/title.xhtml"]
	for _, want := range []string{`<h1 class="title">Soundings</h1>`, `<p class="author">Hugh Carrow</p>`, `<p class="imprint first">Quay Press</p>`, `<p class="imprint">1871</p>`} {
		if !strings.Contains(page, want) {
			t.Errorf("the title page lacks %s:\n%s", want, page)
		}
	}
}

func TestListsKeepTheirNesting(t *testing.T) {
	book := writtenFiles(t, []Block{
		{Kind: kindItem, Level: 1, Text: "**I. Verdades consoladoras.**"},
		{Kind: kindItem, Level: 2, Text: "a) Confiemos en la sabiduría de Dios."},
		{Kind: kindItem, Level: 2, Text: "b) Cuando Dios nos prueba."},
		{Kind: kindItem, Level: 1, Text: "**II. Las adversidades**"},
		{Kind: kindItem, Level: 2, Text: "a) Hay que confiar."},
		{Kind: kindParagraph, Text: "After the list."},
		{Kind: kindItem, Text: "a bulleted item"},
	}, meta.Metadata{Title: "T"})
	got := book["OEBPS/section0001.xhtml"]
	want := `<ul>
<li class="plain"><b>I. Verdades consoladoras.</b>
<ul>
<li class="plain">a) Confiemos en la sabiduría de Dios.</li>
<li class="plain">b) Cuando Dios nos prueba.</li>
</ul>
</li>
<li class="plain"><b>II. Las adversidades</b>
<ul>
<li class="plain">a) Hay que confiar.</li>
</ul>
</li>
</ul>
<p>After the list.</p>
<ul>
<li>a bulleted item</li>
</ul>
`
	if !strings.Contains(got, want) {
		t.Errorf("the lists came out as:\n%s", got)
	}
}

// pastoral returns a draft shaped like a pastoral letter of 1892: a
// title page in capitals, a salutation set large above two numbered
// chapters that open with words in capitals, and a seal at the end.
func pastoral(t *testing.T) *draft {
	t.Helper()
	d := newDraft()
	err := d.save(1, []Block{
		{Kind: kindTitle, Text: "EL DEMONIO Y LA TENTACIÓN."},
		{Kind: kindAuthor, Text: "RMO. SR. OBISPO DE OVIEDO"},
		{Kind: kindHeading, Level: 1, Text: "EL OBISPO DE OVIEDO A SUS AMADOS HIJOS"},
		{Kind: kindHeading, Level: 2, Text: "I."},
		{Kind: kindParagraph, Text: "CÓMO ENTRA EL DEMONIO EN EL MUNDO? Aquel demonio de pies de cabrito, nota en MATTH. IV, 1."},
		{Kind: kindHeading, Level: 2, Text: "II."},
		{Kind: kindParagraph, Text: "EN QUÉ CONSISTE LA TENTACION? La inclinación que sentimos."},
		{Kind: kindParagraph, Text: "POR MANDADO DE S. E. RMA., EL OBISPO MI SEÑOR"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.assemble()
	return d
}

func TestHeadingsComeWithWhatTheyOpen(t *testing.T) {
	headings := pastoral(t).headings()
	if len(headings) != 3 {
		t.Fatalf("found %d headings, want 3", len(headings))
	}
	if headings[0].length != 0 || headings[0].opens != "" {
		t.Errorf("the salutation is said to open %q", headings[0].opens)
	}
	if !strings.HasPrefix(headings[1].opens, "CÓMO ENTRA EL DEMONIO") || headings[1].length < 80 {
		t.Errorf("the first chapter is said to open %q over %d characters", headings[1].opens, headings[1].length)
	}
}

func TestCapitalsSetForLooksAreFoundAndOnlyTheirCaseMayChange(t *testing.T) {
	d := pastoral(t)
	found := map[string]stretch{}
	for _, s := range d.stretches() {
		found[s.text] = s
	}
	for _, want := range []string{
		"EL DEMONIO Y LA TENTACIÓN.", "RMO. SR. OBISPO DE OVIEDO", "CÓMO ENTRA EL DEMONIO EN EL MUNDO",
		"MATTH. IV", "EN QUÉ CONSISTE LA TENTACION", "POR MANDADO DE S. E. RMA., EL OBISPO MI SEÑOR",
	} {
		if _, ok := found[want]; !ok {
			t.Errorf("%q was not found among %v", want, d.stretches())
		}
	}
	if _, ok := found["EL OBISPO DE OVIEDO A SUS AMADOS HIJOS"]; ok {
		t.Error("a heading is listed among the stretches; headings are looked at by themselves")
	}
	if err := d.recase(found["EN QUÉ CONSISTE LA TENTACION"], "En qué consiste la tentación"); err != nil {
		t.Errorf("an accent the capitals left out was turned down: %v", err)
	}
	if err := d.recase(found["CÓMO ENTRA EL DEMONIO EN EL MUNDO"], "Cómo entró el demonio en el mundo"); err == nil {
		t.Error("a change to the words went through")
	}
	if err := d.recase(found["EL DEMONIO Y LA TENTACIÓN."], "El demonio y la tentación"); err != nil {
		t.Error(err)
	}
	if got := d.elements[6].Text; got != "En qué consiste la tentación? La inclinación que sentimos." {
		t.Errorf("the paragraph reads %q", got)
	}
	if got := d.elements[0].Text; got != "El demonio y la tentación" {
		t.Errorf("the title reads %q", got)
	}
}

func TestALineThatIsNoChapterBecomesTextAndTheChaptersMoveUp(t *testing.T) {
	d := pastoral(t)
	for id, level := range map[string]int{"h1": 0, "h2": 1, "h3": 1} {
		text := map[string]string{"h1": "El obispo de Oviedo a sus amados hijos", "h2": "I.", "h3": "II."}[id]
		if err := d.retitle(id, level, text); err != nil {
			t.Fatal(err)
		}
	}
	_, body := front(d.elements)
	sections := divide(body, "El demonio y la tentación")
	if len(sections) != 3 || sections[1].title != "I." || sections[2].title != "II." {
		t.Fatalf("the book is divided into %d sections: %+v", len(sections), sections)
	}
	if first := sections[0].elements[0]; first.Kind != kindParagraph || first.Text != "El obispo de Oviedo a sus amados hijos" {
		t.Errorf("the salutation became %+v", first.Block)
	}
}

func TestTextAheadOfTheFirstChapterIsListedInTheContents(t *testing.T) {
	book := writtenFiles(t, []Block{
		{Kind: kindTitle, Text: "El demonio y la tentación"},
		{Kind: kindParagraph, Text: "A sus amados hijos."},
		{Kind: kindHeading, Level: 1, Text: "I."},
		{Kind: kindParagraph, Text: "Text."},
	}, meta.Metadata{Title: "El demonio y la tentación"})
	nav := book["OEBPS/nav.xhtml"]
	for _, want := range []string{`href="title.xhtml"`, `<a href="section0001.xhtml">El demonio y la tentación</a>`, `href="section0002.xhtml#h1"`} {
		if !strings.Contains(nav, want) {
			t.Errorf("the contents lack %s:\n%s", want, nav)
		}
	}
}

func TestBookCarriesTheFontsItIsSetIn(t *testing.T) {
	book := writtenFiles(t, []Block{{Kind: kindParagraph, Text: "Text."}}, meta.Metadata{Title: "T"})
	style, opf := book["OEBPS/style.css"], book["OEBPS/content.opf"]
	if !strings.Contains(style, `body { font-family: "Ibarra Real Nova", serif; }`) {
		t.Errorf("the text is not set in the font:\n%s", style)
	}
	for _, f := range faces {
		if len(book["OEBPS/fonts/"+f.file]) < 50_000 {
			t.Errorf("%s is not in the book", f.file)
		}
		if !strings.Contains(style, "src: url(fonts/"+f.file+")") || !strings.Contains(opf, `href="fonts/`+f.file+`" media-type="font/ttf"`) {
			t.Errorf("%s is not named by the style sheet and the package:\n%s\n%s", f.file, style, opf)
		}
	}
	if !strings.Contains(book["OEBPS/fonts/OFL.txt"], "SIL OPEN FONT LICENSE") {
		t.Error("the licence of the fonts did not come with them")
	}
}
