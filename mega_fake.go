//go:build fakemega

package main

import (
	"os"

	"github.com/manzanit0/athenaeum/internal/megasync"
	"github.com/manzanit0/athenaeum/internal/megasync/megatest"
)

// megaService returns a stand-in for MEGA whose cloud drive is the folder
// named by FAKE_MEGA_ROOT. It only exists in binaries built with the
// fakemega tag, which the end-to-end tests use.
func megaService() megasync.Service {
	return &megatest.Service{Root: os.Getenv("FAKE_MEGA_ROOT"), DelayFile: os.Getenv("FAKE_MEGA_DELAY_FILE")}
}
