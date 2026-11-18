// Package testbin provides the stand-in command line tools used by tests.
package testbin

import (
	"path/filepath"
	"runtime"
)

// root returns the root folder of the module.
func root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// Convert returns the path of a fake ebook-convert that copies its input
// to its output.
func Convert() string {
	return filepath.Join(root(), "testdata", "bin", "ebook-convert")
}
