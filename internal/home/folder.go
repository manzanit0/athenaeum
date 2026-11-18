package home

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Catalogue is the file whose presence makes a folder a library.
const Catalogue = "athenaeum.db"

// maxSuggestions bounds how many folders are suggested for a typed path.
const maxSuggestions = 12

// Kind says what a folder is, as far as opening it as a library goes.
type Kind string

const (
	// KindLibrary is a folder that holds a library.
	KindLibrary Kind = "library"
	// KindEmpty is an empty folder, where a new library can start.
	KindEmpty Kind = "empty"
	// KindOther is a folder that holds other files and no library.
	KindOther Kind = "other"
	// KindNew is a folder that does not exist yet and can be created.
	KindNew Kind = "new"
	// KindInvalid is a path no library can be opened at.
	KindInvalid Kind = "invalid"
)

// Check is the verdict on a path typed as the folder of a library.
type Check struct {
	// Path is the path as it will be used: absolute and tidied.
	Path string `json:"path"`
	// Kind says what is at the path.
	Kind Kind `json:"kind"`
	// Detail says why the path is invalid.
	Detail string `json:"detail,omitempty"`
}

// Resolve turns a typed path into the one to use: surrounding spaces
// are dropped, a leading tilde stands for the user's home folder, and
// the result is tidied. It fails for a path that is not absolute.
func Resolve(typed string) (string, error) {
	path, err := expand(typed)
	if err != nil {
		return "", err
	}
	return filepath.Clean(path), nil
}

// expand drops the spaces around a typed path and writes out a leading
// tilde. It fails for a path that is not absolute.
func expand(typed string) (string, error) {
	path := strings.TrimSpace(typed)
	if path == "~" || strings.HasPrefix(path, "~/") {
		user, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(user, path[1:])
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("give the whole path of the folder, starting with /")
	}
	return path, nil
}

// Inspect says what is at a typed path. It changes nothing on disk.
func Inspect(typed string) Check {
	path, err := Resolve(typed)
	if err != nil {
		return Check{Path: strings.TrimSpace(typed), Kind: KindInvalid, Detail: err.Error()}
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return inspectMissing(path)
	}
	if err != nil {
		return Check{Path: path, Kind: KindInvalid, Detail: "the folder cannot be read"}
	}
	if !info.IsDir() {
		return Check{Path: path, Kind: KindInvalid, Detail: "this is a file, not a folder"}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return Check{Path: path, Kind: KindInvalid, Detail: "the folder cannot be read"}
	}
	return Check{Path: path, Kind: kindOf(entries)}
}

// inspectMissing judges a path nothing exists at: it can be created
// when the folder it would sit in exists.
func inspectMissing(path string) Check {
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil || !parent.IsDir() {
		return Check{Path: path, Kind: KindInvalid, Detail: "the folder it would be created in does not exist"}
	}
	return Check{Path: path, Kind: KindNew}
}

// kindOf says what a folder with the given entries is. Hidden files,
// such as the ones a file manager leaves behind, and the list of
// libraries a home folder keeps do not count.
func kindOf(entries []fs.DirEntry) Kind {
	kind := KindEmpty
	for _, entry := range entries {
		name := entry.Name()
		if name == Catalogue {
			return KindLibrary
		}
		if !strings.HasPrefix(name, ".") && name != listFile {
			kind = KindOther
		}
	}
	return kind
}

// Suggest returns the folders a typed path could go on to name: the
// folders inside the last complete folder of the path whose names start
// with what follows it. Hidden folders are suggested only once a dot
// has been typed.
func Suggest(typed string) []string {
	path, err := expand(typed)
	if err != nil {
		return []string{}
	}
	dir, prefix := filepath.Split(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}
	found := []string{}
	for _, entry := range entries {
		if suggested(entry, dir, prefix) {
			found = append(found, filepath.Join(dir, entry.Name()))
		}
	}
	slices.Sort(found)
	return found[:min(len(found), maxSuggestions)]
}

// suggested reports whether entry, found in dir, is a folder whose name
// continues prefix.
func suggested(entry fs.DirEntry, dir, prefix string) bool {
	name := entry.Name()
	if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
		return false
	}
	if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
		return false
	}
	if entry.IsDir() {
		return true
	}
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && info.IsDir()
}
