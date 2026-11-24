//go:build fakecovers

package main

import (
	"github.com/manzanit0/athenaeum/internal/covers"
	"github.com/manzanit0/athenaeum/internal/covers/coverstest"
)

// coverFinder returns a stand-in for the websites that covers are found
// on. It only exists in binaries built with the fakecovers tag, which the
// end-to-end tests use.
func coverFinder() *covers.Finder {
	return coverstest.Finder()
}
