//go:build !fakemodel

package main

import (
	"context"

	"github.com/manzanit0/athenaeum/internal/restore"
)

// restoreModel returns the way to reach the language model that
// restores books; nil means Anthropic's.
func restoreModel() func(context.Context, string) (restore.Model, error) {
	return nil
}
