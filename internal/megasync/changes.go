package megasync

import (
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/manzanit0/athenaeum/internal/library"
	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/store"
)

// Kinds of change to a book.
const (
	// KindAdd is a book the side did not have.
	KindAdd = "add"
	// KindUpdate is a book the side has whose details or files change.
	KindUpdate = "update"
	// KindRemove is a book the side no longer has.
	KindRemove = "remove"
)

// Ways a file, or the cover of a book, changes.
const (
	// FileAdd is a file the side did not have.
	FileAdd = "add"
	// FileReplace is a file whose content changes.
	FileReplace = "replace"
	// FileRemove is a file the side no longer has.
	FileRemove = "remove"
	// FileRename is a file that goes by another name or folder.
	FileRename = "rename"
)

// FileChange is what happens to one file of a side.
type FileChange struct {
	// Op is one of FileAdd, FileReplace, FileRemove or FileRename.
	Op string `json:"op"`
	// Path is where the file is, relative to the synced folders.
	Path string `json:"path"`
	// From is where a renamed file was.
	From string `json:"from,omitempty"`
	// Size is the number of bytes transferred.
	Size int64 `json:"size,omitempty"`
}

// Change is what happens to one book of a side.
type Change struct {
	// Kind is one of KindAdd, KindUpdate or KindRemove.
	Kind string `json:"kind"`
	// BookID identifies the book in this library, when it has it.
	BookID int64 `json:"book_id,omitempty"`
	// Title is the title of the book.
	Title string `json:"title"`
	// Authors are the authors of the book.
	Authors []string `json:"authors"`
	// Details reports whether the details of the book change.
	Details bool `json:"details"`
	// Cover is how the cover changes: FileAdd, FileReplace, FileRemove
	// or nothing.
	Cover string `json:"cover,omitempty"`
	// Files lists what happens to the ebook files of the book.
	Files []FileChange `json:"files"`
	// Bytes is the number of bytes transferred for the book.
	Bytes int64 `json:"bytes"`
}

// Summary describes, book by book, what a sync changes on one side.
type Summary struct {
	// Books lists the books that change, by title.
	Books []Change `json:"books"`
	// Other lists the changes to files that belong to no book.
	Other []FileChange `json:"other"`
}

// part is what a file is to the book it belongs to.
type part int

const (
	// partFile is one of the ebook files of the book.
	partFile part = iota
	// partDetails is the details file of the book.
	partDetails
	// partCover is the cover of the book.
	partCover
)

// owned says which book a file belongs to.
type owned struct {
	// book is the UUID of the book.
	book string
	// part is what the file is to the book.
	part part
}

// volume is what a sync knows about a book.
type volume struct {
	// id identifies the book in this library, when it has it.
	id int64
	// title is the title of the book.
	title string
	// authors are the authors of the book.
	authors []string
}

// catalog names the books the files of a sync belong to.
type catalog struct {
	// books holds the known books by UUID.
	books map[string]volume
	// files says which book each known file belongs to.
	files map[string]owned
	// had holds the UUIDs of the books the library had before the sync.
	had map[string]bool
	// adopts reports that an ebook of no book becomes a book of its own,
	// as it does when it reaches the library.
	adopts bool
}

// newCatalog returns the catalog of the given books and of the files
// deleted since the last upload.
func newCatalog(books []*store.Book, gone []store.Tombstone) *catalog {
	c := &catalog{books: map[string]volume{}, files: map[string]owned{}, had: map[string]bool{}}
	c.add(books, gone)
	for _, b := range books {
		c.had[b.UUID] = true
	}
	return c
}

// add makes the catalog know the given books and the files deleted since
// the last upload. What a deleted file says about its book only counts
// for a book the library no longer has.
func (c *catalog) add(books []*store.Book, gone []store.Tombstone) {
	for _, b := range books {
		c.books[b.UUID] = volume{id: b.ID, title: b.Title, authors: b.Authors}
		c.files[library.DetailsPath(b.UUID)] = owned{b.UUID, partDetails}
		if b.Cover != "" {
			c.files[library.CoverFile(b.Cover)] = owned{b.UUID, partCover}
		}
		for _, f := range b.Formats {
			c.files[path.Join(b.Path, f.Filename)] = owned{b.UUID, partFile}
		}
	}
	for _, t := range gone {
		c.claim(t.Path, t.Book)
		if _, known := c.books[t.Book]; !known && t.Book != "" && t.Title != "" {
			c.books[t.Book] = volume{title: t.Title, authors: t.Authors}
		}
	}
}

