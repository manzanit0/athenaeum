package megasync

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/manzanit0/mega-cli/pkg/mirror"
	"github.com/manzanit0/mega-cli/pkg/remote"
)

// SDK reaches MEGA through the Go packages of mega-cli.
type SDK struct{}

// SignIn implements Service.
func (SDK) SignIn(email, password, code string) (Session, error) {
	c, err := remote.Login(email, password, code, remote.Options{})
	switch {
	case errors.Is(err, remote.ErrMFARequired):
		return Session{}, ErrCodeRequired
	case err != nil:
		return Session{}, fmt.Errorf("%w: %v", ErrSignInFailed, err)
	}
	return Session{Email: email, ID: c.SessionID(), Key: c.MasterKey()}, nil
}

// Resume implements Service.
func (SDK) Resume(s Session) (Drive, error) {
	c, err := remote.Resume(s.ID, s.Key, remote.Options{})
	switch {
	case errors.Is(err, remote.ErrSessionExpired):
		return nil, ErrSessionEnded
	case err != nil:
		return nil, fmt.Errorf("reaching MEGA: %w", err)
	}
	return sdkDrive{c}, nil
}

// SignOut implements Service.
func (SDK) SignOut(s Session) error {
	return remote.Logout(s.ID)
}

// sdkDrive is the cloud drive behind a mega-cli client.
type sdkDrive struct {
	// c is the signed-in client.
	c *remote.Client
}

// options returns the options of a sync in either direction.
func options(del, dryRun bool, started func(Action)) mirror.Options {
	o := mirror.Options{Delete: del, DryRun: dryRun, Exclude: excludes}
	if started != nil {
		o.OnAction = func(a mirror.Action) {
			started(Action{Op: a.Op.String(), Path: a.Path, Size: a.Size})
		}
	}
	return o
}

// result converts the summary of a sync.
func result(r mirror.Result) Result {
	out := Result{
		Actions: make([]Action, 0, len(r.Actions)), DryRun: r.DryRun,
		Files: r.Files, Bytes: r.Bytes, Folders: r.Folders, Deleted: r.Deleted,
	}
	for _, a := range r.Actions {
		out.Actions = append(out.Actions, Action{Op: a.Op.String(), Path: a.Path, Size: a.Size})
	}
	return out
}

// Push implements Drive.
func (d sdkDrive) Push(local, folder string, del, dryRun bool, started func(Action)) (Result, error) {
	r, err := mirror.Up(d.c, local, remote.Parse(Absolute(folder)), options(del, dryRun, started))
	return result(r), err
}

// Pull implements Drive.
func (d sdkDrive) Pull(folder, local string, del, dryRun bool, started func(Action)) (Result, error) {
	r, err := mirror.Down(d.c, remote.Parse(Absolute(folder)), local, options(del, dryRun, started))
	return result(r), err
}

// Exists implements Drive.
func (d sdkDrive) Exists(path string) bool {
	_, err := d.c.Stat(remote.Parse(Absolute(path)))
	return err == nil
}

// Remove implements Drive.
func (d sdkDrive) Remove(path string) error {
	_, err := d.c.Remove(remote.Parse(Absolute(path)), remote.RemoveOptions{Recursive: true, IgnoreMissing: true})
	return err
}

// sdkEntry describes the file e at the remote path p, which goes by rel
// in a listing.
func sdkEntry(rel string, p remote.Path, e remote.Entry) Entry {
	version := e.Handle + "@" + strconv.FormatInt(e.Modified.Unix(), 10)
	return Entry{Path: p.String(), Rel: rel, Size: e.Size, Modified: e.Modified, Version: version}
}

// Files implements Drive.
func (d sdkDrive) Files(folder string) ([]Entry, error) {
	root, _, err := d.c.Resolve(remote.Parse(Absolute(folder)))
	if remote.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []Entry
	err = d.c.Walk(root, func(p remote.Path, e remote.Entry) error {
		parts := p.Parts[len(root.Parts):]
		names := make([]string, len(parts))
		for i, part := range parts {
			names[i] = remote.LocalName(part)
		}
		rel := strings.Join(names, "/")
		if !e.IsDir() && !Excluded(rel) {
			files = append(files, sdkEntry(rel, p, e))
		}
		return nil
	})
	return files, err
}

// Upload implements Drive.
func (d sdkDrive) Upload(local, path string) (Entry, error) {
	dst := remote.Parse(Absolute(path))
	if _, err := d.c.Mkdir(dst.Dir(), true); err != nil {
		return Entry{}, err
	}
	e, err := d.c.Upload(local, dst, true, nil)
	if err != nil {
		return Entry{}, err
	}
	return sdkEntry("", dst, e), nil
}

// Download implements Drive.
func (d sdkDrive) Download(path, local string) (Entry, error) {
	src, e, err := d.c.Resolve(remote.Parse(Absolute(path)))
	if err != nil {
		return Entry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return Entry{}, err
	}
	if err := d.c.Download(src, local, nil); err != nil {
		return Entry{}, err
	}
	return sdkEntry("", src, e), os.Chtimes(local, e.Modified, e.Modified)
}

// Read implements Drive.
func (d sdkDrive) Read(path string) ([]byte, error) {
	var content bytes.Buffer
	err := d.c.Cat(remote.Parse(Absolute(path)), &content, nil)
	return content.Bytes(), err
}
