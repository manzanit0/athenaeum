package pdf

// maxPages bounds how many pages a document may have.
const maxPages = 100000

// Page is one page of a document.
type Page struct {
	// doc is the document the page belongs to.
	doc *Document
	// dict is the page dictionary.
	dict Dict
	// resources holds the fonts and images the page uses.
	resources Dict
	// box is the visible area in default user space: left, bottom,
	// right, top.
	box [4]float64
	// rotate is the clockwise rotation of the page in degrees.
	rotate int
}

// inherited holds the page attributes passed down the page tree.
type inherited struct {
	// resources is the inherited resource dictionary.
	resources any
	// mediaBox is the inherited media box.
	mediaBox any
	// cropBox is the inherited crop box.
	cropBox any
	// rotate is the inherited rotation.
	rotate any
}

// NumPages returns the number of pages.
func (d *Document) NumPages() int {
	return len(d.pages)
}

// Page returns the page with the given zero based index.
func (d *Document) Page(index int) *Page {
	return d.pages[index]
}

// loadPages walks the page tree and lists its pages in order.
func (d *Document) loadPages() {
	d.pageIndex = map[int]int{}
	d.walkPages(d.catalog()["Pages"], inherited{}, map[int]bool{}, 0)
}

// walkPages adds the pages below node, which inherits the attributes in
// from. The visited set stops reference loops.
func (d *Document) walkPages(node any, from inherited, visited map[int]bool, depth int) {
	if ref, ok := node.(Ref); ok {
		if visited[ref.Num] {
			return
		}
		visited[ref.Num] = true
	}
	dict := d.dict(node)
	if dict == nil || depth > maxDepth || len(d.pages) >= maxPages {
		return
	}
	from = from.merge(dict)
	kids := d.array(dict["Kids"])
	if kids == nil && d.name(dict["Type"]) != "Pages" {
		d.addPage(node, dict, from)
		return
	}
	for _, kid := range kids {
		d.walkPages(kid, from, visited, depth+1)
	}
}

// merge returns the attributes in after applying the ones set by dict.
func (in inherited) merge(dict Dict) inherited {
	if v := dict["Resources"]; v != nil {
		in.resources = v
	}
	if v := dict["MediaBox"]; v != nil {
		in.mediaBox = v
	}
	if v := dict["CropBox"]; v != nil {
		in.cropBox = v
	}
	if v := dict["Rotate"]; v != nil {
		in.rotate = v
	}
	return in
}

// addPage appends the page described by dict.
func (d *Document) addPage(node any, dict Dict, from inherited) {
	page := &Page{doc: d, dict: dict, resources: d.dict(from.resources)}
	page.box = [4]float64{0, 0, 612, 792}
	for _, candidate := range []any{from.cropBox, from.mediaBox} {
		if box, ok := d.rect(candidate); ok {
			page.box = box
			break
		}
	}
	page.rotate = ((d.integer(from.rotate, 0)/90)%4 + 4) % 4 * 90
	if ref, ok := node.(Ref); ok {
		d.pageIndex[ref.Num] = len(d.pages)
	}
	d.pages = append(d.pages, page)
}

// rect reads a rectangle, normalising it so the first corner is the
// lower left one. It reports false for anything that is not a usable
// rectangle.
func (d *Document) rect(obj any) ([4]float64, bool) {
	arr := d.array(obj)
	if len(arr) != 4 {
		return [4]float64{}, false
	}
	var r [4]float64
	for i := range r {
		v, ok := d.number(arr[i])
		if !ok {
			return r, false
		}
		r[i] = v
	}
	if r[0] > r[2] {
		r[0], r[2] = r[2], r[0]
	}
	if r[1] > r[3] {
		r[1], r[3] = r[3], r[1]
	}
	return r, r[2]-r[0] > 1 && r[3]-r[1] > 1
}

// Size returns the width and height of the page as displayed.
func (p *Page) Size() (float64, float64) {
	w, h := p.box[2]-p.box[0], p.box[3]-p.box[1]
	if p.rotate == 90 || p.rotate == 270 {
		return h, w
	}
	return w, h
}

// base returns the matrix that maps default user space to display space,
// whose origin is the top left corner of the page as displayed and whose
// vertical axis points down.
func (p *Page) base() matrix {
	x0, y0, x1, y1 := p.box[0], p.box[1], p.box[2], p.box[3]
	switch p.rotate {
	case 90:
		return matrix{0, 1, 1, 0, -y0, -x0}
	case 180:
		return matrix{-1, 0, 0, 1, x1, -y0}
	case 270:
		return matrix{0, -1, -1, 0, y1, x1}
	}
	return matrix{1, 0, 0, -1, -x0, y1}
}

// contentData returns the decoded content streams of the page joined
// into one.
func (p *Page) contentData() []byte {
	d := p.doc
	var parts []any
	switch c := d.resolve(p.dict["Contents"]).(type) {
	case *Stream:
		parts = []any{c}
	case Array:
		parts = c
	}
	var out []byte
	for _, part := range parts {
		stream := d.stream(part)
		if stream == nil {
			continue
		}
		if data, err := stream.Data(); err == nil {
			out = append(out, data...)
			out = append(out, '\n')
		}
	}
	return out
}
