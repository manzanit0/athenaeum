// Package vault keeps secrets encrypted in the database.
//
// Values are sealed with AES-256-GCM under a key that is never written to
// the database: it is derived from a passphrase given to the application,
// or, without one, read from a file kept beside the database.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/manzanit0/athenaeum/internal/store"
)

const (
	// keyFile is the name of the file holding the key when no passphrase
	// is given.
	keyFile = "secret.key"
	// keyBytes is the length of the key.
	keyBytes = 32
	// saltName is the name the salt of the passphrase is stored under.
	saltName = "vault.salt"
	// saltBytes is the length of the salt.
	saltBytes = 16
	// iterations is the work factor of the key derivation.
	iterations = 600_000
)

// ErrUnreadable is returned for a value that was sealed under another
// key, or that has been tampered with.
var ErrUnreadable = errors.New("the stored secret cannot be read with the current key")

// Vault seals values into the database and opens them again.
type Vault struct {
	// store is the database holding the sealed values.
	store *store.Store
	// aead seals and opens values.
	aead cipher.AEAD
}

// Open returns the vault of the database st. With a passphrase, the key
// is derived from it and a salt kept in the database. Without one, the
// key is read from a file in dir, which is made on first use and can
// only be read by its owner.
func Open(st *store.Store, dir, passphrase string) (*Vault, error) {
	key, err := fileKey(filepath.Join(dir, keyFile))
	if passphrase != "" {
		key, err = derivedKey(st, passphrase)
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{store: st, aead: aead}, nil
}

// derivedKey returns the key that passphrase gives with the salt of the
// database st, which is made on first use.
func derivedKey(st *store.Store, passphrase string) ([]byte, error) {
	salt, err := st.Secret(saltName)
	if errors.Is(err, store.ErrNotFound) {
		salt = make([]byte, saltBytes)
		rand.Read(salt)
		err = st.SetSecret(saltName, salt)
	}
	if err != nil {
		return nil, err
	}
	return pbkdf2.Key(sha256.New, passphrase, salt, iterations, keyBytes)
}

// fileKey returns the key kept in the file at path, making the file with
// a new key when it does not exist.
func fileKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, keyBytes)
		rand.Read(key)
		err = writeKey(path, key)
	}
	if err != nil {
		return nil, err
	}
	if len(key) != keyBytes {
		return nil, fmt.Errorf("%s does not hold a %d byte key", path, keyBytes)
	}
	return key, nil
}

// writeKey writes key to a new file at path that only its owner can read.
func writeKey(path string, key []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(key)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Put seals value and keeps it under name, replacing what was there.
func (v *Vault) Put(name string, value []byte) error {
	nonce := make([]byte, v.aead.NonceSize())
	rand.Read(nonce)
	return v.store.SetSecret(name, v.aead.Seal(nonce, nonce, value, []byte(name)))
}

// Get returns the value kept under name and whether there is one. A
// value that cannot be opened gives ErrUnreadable.
func (v *Vault) Get(name string) ([]byte, bool, error) {
	sealed, err := v.store.Secret(name)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	size := v.aead.NonceSize()
	if len(sealed) < size {
		return nil, true, ErrUnreadable
	}
	value, err := v.aead.Open(nil, sealed[:size], sealed[size:], []byte(name))
	if err != nil {
		return nil, true, ErrUnreadable
	}
	return value, true, nil
}

// Delete forgets the value kept under name, if any.
func (v *Vault) Delete(name string) error {
	return v.store.DeleteSecret(name)
}
