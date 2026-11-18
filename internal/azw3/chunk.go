package azw3

import (
	"bytes"
	"fmt"

	"github.com/manzanit0/athenaeum/internal/kf8"
)

// chunkSize is the size chunks are filled up to. Content is cut between
// elements, so a single large element can still exceed it.
const chunkSize = 8192

// splittable lists the elements the Kindle addresses by identifier.
// Content larger than a chunk is split inside them; anything else is
// kept whole.
var splittable = map[string]bool{
	"a": true, "abbr": true, "address": true, "article": true, "aside": true, "audio": true,
	"b": true, "bdo": true, "blockquote": true, "body": true, "button": true, "cite": true,
	"code": true, "dd": true, "del": true, "details": true, "dfn": true, "div": true, "dl": true,
	"dt": true, "em": true, "fieldset": true, "figcaption": true, "figure": true, "footer": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "header": true,
	"hgroup": true, "i": true, "ins": true, "kbd": true, "label": true, "legend": true,
	"li": true, "map": true, "mark": true, "meter": true, "nav": true, "ol": true,
	"output": true, "p": true, "pre": true, "progress": true, "q": true, "rp": true, "rt": true,
	"samp": true, "section": true, "select": true, "small": true, "span": true, "strong": true,
	"sub": true, "summary": true, "sup": true, "textarea": true, "time": true, "ul": true,
	"var": true, "video": true,
}

// fileBuilder lays a document out as a skeleton and its chunks.
type fileBuilder struct {
	// skeleton collects the markup that stays outside the chunks.
	skeleton bytes.Buffer
	// chunks holds the finished chunks.
	chunks []kf8.Chunk
	// pending is the chunk being filled.
	pending bytes.Buffer
	// pendingAt is where the pending chunk starts in the document.
	pendingAt int
	// pendingSelector is the selector of the pending chunk.
	pendingSelector string
	// size is the length of the document laid out so far, not counting
	// the pending chunk.
	size int
	// selector names the skeleton element the next chunk goes into.
	selector string
	// located is told where every identified element starts.
	located func(n *node, offset int)
}

// flush finishes the pending chunk.
func (fb *fileBuilder) flush() {
	if fb.pending.Len() == 0 {
		return
	}
	raw := bytes.Clone(fb.pending.Bytes())
	fb.chunks = append(fb.chunks, kf8.Chunk{Raw: raw, InsertAt: fb.pendingAt, Selector: fb.pendingSelector})
	fb.size += len(raw)
	fb.pending.Reset()
}

// structure adds markup to the skeleton.
func (fb *fileBuilder) structure(w *writer) {
	fb.flush()
	for _, m := range w.marks {
		fb.located(m.n, fb.size+m.offset)
	}
	fb.skeleton.Write(w.Bytes())
	fb.size += w.Len()
}

// content adds markup to the chunks, starting a new chunk when the
// pending one is full.
func (fb *fileBuilder) content(w *writer) {
	if fb.pending.Len() > 0 && fb.pending.Len()+w.Len() > chunkSize {
		fb.flush()
	}
	if fb.pending.Len() == 0 {
		fb.pendingAt, fb.pendingSelector = fb.size, fb.selector
	}
	for _, m := range w.marks {
		fb.located(m.n, fb.pendingAt+fb.pending.Len()+m.offset)
	}
	fb.pending.Write(w.Bytes())
}

// fill lays out the content of el, whose own tags are in the skeleton.
// Children too large for a chunk are opened in the skeleton and filled
// in turn when the Kindle can address them.
func (fb *fileBuilder) fill(el *node) {
	for _, child := range el.children {
		fb.selector = fmt.Sprintf("P-//*[@aid='%s']", el.aid)
		var whole writer
		whole.node(child)
		if whole.Len() > chunkSize && child.aid != "" && splittable[child.name] && len(child.children) > 0 {
			var start, end writer
			start.open(child)
			fb.structure(&start)
			fb.fill(child)
			end.close(child)
			fb.structure(&end)
		} else {
			fb.content(&whole)
		}
	}
}

// file returns the finished skeleton and chunks.
func (fb *fileBuilder) file() kf8.File {
	fb.flush()
	return kf8.File{Skeleton: bytes.Clone(fb.skeleton.Bytes()), Chunks: fb.chunks}
}
