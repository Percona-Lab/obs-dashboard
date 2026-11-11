package store

import (
	"database/sql"
	"strings"
	"time"
)

// ReleaseAudit is one row of the release_audit log: who performed which
// release action on which branch (and PR, when there is one).
type ReleaseAudit struct {
	At     time.Time
	Email  string
	Action string
	Branch string
	PR     int
	Detail string
}

// AddReleaseAudit appends a row to the release audit log.
func AddReleaseAudit(db *sql.DB, a ReleaseAudit) error {
	_, err := db.Exec(`
		INSERT INTO release_audit (at, email, action, branch, pr, detail)
		VALUES (?,?,?,?,?,?)`,
		a.At.UTC().Format(time.RFC3339Nano), a.Email, a.Action, a.Branch, a.PR, a.Detail,
	)
	return err
}

// ReleaseAuditByBranch returns the audit rows for each requested branch,
// oldest first. Branches without rows are absent from the map.
func ReleaseAuditByBranch(db *sql.DB, branches []string) (map[string][]ReleaseAudit, error) {
	out := map[string][]ReleaseAudit{}
	if len(branches) == 0 {
		return out, nil
	}
	args := make([]any, len(branches))
	for i, b := range branches {
		args[i] = b
	}
	// id is the append order, which is chronological for this log.
	rows, err := db.Query(`
		SELECT at, email, action, branch, pr, detail
		FROM release_audit
		WHERE branch IN (`+placeholders(len(branches))+`)
		ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanReleaseAudit(rows)
		if err != nil {
			return nil, err
		}
		out[a.Branch] = append(out[a.Branch], a)
	}
	return out, rows.Err()
}

// ReleaseAuditByPR returns, for each requested PR number, the newest audit
// row recorded with the given action. PRs without such a row are absent.
func ReleaseAuditByPR(db *sql.DB, prs []int, action string) (map[int]ReleaseAudit, error) {
	out := map[int]ReleaseAudit{}
	if len(prs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(prs)+1)
	for _, pr := range prs {
		args = append(args, pr)
	}
	args = append(args, action)
	rows, err := db.Query(`
		SELECT at, email, action, branch, pr, detail
		FROM release_audit
		WHERE id IN (
			SELECT MAX(id) FROM release_audit
			WHERE pr IN (`+placeholders(len(prs))+`) AND action = ?
			GROUP BY pr)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanReleaseAudit(rows)
		if err != nil {
			return nil, err
		}
		out[a.PR] = a
	}
	return out, rows.Err()
}

// ReleaseAuditSince returns the rows recorded with one of actions at or after
// since, oldest first, whatever their branch.
func ReleaseAuditSince(db *sql.DB, actions []string, since time.Time) ([]ReleaseAudit, error) {
	out := []ReleaseAudit{}
	if len(actions) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(actions)+1)
	for _, a := range actions {
		args = append(args, a)
	}
	// Stored times compare as text only to the second (the fraction has no
	// fixed width): ask from one second earlier, then compare the times.
	args = append(args, since.UTC().Truncate(time.Second).Add(-time.Second).Format(time.RFC3339Nano))
	rows, err := db.Query(`
		SELECT at, email, action, branch, pr, detail
		FROM release_audit
		WHERE action IN (`+placeholders(len(actions))+`) AND at >= ?
		ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanReleaseAudit(rows)
		if err != nil {
			return nil, err
		}
		if !a.At.Before(since) {
			out = append(out, a)
		}
	}
	return out, rows.Err()
}

func scanReleaseAudit(rows *sql.Rows) (ReleaseAudit, error) {
	var a ReleaseAudit
	var at string
	if err := rows.Scan(&at, &a.Email, &a.Action, &a.Branch, &a.PR, &a.Detail); err != nil {
		return a, err
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return a, err
	}
	a.At = t.UTC()
	return a, nil
}

// placeholders returns n comma-separated SQL bind markers.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
