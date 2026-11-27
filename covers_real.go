//go:build !fakecovers

package main

import "github.com/manzanit0/athenaeum/internal/covers"

// coverFinder returns what searches the internet for covers; nil means
// Open Library and Google Books.
func coverFinder() *covers.Finder {
	return nil
}
