package megasync

import (
	"bytes"
	"errors"
	"fmt"

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

// Files implements Drive.
func (d sdkDrive) Files(folder string) ([]Entry, error) {
	var files []Entry
	err := d.c.Walk(remote.Parse(Absolute(folder)), func(p remote.Path, e remote.Entry) error {
		if !e.IsDir() {
			files = append(files, Entry{Path: p.String(), Size: e.Size, Modified: e.Modified})
		}
		return nil
	})
	return files, err
}

// Read implements Drive.
func (d sdkDrive) Read(path string) ([]byte, error) {
	var content bytes.Buffer
	err := d.c.Cat(remote.Parse(Absolute(path)), &content, nil)
	return content.Bytes(), err
}
