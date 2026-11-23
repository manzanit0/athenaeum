package megasync

import (
	"testing"
	"time"

	"github.com/manzanit0/athenaeum/internal/store"
)

// TestPlanMerge checks what a sync both ways does about a file for every
// way its two copies can stand against what the last sync recorded.
func TestPlanMerge(t *testing.T) {
	then := time.Unix(1_700_000_000, 0)
	later := then.Add(time.Hour)
	synced := store.SyncEntry{LocalSize: 10, LocalMTime: then.UnixNano(), RemoteSize: 10, RemoteVersion: "v1"}
	kept, edited := &localFile{size: 10, mtime: then}, &localFile{size: 12, mtime: later}
	old := &Entry{Size: 10, Modified: then, Version: "v1"}
	replaced := &Entry{Size: 11, Modified: then.Add(time.Minute), Version: "v2"}
	latest := &Entry{Size: 11, Modified: later.Add(time.Minute), Version: "v3"}
	cases := []struct {
		name     string
		local    *localFile
		remote   *Entry
		base     *store.SyncEntry
		gone     bool
		op       string
		conflict bool
	}{
		{name: "nothing changed", local: kept, remote: old, base: &synced},
		{name: "changed here", local: edited, remote: old, base: &synced, op: stepUpload},
		{name: "changed on MEGA", local: kept, remote: replaced, base: &synced, op: stepDownload},
		{name: "deleted here", remote: old, base: &synced, op: stepRemoveRemote},
		{name: "deleted on MEGA", local: kept, base: &synced, op: stepRemoveLocal},
		{name: "deleted on both", base: &synced, op: stepForget},
		{name: "changed here, deleted on MEGA", local: edited, base: &synced, op: stepUpload, conflict: true},
		{name: "deleted here, changed on MEGA", remote: replaced, base: &synced, op: stepDownload, conflict: true},
		{name: "changed on both, newer here", local: edited, remote: replaced, base: &synced, op: stepUpload, conflict: true},
		{name: "changed on both, newer on MEGA", local: edited, remote: latest, base: &synced, op: stepDownload, conflict: true},
		{name: "new here", local: edited, op: stepUpload},
		{name: "new on MEGA", remote: replaced, op: stepDownload},
		{name: "deleted here before any record", remote: old, gone: true, op: stepRemoveRemote},
		{name: "alike with no record", local: kept, remote: old, op: stepAdopt},
		{name: "unlike with no record, newer here", local: edited, remote: replaced, op: stepUpload, conflict: true},
		{name: "unlike with no record, newer on MEGA", local: kept, remote: replaced, op: stepDownload, conflict: true},
	}
	for _, c := range cases {
		local, remote, base := map[string]localFile{}, map[string]Entry{}, map[string]store.SyncEntry{}
		if c.local != nil {
			local["f"] = *c.local
		}
		if c.remote != nil {
			remote["f"] = *c.remote
		}
		if c.base != nil {
			base["f"] = *c.base
		}
		steps := planMerge(local, remote, base, map[string]bool{"f": c.gone})
		switch {
		case c.op == "" && len(steps) != 0:
			t.Errorf("%s: steps = %+v, want none", c.name, steps)
		case c.op == "":
		case len(steps) != 1 || steps[0].op != c.op || steps[0].conflict != c.conflict:
			t.Errorf("%s: steps = %+v, want %s (conflict %v)", c.name, steps, c.op, c.conflict)
		}
	}
}
