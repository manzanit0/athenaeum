package vault_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/manzanit0/athenaeum/internal/store"
	"github.com/manzanit0/athenaeum/internal/vault"
)

// open returns the database in dir and its vault under passphrase.
func open(t *testing.T, dir, passphrase string) (*store.Store, *vault.Vault) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	v, err := vault.Open(st, dir, passphrase)
	if err != nil {
		t.Fatal(err)
	}
	return st, v
}

// TestVault checks that values come back as they went in, that the
// database holds none of them in the clear, and that another key, another
// name or a changed value cannot open them.
func TestVault(t *testing.T) {
	for _, passphrase := range []string{"", "open sesame"} {
		dir := t.TempDir()
		st, v := open(t, dir, passphrase)
		secret := []byte("session-id and master key")
		if _, found, err := v.Get("mega"); found || err != nil {
			t.Fatalf("empty vault gave found=%t, %v", found, err)
		}
		if err := v.Put("mega", secret); err != nil {
			t.Fatal(err)
		}
		if got, found, err := v.Get("mega"); !found || err != nil || !bytes.Equal(got, secret) {
			t.Errorf("Get = %q, %t, %v", got, found, err)
		}
		sealed, err := st.Secret("mega")
		if err != nil || bytes.Contains(sealed, secret) {
			t.Errorf("the database holds the secret in the clear: %v", err)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "test.db"))
		if bytes.Contains(raw, secret) {
			t.Error("the database file holds the secret in the clear")
		}

		_, again := open(t, dir, passphrase)
		if got, _, err := again.Get("mega"); err != nil || !bytes.Equal(got, secret) {
			t.Errorf("after reopening Get = %q, %v", got, err)
		}
		_, other := open(t, dir, passphrase+" changed")
		if _, found, err := other.Get("mega"); !found || !errors.Is(err, vault.ErrUnreadable) {
			t.Errorf("another key gave found=%t, %v; want ErrUnreadable", found, err)
		}
		if err := st.SetSecret("elsewhere", sealed); err != nil {
			t.Fatal(err)
		}
		if _, _, err := v.Get("elsewhere"); !errors.Is(err, vault.ErrUnreadable) {
			t.Errorf("a value moved to another name gave %v, want ErrUnreadable", err)
		}
		sealed[len(sealed)-1] ^= 1
		st.SetSecret("mega", sealed)
		if _, _, err := v.Get("mega"); !errors.Is(err, vault.ErrUnreadable) {
			t.Errorf("a changed value gave %v, want ErrUnreadable", err)
		}
		if err := v.Delete("mega"); err != nil {
			t.Fatal(err)
		}
		if _, found, _ := v.Get("mega"); found {
			t.Error("the value is still there after Delete")
		}
	}
}

// TestKeyFile checks that the key file is private to its owner.
func TestKeyFile(t *testing.T) {
	dir := t.TempDir()
	open(t, dir, "")
	info, err := os.Stat(filepath.Join(dir, "secret.key"))
	if err != nil || info.Mode().Perm() != 0o600 || info.Size() != 32 {
		t.Errorf("key file = %v, %v; want 32 bytes readable by the owner only", info, err)
	}
}
