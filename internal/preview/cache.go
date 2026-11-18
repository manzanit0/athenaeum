package preview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Cache keeps the EPUB renditions made to preview books stored in other
// formats. It can be deleted at any time; renditions are made again when
// they are next needed.
type Cache struct {
	// Dir is the folder holding the renditions.
	Dir string
}

// Path returns where the rendition of the file src of the book uuid
// belongs. The name changes whenever the file does, so a rendition never
// outlives the file it was made from.
func (c *Cache) Path(uuid, src string) (string, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s%d-%d.epub", prefix(uuid, format(src)), info.Size(), info.ModTime().UnixNano())
	return filepath.Join(c.Dir, name), nil
}

// Temp returns the path of a new empty file in the cache in which a
// rendition can be written before it is moved into place.
func (c *Cache) Temp() (string, error) {
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(c.Dir, "making-*.epub")
	if err != nil {
		return "", err
	}
	return f.Name(), f.Close()
}

// Drop removes the renditions of the book uuid made from files of the
// given format, or all of its renditions when format is empty.
func (c *Cache) Drop(uuid, format string) error {
	entries, err := os.ReadDir(c.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var failed error
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix(uuid, format)) {
			failed = errors.Join(failed, os.Remove(filepath.Join(c.Dir, entry.Name())))
		}
	}
	return failed
}

// format returns the lower case extension of file without its dot.
func format(file string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(file)), ".")
}

// prefix returns the start of the names of the renditions of the book
// uuid made from files of the given format; an empty format matches all.
func prefix(uuid, format string) string {
	if format == "" {
		return uuid + "."
	}
	return uuid + "." + format + "."
}
