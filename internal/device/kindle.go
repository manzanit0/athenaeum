// Package device manages the books stored on an Amazon Kindle that is
// mounted as a USB drive.
package device

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/manzanit0/athenaeum/internal/meta"
)

var (
	// ErrNotConnected is returned when no Kindle is mounted.
	ErrNotConnected = errors.New("no Kindle is connected")
	// ErrInvalidPath is returned for paths outside the documents folder.
	ErrInvalidPath = errors.New("path is not a book in the Kindle documents folder")
)

// documentsDir is the folder holding the books on a Kindle. The value, the
// supported formats and the sidecar extensions below follow the KINDLE2
// driver in calibre's src/calibre/devices/kindle/driver.py.
const documentsDir = "documents"

// formats lists the book formats a Kindle reads, in the order preferred
// when choosing which format of a book to send.
var formats = []string{"azw3", "mobi", "azw", "prc", "pdf", "txt", "azw4", "kfx", "azw1", "tpz", "pobi"}

// sidecarExts lists the extensions of the files a Kindle keeps next to a
// book for reading position, notes and page numbers.
var sidecarExts = []string{".mbp", ".tan", ".pdr", ".ea", ".apnx", ".phl", ".mbp1", ".mbs", ".han"}

// sidecarDirExt is the extension of the folder a Kindle keeps per book.
const sidecarDirExt = ".sdr"

// mountPoints lists glob patterns for the usual Kindle mount points.
var mountPoints = []string{
	"/Volumes/Kindle", "/kindle", "/mnt/kindle", "/media/Kindle",
	"/media/*/Kindle", "/run/media/*/Kindle",
}

// Formats returns the book formats a Kindle reads, most preferred first.
func Formats() []string {
	return slices.Clone(formats)
}

// Supports reports whether a Kindle reads format.
func Supports(format string) bool {
	return slices.Contains(formats, format)
}

// Status describes the connected Kindle.
type Status struct {
	// Connected reports whether a Kindle is mounted.
	Connected bool `json:"connected"`
	// Path is the mount point of the Kindle.
	Path string `json:"path"`
	// TotalBytes is the capacity of the Kindle storage.
	TotalBytes uint64 `json:"total_bytes"`
	// FreeBytes is the unused space on the Kindle storage.
	FreeBytes uint64 `json:"free_bytes"`
	// Formats lists the formats the Kindle reads.
	Formats []string `json:"formats"`
}

// File is a book stored on the Kindle.
type File struct {
	// Path is the file path relative to the Kindle root.
	Path string `json:"path"`
	// Name is the file name.
	Name string `json:"name"`
	// Format is the lower case file extension.
	Format string `json:"format"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
	// Modified is when the file was last written.
	Modified time.Time `json:"modified"`
	// Title is the book title read from the file or its name.
	Title string `json:"title"`
	// Authors lists the book authors read from the file or its name.
	Authors []string `json:"authors"`
}

// Kindle gives access to the books on a mounted Kindle.
type Kindle struct {
	// configured returns the mount point chosen by the user, or an empty
	// string to detect it.
	configured func() string
}

// New returns a Kindle whose mount point is given by configured, falling
// back to the usual mount points when that returns an empty string.
func New(configured func() string) *Kindle {
	return &Kindle{configured: configured}
}

// mount returns the mount point of the connected Kindle.
func (k *Kindle) mount() (string, bool) {
	candidates := []string{k.configured()}
	if candidates[0] == "" {
		candidates = nil
		for _, pattern := range mountPoints {
			matches, _ := filepath.Glob(pattern)
			candidates = append(candidates, matches...)
		}
	}
	for _, dir := range candidates {
		if info, err := os.Stat(filepath.Join(dir, documentsDir)); err == nil && info.IsDir() {
			return dir, true
		}
	}
	return "", false
}

// open returns a handle confining file access to the Kindle storage.
func (k *Kindle) open() (*os.Root, error) {
	dir, ok := k.mount()
	if !ok {
		return nil, ErrNotConnected
	}
	return os.OpenRoot(dir)
}

// Status reports whether a Kindle is connected and how full it is.
func (k *Kindle) Status() Status {
	status := Status{Formats: Formats()}
	dir, ok := k.mount()
	if !ok {
		status.Path = k.configured()
		return status
	}
	status.Connected = true
	status.Path = dir
	var fsStat syscall.Statfs_t
	if err := syscall.Statfs(dir, &fsStat); err == nil {
		status.TotalBytes = uint64(fsStat.Blocks) * uint64(fsStat.Bsize)
		status.FreeBytes = uint64(fsStat.Bavail) * uint64(fsStat.Bsize)
	}
	return status
}

// List returns the books in the Kindle documents folder.
func (k *Kindle) List() ([]File, error) {
	root, err := k.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := []File{}
	err = fs.WalkDir(root.FS(), documentsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasSuffix(d.Name(), sidecarDirExt) || isHidden(d.Name()) && p != documentsDir {
				return fs.SkipDir
			}
			return nil
		}
		format := strings.TrimPrefix(strings.ToLower(path.Ext(d.Name())), ".")
		if isHidden(d.Name()) || !Supports(format) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		m := meta.ReadHeader(filepath.Join(root.Name(), filepath.FromSlash(p)), d.Name())
		files = append(files, File{
			Path: p, Name: d.Name(), Format: format, Size: info.Size(),
			Modified: info.ModTime(), Title: m.Title, Authors: m.Authors,
		})
		return nil
	})
	return files, err
}

// isHidden reports whether name is a hidden file or folder.
func isHidden(name string) bool {
	return strings.HasPrefix(name, ".")
}

// Put copies the file at src into the Kindle documents folder as name and
// returns its path relative to the Kindle root.
func (k *Kindle) Put(name, src string) (string, error) {
	root, err := k.open()
	if err != nil {
		return "", err
	}
	defer root.Close()
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	dest := path.Join(documentsDir, path.Base(filepath.ToSlash(name)))
	partial := dest + ".partial"
	out, err := root.Create(partial)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = root.Rename(partial, dest)
	}
	if err != nil {
		root.Remove(partial)
		return "", err
	}
	return dest, nil
}

// Open opens the book at rel, a path relative to the Kindle root.
func (k *Kindle) Open(rel string) (*os.File, error) {
	if !validPath(rel) {
		return nil, ErrInvalidPath
	}
	root, err := k.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(rel)
}

// Delete removes the book at rel, a path relative to the Kindle root,
// together with the sidecar files the Kindle keeps for it.
func (k *Kindle) Delete(rel string) error {
	if !validPath(rel) {
		return ErrInvalidPath
	}
	root, err := k.open()
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Remove(rel); err != nil {
		return err
	}
	base := strings.TrimSuffix(rel, path.Ext(rel))
	for _, ext := range sidecarExts {
		root.Remove(base + ext)
	}
	return root.RemoveAll(base + sidecarDirExt)
}

// validPath reports whether rel names a book file inside the documents
// folder.
func validPath(rel string) bool {
	format := strings.TrimPrefix(strings.ToLower(path.Ext(rel)), ".")
	return filepath.IsLocal(rel) && path.Clean(rel) == rel &&
		strings.HasPrefix(rel, documentsDir+"/") && Supports(format)
}
