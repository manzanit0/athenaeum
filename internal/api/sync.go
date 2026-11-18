package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/manzanit0/athenaeum/internal/megasync"
	"github.com/manzanit0/athenaeum/internal/store"
)

// syncTitles names each sync direction for the job history.
var syncTitles = map[string]string{
	megasync.Push: "Push library to MEGA",
	megasync.Pull: "Pull library from MEGA",
	megasync.Both: "Two-way sync with MEGA",
}

// syncInfo describes the state of MEGA sync.
type syncInfo struct {
	megasync.Status
	// Folder is the MEGA folder the library syncs with.
	Folder string `json:"folder"`
	// PendingDeletions is the number of local deletions waiting to be
	// applied to MEGA.
	PendingDeletions int `json:"pending_deletions"`
	// LastRun is the most recent sync job, if any.
	LastRun *jobView `json:"last_run,omitempty"`
}

// syncStatus reports which MEGA account is signed in and how the last
// run went.
func (s *Server) syncStatus(w http.ResponseWriter, _ *http.Request) {
	info := syncInfo{
		Status: s.Syncer.Accounts.Status(),
		Folder: s.setting(SettingMegaFolder),
	}
	pending, err := s.Store.Tombstones()
	if err != nil {
		fail(w, err)
		return
	}
	info.PendingDeletions = len(pending)
	jobs, err := s.Store.Jobs("sync", 1)
	if err != nil {
		fail(w, err)
		return
	}
	if len(jobs) > 0 {
		last := s.view(jobs[0])
		info.LastRun = &last
	}
	writeJSON(w, http.StatusOK, info)
}

// signInRequest holds what signs in to MEGA.
type signInRequest struct {
	// Email is the address of the account.
	Email string `json:"email"`
	// Password is the password of the account. It is used for this
	// sign-in and not kept.
	Password string `json:"password"`
	// Code is the current two-factor code, for accounts that have one.
	Code string `json:"code"`
}

// signInFailure says why a sign-in did not succeed.
type signInFailure struct {
	// Error is the message for the reader.
	Error string `json:"error"`
	// CodeRequired reports that the account asks for a two-factor code.
	CodeRequired bool `json:"code_required"`
}

// signIn signs in to MEGA and keeps the session, encrypted, for later
// syncs.
func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	var req signInRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	status, err := s.Syncer.Accounts.SignIn(req.Email, req.Password, req.Code)
	switch {
	case errors.Is(err, megasync.ErrCodeRequired):
		failure := signInFailure{Error: "Enter the code from your authenticator app.", CodeRequired: true}
		writeJSON(w, http.StatusUnauthorized, failure)
	case err != nil:
		fail(w, err)
	default:
		writeJSON(w, http.StatusOK, status)
	}
}

// signOutResult says how a sign-out went.
type signOutResult struct {
	// Confirmed reports whether MEGA confirmed that the session is over.
	Confirmed bool `json:"confirmed"`
}

// signOut forgets the MEGA session and ends it on MEGA.
func (s *Server) signOut(w http.ResponseWriter, _ *http.Request) {
	confirmed, err := s.Syncer.Accounts.SignOut()
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, signOutResult{Confirmed: confirmed})
}

// startSync starts a job synchronising the library with MEGA.
func (s *Server) startSync(w http.ResponseWriter, r *http.Request) {
	var opts megasync.Options
	if err := decode(r, &opts); err != nil {
		fail(w, err)
		return
	}
	folder := s.setting(SettingMegaFolder)
	if err := s.checkSync(folder, opts); err != nil {
		fail(w, err)
		return
	}
	title := syncTitles[opts.Direction]
	if opts.DryRun {
		title += " (preview)"
	}
	job, err := s.startTrackedJob("sync", title, nil, func(ctx context.Context, report func(any)) (any, error) {
		return s.Syncer.Run(ctx, folder, opts, func(p megasync.Progress) { report(p) })
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// checkSync reports why a sync with the given folder and options cannot
// start, or nil when it can.
func (s *Server) checkSync(folder string, opts megasync.Options) error {
	if err := opts.Validate(); err != nil {
		return err
	}
	if megasync.Normalize(folder) == "" {
		return megasync.ErrNotConfigured
	}
	if !s.Syncer.Accounts.Status().SignedIn {
		return megasync.ErrSignedOut
	}
	jobs, err := s.Store.Jobs("sync", 1)
	if err != nil {
		return err
	}
	if len(jobs) > 0 && jobs[0].Status == store.JobRunning {
		return megasync.ErrBusy
	}
	return nil
}
