package megasync

import (
	"sort"
	"time"

	"github.com/manzanit0/athenaeum/internal/store"
)

// What a sync both ways does about one file.
const (
	// stepUpload sends the library file to MEGA.
	stepUpload = "upload"
	// stepDownload fetches the file from MEGA.
	stepDownload = "download"
	// stepRemoveRemote removes the file from MEGA.
	stepRemoveRemote = "remove-remote"
	// stepRemoveLocal removes the file from the library.
	stepRemoveLocal = "remove-local"
	// stepAdopt records that both sides already hold the same file.
	stepAdopt = "adopt"
	// stepForget drops the record of a file both sides have lost.
	stepForget = "forget"
)

// The sides of a sync.
const (
	// SideLibrary is the library folder.
	SideLibrary = "library"
	// SideMega is the MEGA folder.
	SideMega = "mega"
)

// Conflict is a file that changed on both sides since they last matched.
type Conflict struct {
	// Path is the file, relative to the synced folders.
	Path string `json:"path"`
	// Kept is the side whose version stays: SideLibrary or SideMega.
	Kept string `json:"kept"`
}

// localFile is a file of the library folder.
type localFile struct {
	// size is the size in bytes.
	size int64
	// mtime is the modification time.
	mtime time.Time
}

// step is what a sync both ways does about the file at path.
type step struct {
	// op is one of the step constants.
	op string
	// path is the file, relative to the synced folders.
	path string
	// size is the number of bytes a transfer moves.
	size int64
	// replaces reports that a transfer replaces a file the other side
	// has.
	replaces bool
	// conflict reports that both sides changed the file.
	conflict bool
}

// Ways a side stands against what the last sync recorded.
const (
	// same is a file the side still has as recorded, or one neither the
	// side nor the record has.
	same = iota
	// changed is a file the side has that differs from the record, or
	// that the record lacks.
	changed
	// deleted is a recorded file the side no longer has.
	deleted
)

// localState says how the library file l, which exists or not, stands
// against the record b, which exists or not.
func localState(l localFile, exists bool, b store.SyncEntry, recorded bool) int {
	switch {
	case !exists && recorded:
		return deleted
	case !exists:
		return same
	case !recorded || l.size != b.LocalSize || l.mtime.UnixNano() != b.LocalMTime:
		return changed
	}
	return same
}

// remoteState says how the MEGA file r, which exists or not, stands
// against the record b, which exists or not.
func remoteState(r Entry, exists bool, b store.SyncEntry, recorded bool) int {
	switch {
	case !exists && recorded:
		return deleted
	case !exists:
		return same
	case !recorded || r.Size != b.RemoteSize || r.Version != b.RemoteVersion:
		return changed
	}
	return same
}

// planMerge returns what a sync both ways does to make the library files
// local and the MEGA files remote the same, given what the last syncs
// recorded in base. A side that still matches the record yields to the
// one that does not, deletions included. When both differ, the newer
// file stays and a changed file outlives its deletion. gone holds the
// files deleted here before anything was recorded about them: they are
// removed from MEGA rather than fetched again.
func planMerge(local map[string]localFile, remote map[string]Entry, base map[string]store.SyncEntry, gone map[string]bool) []step {
	paths := map[string]bool{}
	for p := range local {
		paths[p] = true
	}
	for p := range remote {
		paths[p] = true
	}
	for p := range base {
		paths[p] = true
	}
	var steps []step
	for p := range paths {
		l, hasLocal := local[p]
		r, hasRemote := remote[p]
		b, recorded := base[p]
		up := step{op: stepUpload, path: p, size: l.size, replaces: hasRemote}
		down := step{op: stepDownload, path: p, size: r.Size, replaces: hasLocal}
		ls, rs := localState(l, hasLocal, b, recorded), remoteState(r, hasRemote, b, recorded)
		switch {
		case ls == same && rs == same:
			continue
		case !recorded && hasLocal && hasRemote:
			steps = append(steps, unrecorded(l, r, up, down))
		case !recorded && hasRemote && gone[p]:
			steps = append(steps, step{op: stepRemoveRemote, path: p})
		case ls == deleted && rs == deleted:
			steps = append(steps, step{op: stepForget, path: p})
		case rs == same && ls == deleted:
			steps = append(steps, step{op: stepRemoveRemote, path: p})
		case ls == same && rs == deleted:
			steps = append(steps, step{op: stepRemoveLocal, path: p})
		case rs == same || rs == deleted:
			up.conflict = rs == deleted
			steps = append(steps, up)
		case ls == same || ls == deleted:
			down.conflict = ls == deleted
			steps = append(steps, down)
		default:
			steps = append(steps, newer(l, r, up, down))
		}
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].path < steps[j].path })
	return steps
}

// unrecorded returns what to do about a file both sides have and nothing
// was recorded about. Files of one size and time are taken to be the
// same, as an upload or a download leaves them; otherwise the newer one
// stays.
func unrecorded(l localFile, r Entry, up, down step) step {
	if l.size == r.Size && l.mtime.Unix() == r.Modified.Unix() {
		return step{op: stepAdopt, path: up.path}
	}
	return newer(l, r, up, down)
}

// newer returns the transfer that keeps the newer of two versions of a
// file both sides changed: up when the library file is, down otherwise.
func newer(l localFile, r Entry, up, down step) step {
	if l.mtime.After(r.Modified) {
		up.conflict = true
		return up
	}
	down.conflict = true
	return down
}
