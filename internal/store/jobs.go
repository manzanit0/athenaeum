package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Job statuses.
const (
	// JobRunning marks a job that is still executing.
	JobRunning = "running"
	// JobDone marks a job that finished successfully.
	JobDone = "done"
	// JobFailed marks a job that finished with an error.
	JobFailed = "failed"
)

// Job is a background task such as a conversion or a sync.
type Job struct {
	// ID is the database identifier.
	ID int64 `json:"id"`
	// Kind names the type of work, such as convert or sync.
	Kind string `json:"kind"`
	// Title is a human readable summary of the work.
	Title string `json:"title"`
	// Status is one of the Job status constants.
	Status string `json:"status"`
	// Error describes why a failed job failed.
	Error string `json:"error"`
	// Result holds the JSON outcome of a finished job.
	Result json.RawMessage `json:"result,omitempty"`
	// BookID identifies the book the job works on, if any.
	BookID *int64 `json:"book_id,omitempty"`
	// CreatedAt is when the job started.
	CreatedAt time.Time `json:"created_at"`
	// FinishedAt is when the job ended.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// jobColumns lists the job columns in scan order.
const jobColumns = `id, kind, title, status, error, result, book_id, created_at, finished_at`

// scanJob reads a job row from sc.
func scanJob(sc scanner) (*Job, error) {
	var j Job
	var result, created, finished string
	var bookID sql.NullInt64
	err := sc.Scan(&j.ID, &j.Kind, &j.Title, &j.Status, &j.Error, &result, &bookID, &created, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if result != "" {
		j.Result = json.RawMessage(result)
	}
	if bookID.Valid {
		j.BookID = &bookID.Int64
	}
	j.CreatedAt = parseTime(created)
	if finished != "" {
		t := parseTime(finished)
		j.FinishedAt = &t
	}
	return &j, nil
}

// CreateJob inserts a running job and sets its ID.
func (s *Store) CreateJob(j *Job) error {
	j.Status = JobRunning
	j.CreatedAt = time.Now().UTC()
	res, err := s.db.Exec(`INSERT INTO jobs (kind, title, status, book_id, created_at)
		VALUES (?, ?, ?, ?, ?)`, j.Kind, j.Title, j.Status, j.BookID, formatTime(j.CreatedAt))
	if err != nil {
		return err
	}
	j.ID, err = res.LastInsertId()
	return err
}

// FinishJob records the outcome of the job with the given ID. A nil
// jobErr marks the job as done.
func (s *Store) FinishJob(id int64, result any, jobErr error) error {
	status, message := JobDone, ""
	if jobErr != nil {
		status, message = JobFailed, jobErr.Error()
	}
	var encoded string
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			return err
		}
		encoded = string(data)
	}
	_, err := s.db.Exec(`UPDATE jobs SET status = ?, error = ?, result = ?, finished_at = ?
		WHERE id = ?`, status, message, encoded, now(), id)
	return err
}

// FailRunningJobs marks jobs left running by a previous process as failed.
func (s *Store) FailRunningJobs() error {
	_, err := s.db.Exec(`UPDATE jobs SET status = ?, error = ?, finished_at = ?
		WHERE status = ?`, JobFailed, "interrupted by a restart", now(), JobRunning)
	return err
}

// Job returns the job with the given ID.
func (s *Store) Job(id int64) (*Job, error) {
	return scanJob(s.db.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
}

// Jobs returns the most recent jobs, newest first. An empty kind matches
// every job.
func (s *Store) Jobs(kind string, limit int) ([]*Job, error) {
	rows, err := s.db.Query(`SELECT `+jobColumns+` FROM jobs
		WHERE ? = '' OR kind = ? ORDER BY id DESC LIMIT ?`, kind, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []*Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