// claim records that the ebook file at rel belongs to the book identified
// by uuid, unless the file is already known.
func (c *catalog) claim(rel, uuid string) {
	if _, taken := c.owner(rel); taken || uuid == "" {
		return
	}
	c.files[rel] = owned{uuid, partFile}
}

// learn makes the catalog know what a details file says about its book.
func (c *catalog) learn(d library.Details) {
	if _, known := c.books[d.UUID]; !known {
		c.books[d.UUID] = volume{title: d.Title, authors: d.Authors}
	}
	for _, f := range d.Files {
		c.claim(f, d.UUID)
	}
}

// owner returns the book the file at rel belongs to, if any.
func (c *catalog) owner(rel string) (owned, bool) {
	if o, ok := c.files[rel]; ok {
		return o, true
	}
	uuid, details, ok := library.Kept(rel)
	switch {
	case !ok:
		return owned{}, false
	case details:
		return owned{uuid, partDetails}, true
	}
	return owned{uuid, partCover}, true
}

// under returns the known files below the folder dir, sorted.
func (c *catalog) under(dir string) []string {
	var files []string
	for rel := range c.files {
		if strings.HasPrefix(rel, dir+"/") {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	return files
}

// draft collects what happens to the files of one book.
type draft struct {
	// book is the UUID of the book, or nothing for an ebook that is a
	// book by itself.
	book string
	// title names an ebook that is a book by itself.
	title string
	// details is what happens to the details file, if anything.
	details string
	// cover lists what happens to the cover files.
	cover []string
	// files lists what happens to the ebook files.
	files []Action
	// all lists everything that happens, in order.
	all []Action
}

// record notes that a happens to the given part of the book.
func (d *draft) record(p part, a Action) {
	d.all = append(d.all, a)
	switch p {
	case partDetails:
		d.details = a.Op
	case partCover:
		d.cover = append(d.cover, a.Op)
	default:
		d.files = append(d.files, a)
	}
}

// kind returns what the collected changes amount to for the book, which
// the library had before the sync or not. A book new to the library
// whose details file does not travel was made from the ebooks that do.
func (d *draft) kind(had bool) string {
	decisive := d.details
	if d.book == "" {
		decisive = d.files[0].Op
	}
	arrives := !slices.ContainsFunc(d.all, func(a Action) bool { return a.Op == "delete" })
	switch {
	case decisive == "copy", decisive == "" && !had && arrives:
		return KindAdd
	case decisive == "delete":
		return KindRemove
	}
	return KindUpdate
}

// change describes the book the draft is about, known as v, which the
// library had before the sync or not.
func (d *draft) change(v volume, had bool) Change {
	out := Change{
		Kind: d.kind(had), BookID: v.id, Title: v.title, Authors: v.authors,
		Details: d.book != "" && d.details != "", Cover: coverChange(d.cover), Files: fileChanges(d.files),
	}
	if out.Authors == nil {
		out.Authors = []string{}
	}
	for _, a := range d.all {
		out.Bytes += transferred(a)
	}
	return out
}

// coverChange returns how a cover changes given what happens to the
// cover files of its book.
func coverChange(ops []string) string {
	added, removed := slices.Contains(ops, "copy"), slices.Contains(ops, "delete")
	switch {
	case slices.Contains(ops, "update"), added && removed:
		return FileReplace
	case added:
		return FileAdd
	case removed:
		return FileRemove
	}
	return ""
}

// extension returns the lower case extension of the file at rel.
func extension(rel string) string {
	return strings.ToLower(path.Ext(rel))
}

// fileChanges describes what happens to the ebook files of a book. A file
// that goes while another of the same format comes was renamed.
func fileChanges(actions []Action) []FileChange {
	out := []FileChange{}
	leaving := map[string]string{}
	for _, a := range actions {
		if _, seen := leaving[extension(a.Path)]; a.Op == "delete" && !seen {
			leaving[extension(a.Path)] = a.Path
		}
	}
	renamed := map[string]bool{}
	for _, a := range actions {
		from, moved := leaving[extension(a.Path)]
		switch {
		case a.Op == "copy" && moved && !renamed[from]:
			renamed[from] = true
			out = append(out, FileChange{Op: FileRename, Path: a.Path, From: from, Size: a.Size})
		case a.Op == "copy":
			out = append(out, FileChange{Op: FileAdd, Path: a.Path, Size: a.Size})
		case a.Op == "update":
			out = append(out, FileChange{Op: FileReplace, Path: a.Path, Size: a.Size})
		}
	}
	for _, a := range actions {
		if a.Op == "delete" && !renamed[a.Path] {
			out = append(out, FileChange{Op: FileRemove, Path: a.Path})
		}
	}
	return out
}

// plain describes a change to a file of no book.
func plain(a Action) FileChange {
	ops := map[string]string{"copy": FileAdd, "update": FileReplace, "delete": FileRemove}
	return FileChange{Op: ops[a.Op], Path: a.Path, Size: a.Size}
}

// ledger gathers the drafts of a summary in the order they first appear.
type ledger struct {
	// drafts holds the drafts by the book or the file they are about.
	drafts map[string]*draft
	// order lists the keys of drafts as they first appeared.
	order []string
}

// draft returns the draft filed under key, starting it when needed.
func (l *ledger) draft(key string, start draft) *draft {
	if d, ok := l.drafts[key]; ok {
		return d
	}
	l.drafts[key] = &start
	l.order = append(l.order, key)
	return &start
}

// file records the change a in the draft of the book it belongs to and
// reports whether it belongs to one. Deleting a folder is a change to
// every known file below it.
func (c *catalog) file(l *ledger, a Action) bool {
	if o, ok := c.owner(a.Path); ok {
		l.draft(o.book, draft{book: o.book}).record(o.part, a)
		return true
	}
	if a.Op == "delete" && c.fileBelow(l, a.Path) {
		return true
	}
	if _, ebook := meta.FormatOf(a.Path); !ebook || !c.adopts {
		return false
	}
	name := strings.TrimSuffix(a.Path, path.Ext(a.Path))
	l.draft("file:"+name, draft{title: path.Base(name)}).record(partFile, a)
	return true
}

// fileBelow records the deletion of the folder dir in the drafts of the
// books with files below it and reports whether there are any.
func (c *catalog) fileBelow(l *ledger, dir string) bool {
	below := c.under(dir)
	for _, rel := range below {
		o := c.files[rel]
		l.draft(o.book, draft{book: o.book}).record(o.part, Action{Op: "delete", Path: rel})
	}
	return len(below) > 0
}

// summarise describes, book by book, what the given changes to the files
// of one side amount to.
func (c *catalog) summarise(actions []Action) Summary {
	out := Summary{Books: []Change{}, Other: []FileChange{}}
	l := &ledger{drafts: map[string]*draft{}}
	for _, a := range actions {
		if a.Op != "mkdir" && !c.file(l, a) {
			out.Other = append(out.Other, plain(a))
		}
	}
	for _, key := range l.order {
		d := l.drafts[key]
		v, known := c.books[d.book]
		switch {
		case d.book == "":
			out.Books = append(out.Books, d.change(volume{title: d.title}, false))
		case known:
			out.Books = append(out.Books, d.change(v, c.had[d.book]))
		default:
			out.Other = append(out.Other, c.unnamed(d)...)
		}
	}
	sort.SliceStable(out.Books, func(i, j int) bool {
		return strings.ToLower(out.Books[i].Title) < strings.ToLower(out.Books[j].Title)
	})
	sort.SliceStable(out.Other, func(i, j int) bool { return out.Other[i].Path < out.Other[j].Path })
	return out
}

// unnamed describes, file by file, the changes of a book nothing is
// known about. The details and cover arriving in the library for such a
// book are left out: its ebooks are listed as books of their own.
func (c *catalog) unnamed(d *draft) []FileChange {
	var out []FileChange
	for _, a := range d.all {
		if !c.adopts || a.Op == "delete" {
			out = append(out, plain(a))
		}
	}
	return out
}
