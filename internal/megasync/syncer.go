package megasync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sync"

	"github.com/manzanit0/athenaeum/internal/library"
	"github.com/manzanit0/athenaeum/internal/store"
)

// Sync directions.
const (
	// Push uploads the library to MEGA.
	Push = "push"
	// Pull downloads the library from MEGA.
	Pull = "pull"
	// Both merges the library and MEGA in both directions.
	Both = "both"
)

var (
	// ErrNotConfigured is returned when no MEGA folder has been set.
	ErrNotConfigured = errors.New("no MEGA folder is configured")
	// ErrInvalidOptions is returned for unsupported sync options.
	ErrInvalidOptions = errors.New("direction must be push, pull or both, and delete needs push or pull")
	// ErrBusy is returned when a sync is already running.
	ErrBusy = errors.New("a sync is already running")
)

// Options selects what a sync run does.
type Options struct {
	// Direction is one of Push, Pull or Both.
	Direction string `json:"direction"`
	// Delete removes items missing from the source side. It is only valid
	// for Push and Pull.
	Delete bool `json:"delete"`
	// DryRun plans the sync without changing anything.
	DryRun bool `json:"dry_run"`
}

// Validate reports whether o is a supported combination of options.
func (o Options) Validate() error {
	switch o.Direction {
	case Push, Pull:
		return nil
	case Both:
		if !o.Delete {
			return nil
		}
	}
	return ErrInvalidOptions
}

// Report describes what a sync run did.
type Report struct {
	Options
	// Folder is the MEGA folder that was synced.
	Folder string `json:"folder"`
	// Removed lists the locally deleted paths removed from MEGA.
	Removed []string `json:"removed"`
	// Push is the outcome of uploading, when it ran.
	Push *Result `json:"push,omitempty"`
	// Pull is the outcome of downloading, when it ran.
	Pull *Result `json:"pull,omitempty"`
	// Rescan is how the database changed after downloading.
	Rescan *library.RescanResult `json:"rescan,omitempty"`
	// Mega describes, book by book, what the run changes on MEGA.
	Mega *Summary `json:"mega,omitempty"`
	// Library describes, book by book, what the run changes in the
	// library.
	Library *Summary `json:"library,omitempty"`
	// Conflicts lists the files a sync both ways found changed on both
	// sides, and whose version stays.
	Conflicts []Conflict `json:"conflicts,omitempty"`
}

// previewReads is the most details files a preview reads from MEGA to
// name the books it would download. Past it, books are named after their
// files.
const previewReads = 30

// Phases of a sync run.
const (
	// PhasePreparing compares the library with MEGA.
	PhasePreparing = "preparing"
	// PhaseUploading sends changes to MEGA.
	PhaseUploading = "uploading"
	// PhaseDownloading fetches changes from MEGA.
	PhaseDownloading = "downloading"
	// PhaseFinishing brings the library up to date after the transfers.
	PhaseFinishing = "finishing"
)

// Progress says how far a sync run has come.
type Progress struct {
	// Phase is what the run is doing.
	Phase string `json:"phase"`
	// Done is the number of changes finished in this phase.
	Done int `json:"done"`
	// Total is the number of changes this phase makes.
	Total int `json:"total"`
	// BytesDone is the size of the files transferred in this phase.
	BytesDone int64 `json:"bytes_done"`
	// BytesTotal is the size of the files this phase transfers.
	BytesTotal int64 `json:"bytes_total"`
	// Current is the path being transferred, if any.
	Current string `json:"current"`
}

// stepper returns the function that turns the steps of a transfer into
// progress reports for the given phase.
func stepper(phase string, report func(Progress)) Step {
	var bytes int64
	return func(plan Result, done int, current *Action) {
		p := Progress{Phase: phase, Done: done, Total: len(plan.Actions), BytesTotal: plan.Bytes}
		if current == nil && done > 0 {
			bytes = plan.Bytes
		}
		p.BytesDone = bytes
		if current != nil {
			p.Current = current.Path
			bytes += transferred(*current)
		}
		report(p)
	}
}

// transferred returns the number of bytes the change a moves.
func transferred(a Action) int64 {
	if a.Op == "copy" || a.Op == "update" {
		return a.Size
	}
	return 0
}

