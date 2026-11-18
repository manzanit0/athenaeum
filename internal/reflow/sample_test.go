package reflow_test

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/manzanit0/athenaeum/internal/preview"
	"github.com/manzanit0/athenaeum/internal/reflow"
)

// blockTag matches the start of the elements whose text the sample test
// prints.
var blockTag = regexp.MustCompile(`<(h[1-3]|p|li|div|ul|ol)[ >]`)

// anyTag matches any tag.
var anyTag = regexp.MustCompile(`<[^>]+>`)

// TestSample converts the PDF named by the REFLOW_SAMPLE environment
// variable, checks that the result is well formed and prints an outline
// of it, as an aid for judging conversions of real files. It is skipped
// when the variable is unset.
func TestSample(t *testing.T) {
	src := os.Getenv("REFLOW_SAMPLE")
	if src == "" {
		t.Skip("REFLOW_SAMPLE is not set")
	}
	dst := strings.TrimSuffix(src, ".pdf") + ".epub"
	report, err := reflow.Convert(context.Background(), src, dst, reflow.Options{Cover: os.Getenv("REFLOW_COVER")})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("report: %+v", *report)
	if _, err := preview.ReadOutline(dst); err != nil {
		t.Errorf("the preview cannot open the book: %v", err)
	}
	checkXML(t, unpack(t, dst))
	zr, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".xhtml") && !strings.HasSuffix(f.Name, ".ncx") {
			t.Logf("== %s (%d bytes)", f.Name, f.UncompressedSize64)
			continue
		}
		r, _ := f.Open()
		data, _ := io.ReadAll(r)
		r.Close()
		t.Logf("== %s", f.Name)
		for _, row := range strings.Split(string(data), "\n") {
			tag := blockTag.FindStringSubmatch(row)
			if tag == nil {
				continue
			}
			text := anyTag.ReplaceAllString(row, "")
			if limit := 150; len(text) > limit && os.Getenv("REFLOW_FULL") == "" {
				text = text[:limit/2] + " ... " + text[len(text)-limit/2:]
			}
			t.Logf("  [%s] %s", tag[1], text)
		}
	}
}

// TestSampleFolder converts every PDF in the folder named by the
// REFLOW_SAMPLE_DIR environment variable and prints one line for each,
// to see how a collection of real files fares. It is skipped when the
// variable is unset.
func TestSampleFolder(t *testing.T) {
	dir := os.Getenv("REFLOW_SAMPLE_DIR")
	if dir == "" {
		t.Skip("REFLOW_SAMPLE_DIR is not set")
	}
	sources, err := filepath.Glob(filepath.Join(dir, "*.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range sources {
		dst := strings.TrimSuffix(src, ".pdf") + ".epub"
		started := time.Now()
		report, err := reflow.Convert(context.Background(), src, dst, reflow.Options{})
		took := time.Since(started).Round(time.Millisecond)
		if err != nil {
			t.Logf("%-28s %8s  %v", filepath.Base(src), took, err)
			continue
		}
		if _, err := preview.ReadOutline(dst); err != nil {
			t.Errorf("%s: the preview cannot open the book: %v", src, err)
		}
		checkXML(t, unpack(t, dst))
		info, _ := os.Stat(dst)
		t.Logf("%-28s %8s  pages %-4d sections %-3d images %-3d %7d bytes  %s", filepath.Base(src), took,
			report.Pages, report.Sections, report.Images, info.Size(), strings.Join(report.Warnings, " "))
	}
}
