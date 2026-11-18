package reflow

import (
	"fmt"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/pdf"
)

// TestJoinSpans joins lines with a space, except where a hyphen splits a
// word.
func TestJoinSpans(t *testing.T) {
	words := vocabulary{}
	words.add("Twenty-three islands and a well-known harbour; the wellknown one.")
	cases := [][3]string{
		{"written down their obser-", "vations for years", "written down their observations for years"},
		{"in all, twenty-", "three islands", "in all, twenty-three islands"},
		{"it is well-", "known here", "it is wellknown here"},
		{"a well-", "Known harbour", "a well- Known harbour"},
		{"the years 1914-", "1918 were hard", "the years 1914- 1918 were hard"},
		{"soft­", "ware", "software"},
		{"end of line ", " start of next", "end of line start of next"},
		{"a dash -", "then more", "a dash - then more"},
	}
	for _, c := range cases {
		joined := joinSpans([]span{{text: c[0]}}, []span{{text: c[1]}}, words)
		if got := (&element{spans: joined}).text(); got != c[2] {
			t.Errorf("join(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	styled := joinSpans([]span{{text: "plain "}, {text: "bold", bold: true}},
		[]span{{text: "bold too", bold: true}, {text: " plain"}}, nil)
	if len(styled) != 3 || styled[1].text != "bold bold too" {
		t.Errorf("styled join = %+v", styled)
	}
}

// TestPatterns checks the patterns that recognise page numbers, list
// markers, lines of a table of contents and numbered headings.
func TestPatterns(t *testing.T) {
	numbers := map[string]bool{
		"12": true, "- 12 -": true, "Page 3 of 10": true, "xiv": true, "3/40": true, "[7]": true,
		"1984 was": false, "Chapter 2": false, "mix": false, "12345": false, "": false,
	}
	for text, want := range numbers {
		if got := pageNumber.MatchString(text); got != want {
			t.Errorf("pageNumber(%q) = %v, want %v", text, got, want)
		}
	}
	markers := map[string]bool{
		"1. First": true, "(a) First": true, "12) Twelfth": true, "iv. Fourth": true, "A. First": true,
		"1.5 metres": false, "1984 was": false, "a boat": false, "(see below) text": false,
	}
	for text, want := range markers {
		if got := numberMarker.MatchString(text); got != want {
			t.Errorf("numberMarker(%q) = %v, want %v", text, got, want)
		}
	}
	bullets := map[string]bool{"• item": true, "- item": true, "– item": true, "-item": false, "item - more": false}
	for text, want := range bullets {
		if got := bulletMarker.MatchString(text); got != want {
			t.Errorf("bulletMarker(%q) = %v, want %v", text, got, want)
		}
	}
	if got := leader.ReplaceAllString("3.1 Shell Syntax . . . . . . . . 5", ""); got != "3.1 Shell Syntax" {
		t.Errorf("leader left %q", got)
	}
	if leader.MatchString("Wait... what? 5") {
		t.Error("leader matched an ellipsis")
	}
	depths := map[string]int{"2 Scope": 0, "2.1 Scope": 1, "2.1.3. Scope": 2, "Scope": 0, "1984": 0}
	for title, want := range depths {
		if got := numberDepth(title); got != want {
			t.Errorf("numberDepth(%q) = %d, want %d", title, got, want)
		}
	}
}

// run returns a run of ten point text at the given position.
func run(text string, x, y float64) pdf.Run {
	return pdf.Run{Text: text, X: x, Y: y, Width: 5 * float64(len(text)), Size: 10, Upright: true}
}

// TestFurniture tells running headers, footers and page numbers from
// the text of the pages.
func TestFurniture(t *testing.T) {
	var margins [][]segment
	var pages [][]pdf.Run
	for i := range 6 {
		title := "Chapter the First"
		if i%2 == 1 {
			title = "THE BOOK"
		}
		runs := []pdf.Run{
			run(title, 72, 40), run(fmt.Sprint(100+i), 500, 40),
			run(fmt.Sprintf("First line of page %c", 'a'+i), 72, 90),
			run("Body text", 72, 400), run("Footer note", 72, 760),
		}
		pages = append(pages, runs)
		margins = append(margins, marginSegments(runs, 792))
	}
	for i, runs := range pages {
		var kept []string
		for _, r := range withoutFurniture(runs, i, margins) {
			kept = append(kept, r.Text)
		}
		want := fmt.Sprintf("First line of page %c|Body text", 'a'+i)
		if got := strings.Join(kept, "|"); got != want {
			t.Errorf("page %d keeps %q, want %q", i, got, want)
		}
	}
	alone := []pdf.Run{run("A Title", 72, 40), run("7", 500, 40), run("Body text", 72, 400), run("Footer note", 72, 760)}
	if kept := withoutFurniture(alone, 0, [][]segment{marginSegments(alone, 792)}); len(kept) != 3 {
		t.Errorf("a single page keeps %d runs, want all but the page number", len(kept))
	}
}

// TestArrange reads two columns under a full width heading in order.
func TestArrange(t *testing.T) {
	var runs []pdf.Run
	runs = append(runs, run("A heading across both columns of the page here", 72, 100))
	for i := range 4 {
		runs = append(runs, run(fmt.Sprintf("left %d left left left left left", i), 72, 140+14*float64(i)))
		runs = append(runs, run(fmt.Sprintf("right %d right right right right", i), 330, 140+14*float64(i)))
	}
	runs = append(runs, run("A closing line below the two columns of the page", 72, 260))
	var order []string
	var flows []bool
	for _, b := range arrange(runs, 0) {
		flows = append(flows, b.flows)
		for _, ln := range b.lines {
			order = append(order, strings.Fields(ln.text())[0])
		}
	}
	if got := strings.Join(order, " "); got != "A left left left left right right right right A" {
		t.Errorf("reading order = %q", got)
	}
	if fmt.Sprint(flows) != "[true false true false]" {
		t.Errorf("flows = %v", flows)
	}
}

// TestNavigationNesting closes nested lists where the levels fall.
func TestNavigationNesting(t *testing.T) {
	b := &book{language: "en", entries: []entry{
		{title: "One"}, {title: "One A", level: 1, id: "a1"}, {title: "Deep", level: 3, id: "a2"},
		{title: "Two", section: 1}, {title: "Three & more", section: 2},
	}}
	nav := string(b.navigation())
	want := `<ol>
<li><a href="section0001.xhtml">One</a>
<ol>
<li><a href="section0001.xhtml#a1">One A</a>
<ol>
<li><a href="section0001.xhtml#a2">Deep</a></li>
</ol>
</li>
</ol>
</li>
<li><a href="section0002.xhtml">Two</a></li>
<li><a href="section0003.xhtml">Three &amp; more</a></li>
</ol>`
	if !strings.Contains(nav, want) {
		t.Errorf("navigation:\n%s\nwant it to hold:\n%s", nav, want)
	}
	if got := strings.Count(string(b.ncx()), "<navPoint"); got != 5 || strings.Count(string(b.ncx()), "</navPoint>") != 5 {
		t.Errorf("NCX has %d navPoint elements, want 5 opened and closed", got)
	}
}

// TestTimestamp formats the dates of PDFs as EPUB timestamps.
func TestTimestamp(t *testing.T) {
	dates := map[string]string{
		"D:20240229103000+01'00'": "2024-02-29T10:30:00Z", "D:1999": "1999-01-01T00:00:00Z",
		"D:20051231": "2005-12-31T00:00:00Z",
	}
	for date, want := range dates {
		if got := timestamp(pdfDate.FindStringSubmatch(date)); got != want {
			t.Errorf("timestamp(%q) = %q, want %q", date, got, want)
		}
	}
	if pdfDate.MatchString("yesterday") {
		t.Error("pdfDate matched text that is not a date")
	}
}
