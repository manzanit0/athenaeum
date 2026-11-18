// Package megasync mirrors the library folder to and from MEGA with the
// Go packages of mega-cli (https://github.com/manzanit0/mega-cli).
package megasync

import (
	"errors"
	"strings"
	"time"
)

var (
	// ErrSignedOut is returned when there is no MEGA account to work with.
	ErrSignedOut = errors.New("sign in to MEGA first")
	// ErrCodeRequired is returned when the account asks for the code of
	// its two-factor authentication.
	ErrCodeRequired = errors.New("the account asks for a two-factor code")
	// ErrSignInFailed is returned when MEGA does not accept a sign-in.
	ErrSignInFailed = errors.New("MEGA did not accept the sign-in")
	// ErrSessionEnded is returned when MEGA no longer accepts the session
	// kept from the last sign-in.
	ErrSessionEnded = errors.New("MEGA has ended the session; sign in again")
)

// excludes lists the file name globs never synced.
var excludes = []string{".DS_Store"}

// Action is one change made, or planned, by a sync.
type Action struct {
	// Op is one of mkdir, copy, update or delete.
	Op string `json:"op"`
	// Path is the item's path relative to the synced folders.
	Path string `json:"path"`
	// Size is the number of bytes transferred.
	Size int64 `json:"size,omitempty"`
}

// Result summarises one sync direction.
type Result struct {
	// Actions lists the changes made or planned.
	Actions []Action `json:"actions"`
	// DryRun reports whether the changes were only planned.
	DryRun bool `json:"dry_run"`
	// Files is the number of files transferred.
	Files int `json:"files"`
	// Bytes is the number of bytes transferred.
	Bytes int64 `json:"bytes"`
	// Folders is the number of folders created.
	Folders int `json:"folders"`
	// Deleted is the number of items deleted.
	Deleted int `json:"deleted"`
}

// tally fills in the totals of r from its actions.
func (r *Result) tally() {
	r.Files, r.Bytes, r.Folders, r.Deleted = 0, 0, 0, 0
	for _, a := range r.Actions {
		switch a.Op {
		case "mkdir":
			r.Folders++
		case "copy", "update":
			r.Files++
			r.Bytes += a.Size
		case "delete":
			r.Deleted++
		}
	}
}

// Entry is a file on MEGA.
type Entry struct {
	// Path is the absolute remote path.
	Path string `json:"path"`
	// Size is the size in bytes.
	Size int64 `json:"size"`
	// Modified is the remote timestamp.
	Modified time.Time `json:"modified"`
}

// Session is what MEGA hands out at sign-in to open the account again
// without its password.
type Session struct {
	// Email is the address of the account.
	Email string `json:"email"`
	// ID identifies the session to MEGA.
	ID string `json:"id"`
	// Key is the master key of the account.
	Key []byte `json:"key"`
}

// Drive is the cloud drive of a signed-in MEGA account.
type Drive interface {
	// Push makes the MEGA folder match the local folder local. A dry run
	// only returns the plan; otherwise started is told of every change
	// as it begins.
	Push(local, folder string, del, dryRun bool, started func(Action)) (Result, error)
	// Pull makes the local folder local match the MEGA folder, in the
	// same manner as Push.
	Pull(folder, local string, del, dryRun bool, started func(Action)) (Result, error)
	// Exists reports whether the remote path exists.
	Exists(remote string) bool
	// Remove moves the remote path to the Rubbish Bin. A missing path is
	// not an error.
	Remove(remote string) error
	// Files lists the files below the remote folder.
	Files(folder string) ([]Entry, error)
	// Read returns the content of the remote file.
	Read(remote string) ([]byte, error)
}

// Service signs in to MEGA.
type Service interface {
	// SignIn opens the account of email with its password and, for
	// accounts with two-factor authentication, the current code. It
	// returns ErrCodeRequired when a code is needed and none was given,
	// and ErrSignInFailed when MEGA refuses.
	SignIn(email, password, code string) (Session, error)
	// Resume opens the drive of the account a session belongs to. It
	// returns ErrSessionEnded when MEGA no longer accepts the session.
	Resume(s Session) (Drive, error)
	// SignOut ends the session on MEGA.
	SignOut(s Session) error
}

// Step is called while a sync runs: done is the number of changes
// finished and current the change now being made, or nil at the end.
type Step func(plan Result, done int, current *Action)

// transfer plans one direction of a sync with do and, unless it is a dry
// run, carries it out, telling step of every change as it starts.
func transfer(do func(dryRun bool, started func(Action)) (Result, error), dryRun bool, step Step) (Result, error) {
	plan, err := do(true, nil)
	if plan.Actions == nil {
		plan.Actions = []Action{}
	}
	plan.DryRun = true
	plan.tally()
	if err != nil || dryRun {
		return plan, err
	}
	done := Result{Actions: []Action{}}
	if len(plan.Actions) == 0 {
		return done, nil
	}
	step(plan, 0, nil)
	_, err = do(false, func(a Action) {
		step(plan, len(done.Actions), &a)
		done.Actions = append(done.Actions, a)
	})
	if err != nil {
		done.Actions = done.Actions[:max(len(done.Actions)-1, 0)]
	} else {
		step(plan, len(plan.Actions), nil)
	}
	done.tally()
	return done, err
}

// Normalize returns the remote folder without surrounding slashes or the
// mega:// scheme.
func Normalize(folder string) string {
	return strings.Trim(strings.TrimPrefix(strings.TrimSpace(folder), "mega://"), "/")
}

// Absolute returns folder as an absolute MEGA path.
func Absolute(folder string) string {
	return "/" + Normalize(folder)
}
