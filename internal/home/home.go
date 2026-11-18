// Package home remembers which libraries the application knows and
// which one is open. A library is a folder that holds everything about
// one collection of books; the home folder holds the list of them and is
// itself the first library.
package home

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// listFile is the name of the file in the home folder that holds the
// list of libraries.
const listFile = "libraries.json"

// ErrOpen is returned when asked to forget the library that is open.
var ErrOpen = errors.New("the open library cannot be removed from the list")

// list is the content of the list file.
type list struct {
	// Current is the folder of the library to open, or empty for none.
	Current string `json:"current"`
	// Libraries holds the folders of the known libraries, oldest first.
	Libraries []string `json:"libraries"`
}

// Home is the folder where the list of libraries is kept.
type Home struct {
	// dir is the absolute path of the home folder.
	dir string
	// mu guards list.
	mu sync.Mutex
	// list is what the list file holds.
	list list
}

// Open reads the list of libraries kept in dir, creating the folder when
// it is missing. A home without a list knows one library, itself.
func Open(dir string) (*Home, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	h := &Home{dir: dir, list: list{Current: dir, Libraries: []string{dir}}}
	data, err := os.ReadFile(filepath.Join(dir, listFile))
	if errors.Is(err, fs.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &h.list); err != nil {
		return nil, err
	}
	return h, nil
}

// Dir returns the home folder, which is also the default library.
func (h *Home) Dir() string {
	return h.dir
}

// Current returns the folder of the library to open, or an empty string
// when there is none.
func (h *Home) Current() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.list.Current
}

// Libraries returns the folders of the known libraries, oldest first.
func (h *Home) Libraries() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.list.Libraries)
}

// Use makes the library in path the one to open, adding it to the list
// when it is new.
func (h *Home) Use(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !slices.Contains(h.list.Libraries, path) {
		h.list.Libraries = append(h.list.Libraries, path)
	}
	h.list.Current = path
	return h.save()
}

// Forget removes the library in path from the list and leaves its folder
// alone. A library named as the one to open is no longer; the caller
// makes sure it is not open.
func (h *Home) Forget(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.list.Libraries = slices.DeleteFunc(h.list.Libraries, func(known string) bool { return known == path })
	if h.list.Current == path {
		h.list.Current = ""
	}
	return h.save()
}

// save writes the list file, through a temporary file so that a crash
// does not leave half a list behind.
func (h *Home) save() error {
	data, err := json.MarshalIndent(h.list, "", "  ")
	if err != nil {
		return err
	}
	name := filepath.Join(h.dir, listFile)
	if err := os.WriteFile(name+".tmp", append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(name+".tmp", name)
}
