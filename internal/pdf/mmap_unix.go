//go:build unix

package pdf

import (
	"os"
	"syscall"
)

// load maps the file at path into memory, so large PDFs are paged in by
// the operating system instead of being copied onto the heap.
func load(path string) ([]byte, func() error, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if info.Size() == 0 {
		return nil, func() error { return nil }, nil
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(info.Size()), syscall.PROT_READ, syscall.MAP_PRIVATE)
	if err != nil {
		return loadWhole(path)
	}
	return data, func() error { return syscall.Munmap(data) }, nil
}
