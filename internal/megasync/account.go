package megasync

import (
	"encoding/json"
	"errors"
	"strings"
)

// sessionName is the name the session is kept under.
const sessionName = "mega.session"

// Secrets keeps values safe between runs of the application.
type Secrets interface {
	// Get returns the value kept under name and whether there is one.
	Get(name string) ([]byte, bool, error)
	// Put keeps value under name.
	Put(name string, value []byte) error
	// Delete forgets the value kept under name.
	Delete(name string) error
}

// Status says which MEGA account, if any, the library syncs with.
type Status struct {
	// SignedIn reports whether an account is signed in.
	SignedIn bool `json:"signed_in"`
	// Account is the email of that account.
	Account string `json:"account"`
	// Error explains why a kept session cannot be used.
	Error string `json:"error"`
}

// Accounts signs in to MEGA and keeps the session for later syncs. The
// password is used once and never kept.
type Accounts struct {
	// Service reaches MEGA.
	Service Service
	// Secrets keeps the session between runs.
	Secrets Secrets
}

// session returns the kept session and whether there is one.
func (a *Accounts) session() (Session, bool, error) {
	var s Session
	data, found, err := a.Secrets.Get(sessionName)
	if err != nil || !found {
		return s, found, err
	}
	return s, true, json.Unmarshal(data, &s)
}

// Status reports the signed-in account without reaching MEGA.
func (a *Accounts) Status() Status {
	s, found, err := a.session()
	switch {
	case err != nil:
		return Status{Error: "The MEGA session kept from the last sign-in cannot be read. Sign in again."}
	case !found:
		return Status{}
	}
	return Status{SignedIn: true, Account: s.Email}
}

// SignIn opens the account of email and keeps its session. code is the
// current two-factor code of accounts that have one.
func (a *Accounts) SignIn(email, password, code string) (Status, error) {
	email = strings.TrimSpace(email)
	if email == "" || password == "" {
		return Status{}, ErrSignInFailed
	}
	s, err := a.Service.SignIn(email, password, strings.TrimSpace(code))
	if err != nil {
		return Status{}, err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return Status{}, err
	}
	if err := a.Secrets.Put(sessionName, data); err != nil {
		return Status{}, err
	}
	return Status{SignedIn: true, Account: s.Email}, nil
}

// SignOut forgets the kept session and ends it on MEGA. It reports
// whether MEGA confirmed the end of the session; the session is forgotten
// here either way.
func (a *Accounts) SignOut() (bool, error) {
	s, found, err := a.session()
	if derr := a.Secrets.Delete(sessionName); derr != nil {
		return false, derr
	}
	if err != nil || !found {
		return false, nil
	}
	return a.Service.SignOut(s) == nil, nil
}

// Open returns the drive of the signed-in account. A session MEGA no
// longer accepts is forgotten.
func (a *Accounts) Open() (Drive, error) {
	s, found, err := a.session()
	if err != nil || !found {
		return nil, ErrSignedOut
	}
	drive, err := a.Service.Resume(s)
	if errors.Is(err, ErrSessionEnded) {
		a.Secrets.Delete(sessionName)
	}
	return drive, err
}
