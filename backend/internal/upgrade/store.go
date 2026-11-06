package upgrade

import (
	"database/sql"
	"errors"
	"sort"
	"time"
)

// Upgrade row statuses.
const (
	StatusAvailable  = "available"
	StatusApplying   = "applying"
	StatusApplied    = "applied"
	StatusFailed     = "failed"
	StatusSuperseded = "superseded"
)

// Row is a release plus its upgrade state.
type Row struct {
	Release
	Status      string
	RequestedBy string
	RequestedAt *time.Time
	FinishedAt  *time.Time
	Error       string
}

type store_ struct{ db *sql.DB }

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, ns.String)
	if err != nil {
		return nil
	}
	return &t
}

// upsert inserts r as available, or refreshes its release fields. Statuses
// are kept, except superseded, which becomes available again (r is newer
// than the running version again, e.g. after a manual rollback).
func (s store_) upsert(r Release) error {
	_, err := s.db.Exec(`INSERT INTO upgrades (version, notes, url, host_changes, published_at, status)
VALUES (?, ?, ?, ?, ?, 'available')
ON CONFLICT(version) DO UPDATE SET
  notes = excluded.notes,
  url = excluded.url,
  host_changes = excluded.host_changes,
  published_at = excluded.published_at,
  status = CASE WHEN upgrades.status = 'superseded' THEN 'available' ELSE upgrades.status END`,
		r.Version, r.Notes, r.URL, r.HostChanges, fmtTime(r.PublishedAt))
	return err
}

// all returns every row, newest version first.
func (s store_) all() ([]Row, error) {
	rows, err := s.db.Query(`SELECT version, notes, url, host_changes, published_at, status,
  requested_by, requested_at, finished_at, error FROM upgrades`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var published string
		var by, reqAt, finAt, errText sql.NullString
		if err := rows.Scan(&r.Version, &r.Notes, &r.URL, &r.HostChanges, &published, &r.Status, &by, &reqAt, &finAt, &errText); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, published); err == nil {
			r.PublishedAt = t
		}
		r.RequestedBy = by.String
		r.RequestedAt = parseTime(reqAt)
		r.FinishedAt = parseTime(finAt)
		r.Error = errText.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return Newer(out[i].Version, out[j].Version) })
	return out, nil
}

func (s store_) setStatus(version, status string) error {
	_, err := s.db.Exec(`UPDATE upgrades SET status = ? WHERE version = ?`, status, version)
	return err
}

func (s store_) markApplying(version, by string, at time.Time) error {
	_, err := s.db.Exec(`UPDATE upgrades SET status = 'applying', requested_by = ?, requested_at = ?,
  finished_at = NULL, error = NULL WHERE version = ?`, by, fmtTime(at), version)
	return err
}

func (s store_) markFinished(version string, ok bool, errText string, at time.Time) error {
	status := StatusFailed
	if ok {
		status = StatusApplied
	}
	_, err := s.db.Exec(`UPDATE upgrades SET status = ?, error = ?, finished_at = ? WHERE version = ?`,
		status, errText, fmtTime(at), version)
	return err
}

// applying returns the version currently being applied, or "".
func (s store_) applying() (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT version FROM upgrades WHERE status = 'applying' LIMIT 1`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
