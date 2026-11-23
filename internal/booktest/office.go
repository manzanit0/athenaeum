package booktest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
)

// docxTypes is the list of content types of a built Word document.
const docxTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="jpeg" ContentType="image/jpeg"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
</Types>
`

// docxRoot is the list of the parts a built Word document starts from.
const docxRoot = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>%s
</Relationships>
`

// docxThumbnail is the line of the root that names the preview picture.
const docxThumbnail = `
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/thumbnail" Target="docProps/thumbnail.jpeg"/>`

// docxParts is the list of the parts the main part of a built Word
// document refers to.
const docxParts = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>
</Relationships>
`

// docxStyles holds the styles of a built Word document: ordinary text
// and the first level of headings, named as Word names them.
const docxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:docDefaults><w:rPrDefault><w:rPr><w:sz w:val="24"/></w:rPr></w:rPrDefault></w:docDefaults>
  <w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
  <w:style w:type="paragraph" w:styleId="Heading1">
    <w:name w:val="heading 1"/><w:basedOn w:val="Normal"/>
    <w:pPr><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:sz w:val="32"/></w:rPr>
  </w:style>
</w:styles>
`

// JPEG returns a small JPEG image filled with the given colour.
func JPEG(fill color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 6))
	for y := range 6 {
		for x := range 4 {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// archive returns a ZIP file holding the given files, which are listed
// as name and content by turns so that their order is kept.
func archive(files ...string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i+1 < len(files); i += 2 {
		w, err := zw.Create(files[i])
		if err == nil {
			_, err = w.Write([]byte(files[i+1]))
		}
		if err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// DOCX returns b as a Word document: a heading and a paragraph for
// every chapter, with the details of the book in its properties. A
// cover, which has to be a JPEG, becomes its preview picture.
func DOCX(b Book) []byte {
	var body strings.Builder
	for _, name := range b.chapters() {
		fmt.Fprintf(&body, `<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>%s</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>It was a quiet morning in the library.</w:t></w:r></w:p>`, html.EscapeString(name))
	}
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		body.String() + `<w:sectPr/></w:body></w:document>`
	files := []string{
		"[Content_Types].xml", docxTypes,
		"_rels/.rels", fmt.Sprintf(docxRoot, ""),
		"docProps/core.xml", docxCore(b),
		"word/_rels/document.xml.rels", docxParts,
		"word/styles.xml", docxStyles,
		"word/document.xml", document,
	}
	if b.Cover != nil {
		files[3] = fmt.Sprintf(docxRoot, docxThumbnail)
		files = append(files, "docProps/thumbnail.jpeg", string(b.Cover))
	}
	return archive(files...)
}

// docxCore returns the part of a Word document that describes b.
func docxCore(b Book) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	sb.WriteString(`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
		`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/">`)
	element := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&sb, "<%[1]s>%[2]s</%[1]s>", name, html.EscapeString(value))
		}
	}
	element("dc:title", b.Title)
	element("dc:creator", b.Author)
	element("cp:keywords", strings.Join(b.Tags, ", "))
	element("dc:description", b.Description)
	element("dc:language", b.Language)
	sb.WriteString("</cp:coreProperties>\n")
	return sb.String()
}

// Pages returns a file laid out as Pages lays out its documents: the
// parts only Pages reads, which are stand-ins here, and the picture of
// the first page, which is the cover of b when it has one.
func Pages(b Book) []byte {
	preview := b.Cover
	if preview == nil {
		preview = JPEG(color.RGBA{R: 200, G: 200, B: 200, A: 255})
	}
	return archive(
		"Index/Document.iwa", "stand-in for the text of "+b.Title,
		"Index/Metadata.iwa", "stand-in",
		"Metadata/DocumentIdentifier", "00000000-0000-4000-8000-000000000000",
		"preview.jpg", string(preview),
		"preview-web.jpg", string(preview),
		"preview-micro.jpg", string(preview),
	)
}
