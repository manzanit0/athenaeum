package docx_test

import (
	"context"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/manzanit0/athenaeum/internal/docx"
	"github.com/manzanit0/athenaeum/internal/preview"
)

// blockTag matches the start of the elements whose text the sample test
// prints.
var blockTag = regexp.MustCompile(`<(h[1-6]|p|li|div|ul|ol|table|blockquote)[ >]`)

// anyTag matches any tag.
var anyTag = regexp.MustCompile(`<[^>]+>`)

// TestSample converts the Word document named by the DOCX_SAMPLE
// environment variable, checks that the result is well formed and
// prints an outline of it, as an aid for judging conversions of real
// files. It is skipped when the variable is unset.
func TestSample(t *testing.T) {
	src := os.Getenv("DOCX_SAMPLE")
	if src == "" {
		t.Skip("DOCX_SAMPLE is not set")
	}
	dst := strings.TrimSuffix(src, ".docx") + ".epub"
	report, err := docx.Convert(context.Background(), src, dst, docx.Options{Cover: os.Getenv("DOCX_COVER")})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("report: %+v", *report)
	if _, err := preview.ReadOutline(dst); err != nil {
		t.Errorf("the preview cannot open the book: %v", err)
	}
	files := unpack(t, dst)
	checkXML(t, files)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !strings.HasSuffix(name, ".xhtml") {
			t.Logf("== %s (%d bytes)", name, len(files[name]))
			continue
		}
		t.Logf("== %s", name)
		for _, row := range strings.Split(files[name], "\n") {
			tag := blockTag.FindStringSubmatch(row)
			if tag == nil {
				continue
			}
			text := anyTag.ReplaceAllString(row, "")
			if limit := 150; len(text) > limit && os.Getenv("DOCX_FULL") == "" {
				text = text[:limit/2] + " ... " + text[len(text)-limit/2:]
			}
			t.Logf("  [%s] %s", tag[1], text)
		}
	}
}
