// Package convert transforms ebooks between formats and embeds metadata.
// EPUB to AZW3 and PDF to EPUB or AZW3 are done by the converters built
// into the application; everything else drives the calibre command line
// tools.
package convert

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/manzanit0/athenaeum/internal/meta"
)

// ErrUnavailable is returned when the required calibre tool is not
// installed.
var ErrUnavailable = errors.New("calibre command line tools are not installed")

// maxErrorOutput bounds how much tool output is kept in an error message.
const maxErrorOutput = 600

// outputFormats lists the formats offered as conversion targets.
var outputFormats = []string{"azw3", "docx", "epub", "fb2", "htmlz", "mobi", "pdf", "rtf", "txt"}

// embedFormats lists the formats ebook-meta can write metadata into.
var embedFormats = []string{"azw", "azw3", "azw4", "docx", "epub", "fb2", "htmlz", "kepub", "mobi", "odt", "pdf", "prc", "rtf"}

// OutputFormats returns the formats offered as conversion targets.
func OutputFormats() []string {
	return slices.Clone(outputFormats)
}

// Tools locates the calibre command line tools.
type Tools struct {
	// ConvertBin is the name or path of ebook-convert.
	ConvertBin string
	// MetaBin is the name or path of ebook-meta.
	MetaBin string
	// PreferCalibre hands calibre the conversions the built in
	// converters could do, when calibre is installed.
	PreferCalibre bool
}

// CanConvert reports whether ebook-convert is installed.
func (t *Tools) CanConvert() bool {
	_, err := exec.LookPath(t.ConvertBin)
	return err == nil
}

// CanEmbed reports whether ebook-meta is installed.
func (t *Tools) CanEmbed() bool {
	_, err := exec.LookPath(t.MetaBin)
	return err == nil
}

// Embed writes m and the cover image at cover into the ebook at file.
// Formats ebook-meta cannot write are left untouched.
func (t *Tools) Embed(ctx context.Context, file string, m meta.Metadata, cover string) error {
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(file)), ".")
	if !slices.Contains(embedFormats, format) {
		return nil
	}
	if !t.CanEmbed() {
		return ErrUnavailable
	}
	args := append([]string{file}, metadataArgs(m, cover, "--index")...)
	return run(ctx, t.MetaBin, args...)
}

// ExtractCover returns the cover of the ebook at file as found by
// ebook-meta, or nil when there is none.
func (t *Tools) ExtractCover(ctx context.Context, file string) *meta.Cover {
	if !t.CanEmbed() {
		return nil
	}
	out, err := os.CreateTemp("", "cover-*.jpg")
	if err != nil {
		return nil
	}
	out.Close()
	defer os.Remove(out.Name())
	if err := run(ctx, t.MetaBin, file, "--get-cover", out.Name()); err != nil {
		return nil
	}
	data, err := os.ReadFile(out.Name())
	if err != nil {
		return nil
	}
	ext := meta.ImageExt(data)
	if ext == "" {
		return nil
	}
	return &meta.Cover{Data: data, Ext: ext}
}

// metadataArgs builds the calibre options that set m and cover. indexFlag
// is the option naming the series index, which differs between tools.
func metadataArgs(m meta.Metadata, cover, indexFlag string) []string {
	var args []string
	add := func(flag, value string) {
		if value != "" {
			args = append(args, flag, value)
		}
	}
	add("--title", m.Title)
	add("--authors", strings.Join(m.Authors, " & "))
	add("--publisher", m.Publisher)
	add("--language", m.Language)
	add("--tags", strings.Join(m.Tags, ","))
	add("--isbn", m.ISBN)
	add("--comments", m.Description)
	add("--cover", cover)
	if m.Series != "" {
		add("--series", m.Series)
		add(indexFlag, strconv.FormatFloat(m.SeriesIndex, 'f', -1, 64))
	}
	return args
}

// run executes the command and returns an error carrying the tail of its
// output when it fails.
func run(ctx context.Context, bin string, args ...string) error {
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	text := strings.TrimSpace(string(out))
	if len(text) > maxErrorOutput {
		text = "..." + text[len(text)-maxErrorOutput:]
	}
	return fmt.Errorf("%s failed: %w: %s", filepath.Base(bin), err, text)
}
