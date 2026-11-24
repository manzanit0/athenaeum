// Package megatest stands in for MEGA in tests. It keeps the cloud drive
// in a local folder and applies the rules of the real service: the same
// plan for a sync, a password, an account with two-factor authentication
// and sessions that can be ended.
package megatest

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manzanit0/athenaeum/internal/megasync"
	"github.com/manzanit0/mega-cli/pkg/mirror"
)

// The accounts the stand-in knows.
const (
	// Email is the address of the plain account.
	Email = "reader@example.com"
	// Password is the password of both accounts.
	Password = "correct horse battery staple"
	// GuardedEmail is the address of the account with two-factor
	// authentication.
	GuardedEmail = "guarded@example.com"
	// Code is the two-factor code the guarded account accepts.
	Code = "246810"
)

// excludes lists the file name globs never synced.
var excludes = []string{".DS_Store"}

// Service is a MEGA whose cloud drive is the folder Root.
type Service struct {
	// Root is the folder standing in for the cloud drive.
	Root string
	// DelayFile names a file whose content, such as "150ms", is how long
	// every change of a sync takes, so tests can watch its progress.
	DelayFile string
	// mu guards the fields below.
	mu sync.Mutex
	// issued counts the sessions handed out.
	issued int
	// ended holds the sessions that were signed out or revoked.
	ended map[string]bool
}

// SignIn implements megasync.Service.
func (s *Service) SignIn(email, password, code string) (megasync.Session, error) {
	known := email == Email || email == GuardedEmail
	switch {
	case !known || password != Password:
		return megasync.Session{}, fmt.Errorf("%w: wrong email or password", megasync.ErrSignInFailed)
	case email == GuardedEmail && code == "":
		return megasync.Session{}, megasync.ErrCodeRequired
	case email == GuardedEmail && code != Code:
		return megasync.Session{}, fmt.Errorf("%w: wrong two-factor code", megasync.ErrSignInFailed)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.issued++
	return megasync.Session{Email: email, ID: fmt.Sprintf("session-%d", s.issued), Key: []byte("master key")}, nil
}

// End makes MEGA stop accepting the session called id, as when it is
// revoked from another device.
func (s *Service) End(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended == nil {
		s.ended = map[string]bool{}
	}
	s.ended[id] = true
}

// Resume implements megasync.Service.
func (s *Service) Resume(session megasync.Session) (megasync.Drive, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended[session.ID] || !strings.HasPrefix(session.ID, "session-") {
		return nil, megasync.ErrSessionEnded
	}
	return drive{root: s.Root, delayFile: s.DelayFile}, nil
}

// SignOut implements megasync.Service.
func (s *Service) SignOut(session megasync.Session) error {
	s.End(session.ID)
	return nil
}

// drive is the cloud drive kept in a folder.
type drive struct {
	// root is the folder standing in for the cloud drive.
	root string
	// delayFile names the file saying how long every change takes.
	delayFile string
}

// local maps a remote path to its place under the root.
func (d drive) local(remote string) string {
	return filepath.Join(d.root, filepath.FromSlash(megasync.Normalize(remote)))
}

// delay returns how long every change should take.
func (d drive) delay() time.Duration {
	data, err := os.ReadFile(d.delayFile)
	if err != nil {
		return 0
	}
	wait, _ := time.ParseDuration(strings.TrimSpace(string(data)))
	return wait
}

// tree returns the tree below dir, treating a missing folder as empty.
func tree(dir string) (mirror.Tree, error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return mirror.Tree{}, nil
	}
	return mirror.LocalTree(dir, excludes, nil)
}

