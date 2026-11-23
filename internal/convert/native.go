package convert

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/manzanit0/athenaeum/internal/azw3"
	"github.com/manzanit0/athenaeum/internal/docx"
	"github.com/manzanit0/athenaeum/internal/kf8"
	"github.com/manzanit0/athenaeum/internal/meta"
	"github.com/manzanit0/athenaeum/internal/reflow"
)

// Engines that can carry out a conversion.
const (
	// EngineNative is the converters built into the application.
	EngineNative = "native"
	// EngineCalibre is calibre's ebook-convert.
	EngineCalibre = "calibre"
)

// nativeTargets maps the formats the built in converters read to the
// formats they can turn them into.
var nativeTargets = map[string][]string{
	"azw3": {"epub"},
	"docx": {"azw3", "epub"},
	"epub": {"azw3"},
	"pdf":  {"azw3", "epub"},
}

// keptHints gives, for every format the library only keeps, what to do
// to get a book that can be read: neither the built in converters nor
// calibre open these formats.
var keptHints = map[string]string{
	"pages": "open the book in the Pages app, export it as EPUB or Word, and add that file",
}

// ErrKept is returned when a book in a format the library only keeps
// is asked to be read, converted or sent to the e-reader.
var ErrKept = errors.New("this file can only be kept and downloaded")

// kindleFormats lists the formats whose files may hold a book in KF8,
// which the built in converters can turn into an EPUB. Only AZW3 files
// always do; the others may be in the older MOBI format.
var kindleFormats = []string{"azw3", "mobi", "azw", "prc"}

// errOldFormat explains that a Kindle book cannot be read without
// calibre.
var errOldFormat = fmt.Errorf("%w: this Kindle book is in a form only calibre can read", ErrUnavailable)

// Request describes one conversion.
type Request struct {
	// In is the path of the book to convert.
	In string
	// Out is the path to write, whose extension names the target format.
	Out string
	// Metadata is embedded in the result.
	Metadata meta.Metadata
	// Cover is the path of the cover image to embed; empty means none.
	Cover string
	// ID identifies the book on the e-reader; empty derives one.
	ID string
}

// Report says how a conversion was carried out.
type Report struct {
	// Engine is the engine that did the conversion.
	Engine string `json:"engine"`
	// Warnings lists what had to be left out of the result.
	Warnings []string `json:"warnings"`
}

// KeptFormats returns the formats the library only keeps.
func KeptFormats() []string {
	return slices.Sorted(maps.Keys(keptHints))
}

// Kept reports whether the library only keeps books in the given
// format, without being able to open them.
func Kept(format string) bool {
	_, kept := keptHints[format]
	return kept
}

// KeptError returns the error that says a file in the given format can
// only be kept, with what to do about it.
func KeptError(format string) error {
	return fmt.Errorf("%w: %s", ErrKept, keptHints[format])
}

// NativeTargets returns, for every format the built in converters read,
// the formats they can turn it into.
func NativeTargets() map[string][]string {
	out := maps.Clone(nativeTargets)
	for from, targets := range out {
		out[from] = slices.Clone(targets)
	}
	return out
}

// formatOf returns the lower case extension of file without its dot.
func formatOf(file string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(file)), ".")
}

// native reports whether the built in converters should handle the
// conversion between the two formats.
func (t *Tools) native(from, to string) bool {
	if t.PreferCalibre && t.CanConvert() {
		return false
	}
	return slices.Contains(nativeTargets[from], to)
}

// CanRead reports whether a book in the given format can, or may, be
// turned into an EPUB for reading: with calibre any format can, without
// it the formats the built in converters read can and the Kindle
// formats are worth trying. The formats that are only kept never can.
func (t *Tools) CanRead(format string) bool {
	if Kept(format) {
		return false
	}
	return t.CanConvert() || ReadsNatively(format) || slices.Contains(kindleFormats, format)
}

// ReadsNatively reports whether books in the given format are always
// readable by the built in converters.
func ReadsNatively(format string) bool {
	return slices.Contains(nativeTargets[format], "epub")
}

// unpack turns the Kindle book of r into an EPUB with the built in
// converter. It reports false when the book is in a form only calibre
// reads and calibre is there to read it.
func (t *Tools) unpack(ctx context.Context, r Request) (*Report, bool, error) {
	unpacked, err := azw3.Unpack(ctx, r.In, r.Out, azw3.Options{Metadata: r.Metadata, Cover: r.Cover, ID: r.ID})
	switch {
	case err == nil:
		return &Report{Engine: EngineNative, Warnings: unpacked.Warnings}, true, nil
	case !errors.Is(err, kf8.ErrNotKF8) && !errors.Is(err, kf8.ErrCompression):
		return nil, true, err
	case !t.CanConvert():
		return nil, true, errOldFormat
	}
	return nil, false, nil
}

// Supports reports whether a book in the format from can be converted
// to the format to, by the built in converters or by calibre.
func (t *Tools) Supports(from, to string) bool {
	return from != to && !Kept(from) && (slices.Contains(nativeTargets[from], to) || t.CanConvert())
}

// intoEPUB turns the PDF or the Word document of r into an EPUB at dst
// with the built in converters and returns what they had to leave out.
func intoEPUB(ctx context.Context, from string, r Request, dst string) ([]string, error) {
	if from == "docx" {
		report, err := docx.Convert(ctx, r.In, dst, docx.Options{Metadata: r.Metadata, Cover: r.Cover, ID: r.ID})
		if err != nil {
			return nil, err
		}
		return report.Warnings, nil
	}
	report, err := reflow.Convert(ctx, r.In, dst, reflow.Options{Metadata: r.Metadata, Cover: r.Cover})
	if err != nil {
		return nil, err
	}
	return report.Warnings, nil
}

// Convert carries out the conversion r with the built in converters when
// they cover it and with calibre otherwise.
func (t *Tools) Convert(ctx context.Context, r Request) (*Report, error) {
	from, to := formatOf(r.In), formatOf(r.Out)
	if Kept(from) {
		return nil, KeptError(from)
	}
	if to == "epub" && slices.Contains(kindleFormats, from) && !(t.PreferCalibre && t.CanConvert()) {
		if report, done, err := t.unpack(ctx, r); done {
			return report, err
		}
	}
	if !t.native(from, to) || from == "azw3" {
		if !t.CanConvert() {
			return nil, ErrUnavailable
		}
		args := append([]string{r.In, r.Out}, metadataArgs(r.Metadata, r.Cover, "--series-index")...)
		return &Report{Engine: EngineCalibre, Warnings: []string{}}, run(ctx, t.ConvertBin, args...)
	}
	report := &Report{Engine: EngineNative, Warnings: []string{}}
	epub := r.In
	if from != "epub" {
		epub = r.Out
		if to != "epub" {
			staged, err := os.CreateTemp(filepath.Dir(r.Out), "staged-*.epub")
			if err != nil {
				return nil, err
			}
			staged.Close()
			defer os.Remove(staged.Name())
			epub = staged.Name()
		}
		warnings, err := intoEPUB(ctx, from, r, epub)
		if err != nil {
			return nil, err
		}
		report.Warnings = append(report.Warnings, warnings...)
	}
	if to == "azw3" {
		packed, err := azw3.Convert(ctx, epub, r.Out, azw3.Options{Metadata: r.Metadata, Cover: r.Cover, ID: r.ID})
		if err != nil {
			return nil, err
		}
		report.Warnings = append(report.Warnings, packed.Warnings...)
	}
	return report, nil
}
