// Package web embeds the built web interface.
package web

import (
	"embed"
	"io/fs"
)

// dist holds the files produced by the front-end build.
//
//go:embed all:dist
var dist embed.FS

// FS returns the built web interface rooted at its index page.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
