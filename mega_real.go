//go:build !fakemega

package main

import "github.com/manzanit0/athenaeum/internal/megasync"

// megaService returns the way to reach MEGA.
func megaService() megasync.Service {
	return megasync.SDK{}
}
