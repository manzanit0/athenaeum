package megasync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/manzanit0/athenaeum/internal/store"
	"github.com/manzanit0/mega-cli/pkg/mirror"
)

// scopeSetting names the setting that says which account and folder the
// recorded sync state is about.
const scopeSetting = "sync_state_scope"

var (
	// ErrRemoteEmpty is returned when the MEGA folder holds nothing
	// although the library was synced with it before.
	ErrRemoteEmpty = errors.New("the MEGA folder is empty although this library was synced with it before; " +
		"syncing would empty the library. Upload to MEGA to fill the folder again, " +
		"or download with the delete option to empty the library")
	// ErrLocalEmpty is returned when the library folder holds nothing
	// although it was synced with MEGA before.
	ErrLocalEmpty = errors.New("the library folder is empty although it was synced with MEGA before; " +
		"syncing would empty the MEGA folder. Download from MEGA to fill the library again, " +
		"or upload with the delete option to empty the MEGA folder")
)

// rescope forgets the recorded sync state when it is about another
// account or folder than the ones now synced.
func (s *Syncer) rescope(folder string) error {
	scope := s.Accounts.Status().Account + "\n" + folder
	was, err := s.Store.Setting(scopeSetting, "")
	if err != nil || was == scope {
		return err
	}
	if err := s.Store.ClearSyncState(); err != nil {
		return err
	}
	return s.Store.SetSetting(scopeSetting, scope)
}

// localFiles returns the files of the library folder that are synced, by
// library relative path.
func (s *Syncer) localFiles() (map[string]localFile, error) {
	tree, err := mirror.LocalTree(s.Library.Root(), excludes, nil)
	if err != nil {
		return nil, err
	}
	out := map[string]localFile{}
	for rel, e := range tree {
		if !e.Dir {
			out[rel] = localFile{size: e.Size, mtime: e.MTime}
		}
	}
	return out, nil
}

// remoteFiles returns the files of the MEGA folder, by library relative
// path.
func remoteFiles(drive Drive, folder string) (map[string]Entry, error) {
	entries, err := drive.Files(folder)
	if err != nil {
		return nil, fmt.Errorf("listing MEGA files: %w", err)
	}
	out := map[string]Entry{}
	for _, e := range entries {
		out[e.Rel] = e
	}
	return out, nil
}

// abs returns the local path of the library file at rel.
func (s *Syncer) abs(rel string) string {
	return filepath.Join(s.Library.Root(), filepath.FromSlash(rel))
}

// trash returns the folder that takes the library files this run
// removes.
func (s *Syncer) trash(started time.Time) string {
	dir := s.Trash
	if dir == "" {
		dir = filepath.Join(filepath.Dir(s.Library.Root()), "trash")
	}
	return filepath.Join(dir, started.UTC().Format("20060102T150405.000"))
}

// discard moves the library file at rel, which may be gone, to the trash
// folder bin, and removes the folders it leaves empty.
func (s *Syncer) discard(rel, bin string) error {
	to := filepath.Join(bin, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if err := os.Rename(s.abs(rel), to); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for dir := path.Dir(rel); dir != "." && dir != "/"; dir = path.Dir(dir) {
		if os.Remove(s.abs(dir)) != nil {
			break
		}
	}
	return nil
}

// record notes that the library file at rel and the MEGA file e are the
// same.
func (s *Syncer) record(rel string, e Entry) error {
	info, err := os.Stat(s.abs(rel))
	if err != nil {
		return err
	}
	return s.remember(rel, info, e)
}

// remember notes that the library file at rel, when it looked like info,
// and the MEGA file e were the same.
func (s *Syncer) remember(rel string, info os.FileInfo, e Entry) error {
	return s.Store.PutSyncState(rel, store.SyncEntry{
		LocalSize: info.Size(), LocalMTime: info.ModTime().UnixNano(),
		RemoteSize: e.Size, RemoteVersion: e.Version,
	})
}

// upload sends the library file at rel to the MEGA path target and
// records the two as the same. A file left alone meanwhile takes the
// timestamp of its copy, which is what an upload or a download on its
// own expects of a file that needs no transfer; one changed meanwhile is
// recorded as it was sent, so the next sync sends it again.
func (s *Syncer) upload(drive Drive, rel, target string) error {
	before, err := os.Stat(s.abs(rel))
	if err != nil {
		return err
	}
	e, err := drive.Upload(s.abs(rel), target)
	if err != nil {
		return err
	}
	after, err := os.Stat(s.abs(rel))
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return s.remember(rel, before, e)
	}
	if err := os.Chtimes(s.abs(rel), e.Modified, e.Modified); err != nil {
		return err
	}
	return s.record(rel, e)
}