// sync makes the folder dst match the folder src by the rules of a
// download when down is set and of an upload otherwise.
func (d drive) sync(src, dst string, down, del, dryRun bool, started func(megasync.Action)) (megasync.Result, error) {
	out := megasync.Result{Actions: []megasync.Action{}, DryRun: dryRun}
	source, err := mirror.LocalTree(src, excludes, nil)
	if err != nil {
		return out, fmt.Errorf("%s: no such file or folder", src)
	}
	target, err := tree(dst)
	if err != nil {
		return out, err
	}
	changed := mirror.ChangedUp
	if down {
		changed = mirror.ChangedDown
	}
	plan, err := mirror.Plan(source, target, changed, del)
	if err != nil {
		return out, err
	}
	for _, a := range plan {
		action := megasync.Action{Op: a.Op.String(), Path: a.Path, Size: a.Size}
		out.Actions = append(out.Actions, action)
		if dryRun {
			continue
		}
		if started != nil {
			started(action)
		}
		time.Sleep(d.delay())
		if err := apply(a, source[a.Path], dst, down); err != nil {
			return out, fmt.Errorf("%s %s: %w", a.Op, a.Path, err)
		}
	}
	return out, nil
}

// apply carries out one action of a plan.
func apply(a mirror.Action, source mirror.Entry, dst string, down bool) error {
	target := filepath.Join(dst, filepath.FromSlash(a.Path))
	switch a.Op {
	case mirror.Mkdir:
		return os.MkdirAll(target, 0o755)
	case mirror.Delete:
		return os.RemoveAll(target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := copyFile(source.Path, target); err != nil {
		return err
	}
	if down {
		return os.Chtimes(target, source.MTime, source.MTime)
	}
	return nil
}

// copyFile copies the file at src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Push implements megasync.Drive.
func (d drive) Push(local, folder string, del, dryRun bool, started func(megasync.Action)) (megasync.Result, error) {
	return d.sync(local, d.local(folder), false, del, dryRun, started)
}

// Pull implements megasync.Drive.
func (d drive) Pull(folder, local string, del, dryRun bool, started func(megasync.Action)) (megasync.Result, error) {
	return d.sync(d.local(folder), local, true, del, dryRun, started)
}

// Exists implements megasync.Drive.
func (d drive) Exists(remote string) bool {
	_, err := os.Stat(d.local(remote))
	return err == nil
}

// Remove implements megasync.Drive.
func (d drive) Remove(remote string) error {
	return os.RemoveAll(d.local(remote))
}

// entry describes the file kept at file, which goes by rel in a listing
// and by remote on the drive.
func entry(rel, remote, file string) (megasync.Entry, error) {
	info, err := os.Stat(file)
	if err != nil {
		return megasync.Entry{}, err
	}
	return megasync.Entry{
		Path: remote, Rel: rel, Size: info.Size(), Modified: info.ModTime(),
		Version: strconv.FormatInt(info.ModTime().UnixNano(), 10),
	}, nil
}

// Files implements megasync.Drive.
func (d drive) Files(folder string) ([]megasync.Entry, error) {
	found, err := tree(d.local(folder))
	if err != nil {
		return nil, err
	}
	var files []megasync.Entry
	for rel, e := range found {
		if e.Dir {
			continue
		}
		file, err := entry(rel, megasync.Absolute(folder)+"/"+rel, e.Path)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

// Upload implements megasync.Drive.
func (d drive) Upload(local, remote string) (megasync.Entry, error) {
	time.Sleep(d.delay())
	target := d.local(remote)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return megasync.Entry{}, err
	}
	if err := copyFile(local, target); err != nil {
		return megasync.Entry{}, err
	}
	return entry("", megasync.Absolute(remote), target)
}

// Download implements megasync.Drive.
func (d drive) Download(remote, local string) (megasync.Entry, error) {
	time.Sleep(d.delay())
	source, err := entry("", megasync.Absolute(remote), d.local(remote))
	if err != nil {
		return source, err
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return source, err
	}
	if err := copyFile(d.local(remote), local); err != nil {
		return source, err
	}
	return source, os.Chtimes(local, source.Modified, source.Modified)
}

// Read implements megasync.Drive.
func (d drive) Read(remote string) ([]byte, error) {
	return os.ReadFile(d.local(remote))
}
