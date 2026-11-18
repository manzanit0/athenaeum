package pdf

// maxBookmarks bounds how many bookmarks are read.
const maxBookmarks = 20000

// Info holds what a document says about itself.
type Info struct {
	// Title is the title of the document.
	Title string
	// Author is the name of the author.
	Author string
	// Subject is the subject of the document.
	Subject string
	// Keywords holds the keywords as one string.
	Keywords string
	// Created is the creation date in PDF date format.
	Created string
	// Modified is the modification date in PDF date format.
	Modified string
	// Language is the natural language of the document.
	Language string
}

// Bookmark is one entry of the document outline.
type Bookmark struct {
	// Title is the text of the entry.
	Title string
	// Level is the nesting depth, starting at zero.
	Level int
	// Page is the index of the page the entry points at, or -1.
	Page int
	// Top is the vertical position on that page in display space, or a
	// negative number when the entry does not say.
	Top float64
}

// Info returns what the document says about itself.
func (d *Document) Info() Info {
	info := d.dict(d.trailer["Info"])
	return Info{
		Title:    d.text(info["Title"]),
		Author:   d.text(info["Author"]),
		Subject:  d.text(info["Subject"]),
		Keywords: d.text(info["Keywords"]),
		Created:  d.text(info["CreationDate"]),
		Modified: d.text(info["ModDate"]),
		Language: d.text(d.catalog()["Lang"]),
	}
}

// Outline returns the bookmarks of the document in reading order.
func (d *Document) Outline() []Bookmark {
	var out []Bookmark
	names := d.namedDestinations()
	d.walkOutline(d.dict(d.catalog()["Outlines"])["First"], 0, names, map[int]bool{}, &out)
	return out
}

// walkOutline appends the bookmark item, its children and the siblings
// after it to out.
func (d *Document) walkOutline(
	item any, level int, names map[string]any, visited map[int]bool, out *[]Bookmark,
) {
	for item != nil && len(*out) < maxBookmarks && level < maxDepth {
		if ref, ok := item.(Ref); ok {
			if visited[ref.Num] {
				return
			}
			visited[ref.Num] = true
		}
		dict := d.dict(item)
		if dict == nil {
			return
		}
		dest := dict["Dest"]
		if action := d.dict(dict["A"]); dest == nil && d.name(action["S"]) == "GoTo" {
			dest = action["D"]
		}
		page, top := d.destination(dest, names, 0)
		*out = append(*out, Bookmark{Title: d.text(dict["Title"]), Level: level, Page: page, Top: top})
		d.walkOutline(dict["First"], level+1, names, visited, out)
		item = dict["Next"]
	}
}

// destination resolves a destination to a page index and a vertical
// position in display space.
func (d *Document) destination(obj any, names map[string]any, depth int) (int, float64) {
	if depth > 4 {
		return -1, -1
	}
	switch v := d.resolve(obj).(type) {
	case Name:
		return d.destination(names[string(v)], names, depth+1)
	case String:
		return d.destination(names[string(v)], names, depth+1)
	case Dict:
		return d.destination(v["D"], names, depth+1)
	case Array:
		return d.explicit(v)
	}
	return -1, -1
}

// explicit resolves a destination given as an array.
func (d *Document) explicit(dest Array) (int, float64) {
	if len(dest) == 0 {
		return -1, -1
	}
	index := -1
	switch target := dest[0].(type) {
	case Ref:
		if found, ok := d.pageIndex[target.Num]; ok {
			index = found
		}
	case int64:
		index = int(target)
	}
	if index < 0 || index >= len(d.pages) {
		return -1, -1
	}
	at := map[Name]int{"XYZ": 3, "FitH": 2, "FitBH": 2, "FitR": 5}
	i, known := at[d.name(dest[min(1, len(dest)-1)])]
	if !known || i >= len(dest) {
		return index, -1
	}
	top, ok := d.number(dest[i])
	if !ok {
		return index, -1
	}
	_, y := d.pages[index].base().apply(d.pages[index].box[0], top)
	return index, y
}

// namedDestinations collects the named destinations of the document.
func (d *Document) namedDestinations() map[string]any {
	names := map[string]any{}
	for key, dest := range d.dict(d.catalog()["Dests"]) {
		names[string(key)] = dest
	}
	tree := d.dict(d.catalog()["Names"])["Dests"]
	d.walkNames(tree, names, map[int]bool{}, 0)
	return names
}

// walkNames adds the entries of the name tree node to names.
func (d *Document) walkNames(node any, names map[string]any, visited map[int]bool, depth int) {
	if ref, ok := node.(Ref); ok {
		if visited[ref.Num] {
			return
		}
		visited[ref.Num] = true
	}
	dict := d.dict(node)
	if dict == nil || depth > maxDepth || len(names) > maxBookmarks {
		return
	}
	pairs := d.array(dict["Names"])
	for i := 0; i+1 < len(pairs); i += 2 {
		if key, ok := d.resolve(pairs[i]).(String); ok {
			names[string(key)] = pairs[i+1]
		}
	}
	for _, kid := range d.array(dict["Kids"]) {
		d.walkNames(kid, names, visited, depth+1)
	}
}
