//go:build fakemodel

package main

import (
	"context"
	"os"

	"github.com/manzanit0/athenaeum/internal/restore"
	"github.com/manzanit0/athenaeum/internal/restore/restoretest"
)

// restoreModel returns a stand-in for the language model that restores
// books. It declines a book after the sample with the reason kept in the
// file named by FAKE_MODEL_DECLINE_FILE, when that file exists. It only
// exists in binaries built with the fakemodel tag, which the end-to-end
// tests use.
func restoreModel() func(context.Context, string) (restore.Model, error) {
	return func(context.Context, string) (restore.Model, error) {
		reason, _ := os.ReadFile(os.Getenv("FAKE_MODEL_DECLINE_FILE"))
		return &restoretest.Model{Name: restore.DefaultModel, Decline: string(reason), Tokens: 1200}, nil
	}
}
