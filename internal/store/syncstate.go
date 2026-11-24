package store

// SyncEntry is how a library file and its copy on MEGA looked when a sync
// last left them the same.
type SyncEntry struct {
	// LocalSize is the size of the library file in bytes.
	LocalSize int64
	// LocalMTime is the modification time of the library file, in
	// nanoseconds since the Unix epoch.
	LocalMTime int64
	// RemoteSize is the size of the copy on MEGA in bytes.
	RemoteSize int64
	// RemoteVersion identifies the copy on MEGA. It changes whenever the
	// copy is replaced.
	RemoteVersion string
}

// SyncState returns what the last syncs recorded, by library relative
// path.
func (s *Store) SyncState() (map[string]SyncEntry, error) {
	rows, err := s.db.Query(`SELECT path, local_size, local_mtime, remote_size, remote_version FROM sync_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SyncEntry{}
	for rows.Next() {
		var rel string
		var e SyncEntry
		if err := rows.Scan(&rel, &e.LocalSize, &e.LocalMTime, &e.RemoteSize, &e.RemoteVersion); err != nil {
			return nil, err
		}
		out[rel] = e
	}
	return out, rows.Err()
}

// PutSyncState records that the library file at rel and its copy on MEGA
// are the same, and how they look.
func (s *Store) PutSyncState(rel string, e SyncEntry) error {
	_, err := s.db.Exec(`INSERT INTO sync_state (path, local_size, local_mtime, remote_size, remote_version)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET local_size = excluded.local_size, local_mtime = excluded.local_mtime,
			remote_size = excluded.remote_size, remote_version = excluded.remote_version`,
		rel, e.LocalSize, e.LocalMTime, e.RemoteSize, e.RemoteVersion)
	return err
}

// DeleteSyncState forgets what was recorded about the file at rel.
func (s *Store) DeleteSyncState(rel string) error {
	_, err := s.db.Exec(`DELETE FROM sync_state WHERE path = ?`, rel)
	return err
}

// ClearSyncState forgets everything the last syncs recorded.
func (s *Store) ClearSyncState() error {
	_, err := s.db.Exec(`DELETE FROM sync_state`)
	return err
}
