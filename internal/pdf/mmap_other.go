//go:build !unix

package pdf

// load reads the file at path into memory.
func load(path string) ([]byte, func() error, error) {
	return loadWhole(path)
}