// Syncer synchronises a library with a MEGA folder.
type Syncer struct {
	// Accounts opens the drive of the signed-in MEGA account.
	Accounts *Accounts
	// Store holds the deletions awaiting propagation.
	Store *store.Store
	// Library is the library being synced.
	Library *library.Library
	// Trash is the folder that takes the library files a sync both ways
	// removes or replaces. Left empty, it is the folder "trash" next to
	// the library folder.
	Trash string
	// mu allows a single sync at a time.
	mu sync.Mutex
}

// Run synchronises the library with the MEGA folder according to opts,
// telling report, which may be nil, how far it has come. A sync both
// ways merges the two sides against what the last syncs recorded; an
// upload or a download on its own makes one side follow the other.
func (s *Syncer) Run(ctx context.Context, folder string, opts Options, report func(Progress)) (*Report, error) {
	if report == nil {
		report = func(Progress) {}
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	folder = Normalize(folder)
	if folder == "" {
		return nil, ErrNotConfigured
	}
	if !s.mu.TryLock() {
		return nil, ErrBusy
	}
	defer s.mu.Unlock()
	out := &Report{Options: opts, Folder: folder, Removed: []string{}}
	report(Progress{Phase: PhasePreparing})
	drive, err := s.Accounts.Open()
	if err != nil {
		return out, err
	}
	if err := s.rescope(folder); err != nil {
		return out, err
	}
	switch opts.Direction {
	case Both:
		return out, s.merge(ctx, drive, out, report)
	case Push:
		err = s.push(drive, out, report)
	default:
		err = s.pull(ctx, drive, out, report)
	}
	if err != nil || opts.DryRun {
		return out, err
	}
	return out, s.agree(drive, folder)
}

// catalog returns what the library knows about its books and about the
// files deleted since the last upload.
func (s *Syncer) catalog() (*catalog, error) {
	books, err := s.Store.Books(store.ListOptions{})
	if err != nil {
		return nil, err
	}
	gone, err := s.Store.Deletions()
	if err != nil {
		return nil, err
	}
	return newCatalog(books, gone), nil
}

// extend makes known hold as well the books the library has now and the
// files deleted since the last upload.
func (s *Syncer) extend(known *catalog) error {
	now, err := s.catalog()
	if err != nil {
		return err
	}
	for uuid, v := range now.books {
		known.books[uuid] = v
	}
	for rel, o := range now.files {
		known.files[rel] = o
	}
	return nil
}

// push propagates local deletions and uploads the library. Once it has,
// the deletions recorded so far are forgotten.
func (s *Syncer) push(drive Drive, report *Report, progress func(Progress)) error {
	progress(Progress{Phase: PhasePreparing})
	known, err := s.catalog()
	if err != nil {
		return err
	}
	if !report.Delete {
		if err := s.removeDeleted(drive, report); err != nil {
			return err
		}
	}
	result, err := transfer(func(dryRun bool, started func(Action)) (Result, error) {
		return drive.Push(s.Library.Root(), report.Folder, report.Delete, dryRun, started)
	}, report.DryRun, stepper(PhaseUploading, progress))
	report.Push = &result
	changes := []Action{}
	for _, p := range report.Removed {
		changes = append(changes, Action{Op: "delete", Path: p})
	}
	summary := known.summarise(append(changes, result.Actions...))
	report.Mega = &summary
	if err != nil {
		return fmt.Errorf("uploading: %w", err)
	}
	if report.DryRun {
		return nil
	}
	progress(Progress{Phase: PhaseFinishing})
	if err := s.Store.ClearTombstones(); err != nil {
		return err
	}
	return s.stamp(drive, report.Folder)
}

// pull downloads the MEGA folder and brings the database up to date.
// Files deleted locally whose deletion has not reached MEGA yet come back
// with the download and are deleted again, unless local files are being
// replaced by what MEGA holds.
func (s *Syncer) pull(ctx context.Context, drive Drive, report *Report, progress func(Progress)) error {
	progress(Progress{Phase: PhasePreparing})
	known, err := s.catalog()
	if err != nil {
		return err
	}
	known.adopts = true
	gone, err := s.deleted()
	if err != nil {
		return err
	}
	result, err := transfer(func(dryRun bool, started func(Action)) (Result, error) {
		return drive.Pull(report.Folder, s.Library.Root(), report.Delete, dryRun, started)
	}, report.DryRun, stepper(PhaseDownloading, progress))
	lasting := s.lasting(report, result.Actions, gone)
	if report.DryRun {
		result.Actions = lasting
		result.tally()
	}
	report.Pull = &result
	s.study(known, drive, report, lasting)
	summary := known.summarise(lasting)
	report.Library = &summary
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	if report.DryRun {
		return nil
	}
	progress(Progress{Phase: PhaseFinishing})
	for _, file := range gone {
		if report.Delete {
			break
		}
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	rescan, err := s.Library.Rescan(ctx)
	if err != nil {
		return fmt.Errorf("rescanning the library: %w", err)
	}
	report.Rescan = &rescan
	if err := s.extend(known); err != nil {
		return err
	}
	summary = known.summarise(lasting)
	return nil
}

// lasting returns the downloads whose effect remains once the run is
// over. A file deleted here whose deletion has not reached MEGA yet is
// deleted again after it comes back, unless local files are being
// replaced by what MEGA holds. In a preview of a sync both ways, a file
// about to be uploaded is the same on both sides by the time the
// download starts.
func (s *Syncer) lasting(report *Report, actions []Action, gone []string) []Action {
	skip := map[string]bool{}
	for _, file := range gone {
		rel, err := filepath.Rel(s.Library.Root(), file)
		if err == nil && !report.Delete {
			skip[filepath.ToSlash(rel)] = true
		}
	}
	if report.DryRun && report.Push != nil {
		for _, a := range report.Push.Actions {
			if a.Op == "copy" || a.Op == "update" {
				skip[a.Path] = true
			}
		}
	}
	out := []Action{}
	for _, a := range actions {
		if a.Op == "delete" || !skip[a.Path] {
			out = append(out, a)
		}
	}
	return out
}

// study makes known hold what the details files among the downloads say
// about their books. A preview reads them from MEGA, up to previewReads
// of them; a run reads the copies it downloaded.
func (s *Syncer) study(known *catalog, drive Drive, report *Report, actions []Action) {
	reads := 0
	for _, a := range actions {
		_, details, ok := library.Kept(a.Path)
		if !ok || !details || a.Op == "delete" {
			continue
		}
		var data []byte
		var err error
		switch {
		case !report.DryRun:
			data, err = os.ReadFile(filepath.Join(s.Library.Root(), filepath.FromSlash(a.Path)))
		case reads < previewReads:
			reads++
			data, err = drive.Read(path.Join(report.Folder, a.Path))
		default:
			continue
		}
		if d, ok := library.ParseDetails(data); ok && err == nil {
			known.learn(d)
		}
	}
}

// deleted returns the local paths of the files deleted since the last
// upload that have not been put back.
func (s *Syncer) deleted() ([]string, error) {
	paths, err := s.Store.Tombstones()
	if err != nil {
		return nil, err
	}
	var gone []string
	for _, p := range paths {
		local := filepath.Join(s.Library.Root(), filepath.FromSlash(p))
		if _, err := os.Lstat(local); errors.Is(err, os.ErrNotExist) {
			gone = append(gone, local)
		}
	}
	return gone, nil
}

// removeDeleted removes from MEGA the paths deleted locally since the
// last sync, skipping any that exist again and any MEGA does not have.
func (s *Syncer) removeDeleted(drive Drive, report *Report) error {
	paths, err := s.Store.Tombstones()
	if err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := os.Lstat(filepath.Join(s.Library.Root(), filepath.FromSlash(p))); err == nil {
			continue
		}
		if !drive.Exists(path.Join(report.Folder, p)) {
			continue
		}
		report.Removed = append(report.Removed, p)
		if report.DryRun {
			continue
		}
		if err := drive.Remove(path.Join(report.Folder, p)); err != nil {
			return fmt.Errorf("removing %s from MEGA: %w", p, err)
		}
	}
	return nil
}

// stamp sets the modification time of each local file to the timestamp of
// its MEGA copy. A download treats any time difference as a change, so
// without this every uploaded file would be downloaded again by the next
// pull.
func (s *Syncer) stamp(drive Drive, folder string) error {
	entries, err := drive.Files(folder)
	if err != nil {
		return fmt.Errorf("listing MEGA files: %w", err)
	}
	for _, e := range entries {
		local := filepath.Join(s.Library.Root(), filepath.FromSlash(e.Rel))
		info, err := os.Stat(local)
		if err != nil || info.Size() != e.Size || info.ModTime().After(e.Modified) {
			continue
		}
		if err := os.Chtimes(local, e.Modified, e.Modified); err != nil {
			return err
		}
	}
	return nil
}