// twoWay holds one sync both ways while it runs.
type twoWay struct {
	// s is the syncer running it.
	s *Syncer
	// drive is the cloud drive of the account.
	drive Drive
	// report is filled in as the run goes.
	report *Report
	// remote holds the MEGA files as the run found them.
	remote map[string]Entry
	// steps is what the run does, by path.
	steps []step
	// bin is the trash folder for the library files the run removes.
	bin string
}

// plans returns what the steps of the run amount to on each side: the
// MEGA paths removed, the uploads and the changes to the library.
func (t *twoWay) plans() (removed []string, up, down Result) {
	removed = []string{}
	up, down = Result{Actions: []Action{}}, Result{Actions: []Action{}}
	for _, st := range t.steps {
		op := "copy"
		if st.replaces {
			op = "update"
		}
		switch st.op {
		case stepRemoveRemote:
			removed = append(removed, st.path)
		case stepUpload:
			up.Actions = append(up.Actions, Action{Op: op, Path: st.path, Size: st.size})
		case stepDownload:
			down.Actions = append(down.Actions, Action{Op: op, Path: st.path, Size: st.size})
		case stepRemoveLocal:
			down.Actions = append(down.Actions, Action{Op: "delete", Path: st.path})
		}
	}
	up.tally()
	down.tally()
	return removed, up, down
}

// conflicts lists the files both sides changed and whose version stays.
func (t *twoWay) conflicts() []Conflict {
	out := []Conflict{}
	for _, st := range t.steps {
		switch {
		case st.conflict && st.op == stepUpload:
			out = append(out, Conflict{Path: st.path, Kept: SideLibrary})
		case st.conflict && st.op == stepDownload:
			out = append(out, Conflict{Path: st.path, Kept: SideMega})
		}
	}
	return out
}

// target returns the MEGA path of the file at rel: where MEGA has it, or
// where an upload puts it.
func (t *twoWay) target(rel string) string {
	if e, ok := t.remote[rel]; ok {
		return e.Path
	}
	return path.Join(Absolute(t.report.Folder), rel)
}

// apply carries out one step of the run.
func (t *twoWay) apply(st step) error {
	s := t.s
	switch st.op {
	case stepRemoveRemote:
		if err := t.drive.Remove(t.target(st.path)); err != nil {
			return err
		}
		return s.Store.DeleteSyncState(st.path)
	case stepUpload:
		return s.upload(t.drive, st.path, t.target(st.path))
	case stepDownload:
		if st.conflict && st.replaces {
			if err := s.discard(st.path, t.bin); err != nil {
				return err
			}
		}
		e, err := t.drive.Download(t.target(st.path), s.abs(st.path))
		if err != nil {
			return err
		}
		return s.record(st.path, e)
	case stepRemoveLocal:
		if err := s.discard(st.path, t.bin); err != nil {
			return err
		}
		return s.Store.DeleteSyncState(st.path)
	case stepAdopt:
		return s.record(st.path, t.remote[st.path])
	}
	return s.Store.DeleteSyncState(st.path)
}

// phase carries out the steps of the run that make up one phase, those
// with one of ops, telling progress of each transfer among them. It
// returns the changes made before any error.
func (t *twoWay) phase(ctx context.Context, name string, plan Result, progress func(Progress), ops ...string) ([]Action, error) {
	done := []Action{}
	advance := stepper(name, progress)
	listed := map[string]Action{}
	for _, a := range plan.Actions {
		listed[a.Path] = a
	}
	if len(plan.Actions) > 0 {
		advance(plan, 0, nil)
	}
	for _, st := range t.steps {
		if !contains(ops, st.op) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return done, err
		}
		a, shown := listed[st.path]
		if shown {
			advance(plan, len(done), &a)
		}
		if err := t.apply(st); err != nil {
			return done, fmt.Errorf("%s %s: %w", st.op, st.path, err)
		}
		if shown {
			done = append(done, a)
		}
	}
	if len(plan.Actions) > 0 {
		advance(plan, len(plan.Actions), nil)
	}
	return done, nil
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// outcome returns the result of a phase that made the changes done.
func outcome(done []Action) *Result {
	r := &Result{Actions: done}
	r.tally()
	return r
}

// merge syncs the library and the MEGA folder both ways. It compares
// each side with what the last syncs recorded, so a change or a deletion
// made on one side reaches the other instead of being undone by it.
func (s *Syncer) merge(ctx context.Context, drive Drive, report *Report, progress func(Progress)) error {
	progress(Progress{Phase: PhasePreparing})
	t := &twoWay{s: s, drive: drive, report: report, bin: s.trash(time.Now())}
	if err := t.prepare(); err != nil {
		return err
	}
	removed, up, down := t.plans()
	report.Conflicts = t.conflicts()
	onMega, err := s.catalog()
	if err != nil {
		return err
	}
	inLibrary, err := s.catalog()
	if err != nil {
		return err
	}
	inLibrary.adopts = true
	describe := func(removed []string, up, down []Action) {
		changes := []Action{}
		for _, p := range removed {
			changes = append(changes, Action{Op: "delete", Path: p})
		}
		mega := onMega.summarise(append(changes, up...))
		report.Removed, report.Mega = removed, &mega
		s.study(inLibrary, drive, report, down)
		library := inLibrary.summarise(down)
		report.Library = &library
	}
	if report.DryRun {
		up.DryRun, down.DryRun = true, true
		report.Push, report.Pull = &up, &down
		describe(removed, up.Actions, down.Actions)
		return nil
	}
	removed = []string{}
	for _, st := range t.steps {
		if st.op != stepRemoveRemote {
			continue
		}
		if err := t.apply(st); err != nil {
			report.Removed = removed
			return fmt.Errorf("removing %s from MEGA: %w", st.path, err)
		}
		removed = append(removed, st.path)
	}
	sent, err := t.phase(ctx, PhaseUploading, up, progress, stepUpload)
	report.Push = outcome(sent)
	if err != nil {
		describe(removed, sent, nil)
		return fmt.Errorf("uploading: %w", err)
	}
	fetched, err := t.phase(ctx, PhaseDownloading, down, progress, stepDownload, stepRemoveLocal, stepAdopt, stepForget)
	report.Pull = outcome(fetched)
	describe(removed, sent, fetched)
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	progress(Progress{Phase: PhaseFinishing})
	if err := s.Store.ClearTombstones(); err != nil {
		return err
	}
	rescan, err := s.Library.Rescan(ctx)
	if err != nil {
		return fmt.Errorf("rescanning the library: %w", err)
	}
	report.Rescan = &rescan
	if err := s.extend(inLibrary); err != nil {
		return err
	}
	library := inLibrary.summarise(fetched)
	report.Library = &library
	return nil
}

// prepare compares both sides with what the last syncs recorded and
// works out the steps of the run. It refuses to run when one side has
// lost everything that was synced other than by the library deleting
// it, which is more likely a folder that moved or a drive that is not
// there than a library emptied on purpose.
func (t *twoWay) prepare() error {
	s := t.s
	base, err := s.Store.SyncState()
	if err != nil {
		return err
	}
	if t.remote, err = remoteFiles(t.drive, t.report.Folder); err != nil {
		return err
	}
	local, err := s.localFiles()
	if err != nil {
		return err
	}
	paths, err := s.Store.Tombstones()
	if err != nil {
		return err
	}
	gone := map[string]bool{}
	for _, p := range paths {
		gone[p] = true
	}
	switch {
	case len(base) > 0 && len(t.remote) == 0:
		return ErrRemoteEmpty
	case len(local) == 0 && unexplained(base, gone):
		return ErrLocalEmpty
	}
	t.steps = planMerge(local, t.remote, base, gone)
	return nil
}

// unexplained reports whether any file recorded in base is not among
// those the library deleted itself.
func unexplained(base map[string]store.SyncEntry, gone map[string]bool) bool {
	for p := range base {
		if !gone[p] {
			return true
		}
	}
	return false
}

// agree records as the same the files that an upload or a download on
// its own left alike on both sides, so the next sync both ways starts
// from them.
func (s *Syncer) agree(drive Drive, folder string) error {
	remote, err := remoteFiles(drive, folder)
	if err != nil {
		return err
	}
	local, err := s.localFiles()
	if err != nil {
		return err
	}
	for rel, l := range local {
		r, ok := remote[rel]
		if !ok || l.size != r.Size || l.mtime.Unix() != r.Modified.Unix() {
			continue
		}
		if err := s.record(rel, r); err != nil {
			return err
		}
	}
	return nil
}
