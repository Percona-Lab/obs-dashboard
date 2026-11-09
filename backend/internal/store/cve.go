package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// UpsertCveScan inserts or replaces one arch scan result, maintaining the
// six-state CVE age transition machine in a single transaction.
func UpsertCveScan(db *sql.DB, project, pkg string, scan model.CveScan) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var prevCveSince, prevCleanSince sql.NullString
	if err := tx.QueryRow(
		`SELECT cve_since, clean_since FROM cve_scans WHERE project=? AND package=? AND repo=? AND arch=?`,
		project, pkg, scan.Repo, scan.Arch,
	).Scan(&prevCveSince, &prevCleanSince); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	hasVulns := scan.CriticalCount > 0 || scan.HighCount > 0
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var newCveSince, newCleanSince sql.NullString

	if hasVulns {
		if prevCveSince.Valid {
			newCveSince = prevCveSince // carry forward
		} else {
			newCveSince = sql.NullString{String: now, Valid: true}
		}
		// newCleanSince stays zero-value (NULL)
	} else {
		if prevCveSince.Valid {
			// CVE→clean transition: record completed episode
			if _, err := tx.Exec(
				`INSERT OR IGNORE INTO cve_periods (project, package, repo, arch, cve_since, clean_since) VALUES (?, ?, ?, ?, ?, ?)`,
				project, pkg, scan.Repo, scan.Arch, prevCveSince.String, now,
			); err != nil {
				return err
			}
		}
		if prevCleanSince.Valid {
			newCleanSince = prevCleanSince // carry forward
		} else {
			newCleanSince = sql.NullString{String: now, Valid: true}
		}
		// newCveSince stays zero-value (NULL)
	}

	findingsJSON, err := json.Marshal(scan.Findings)
	if err != nil {
		return err
	}

	_, err = tx.Exec(`
		INSERT OR REPLACE INTO cve_scans
			(project, package, repo, arch, image_ref, scanned_at, critical_count, high_count, findings_json, cve_since, clean_since)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		project, pkg, scan.Repo, scan.Arch, scan.ImageRef,
		scan.ScannedAt.UTC().Format(time.RFC3339),
		scan.CriticalCount, scan.HighCount, string(findingsJSON),
		newCveSince, newCleanSince,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// QueryCveScans returns all arch scan results for a package.
func QueryCveScans(db *sql.DB, project, pkg string) ([]model.CveScan, error) {
	rows, err := db.Query(`
		SELECT repo, arch, image_ref, scanned_at, critical_count, high_count, findings_json, cve_since, clean_since
		FROM cve_scans WHERE project = ? AND package = ?`,
		project, pkg,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCveRows(rows)
}

// AttachCveScans batch-fetches CVE scan results for a slice of packages and
// sets each package's CveScans field. One SQL query for all packages.
func AttachCveScans(db *sql.DB, packages []*model.Package) error {
	if len(packages) == 0 {
		return nil
	}
	// Callers must not pass duplicate (project, name) pairs; if they do, only
	// the first pointer in the slice receives CVE data.
	keys := make([]interface{}, 0, len(packages))
	index := make(map[string]*model.Package, len(packages))
	for _, p := range packages {
		k := p.Project + "/" + p.Name
		if _, seen := index[k]; !seen {
			keys = append(keys, k)
			index[k] = p
		}
	}
	placeholders := make([]byte, 0, len(keys)*2)
	for i := range keys {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
	}
	rows, err := db.Query(
		`SELECT project, package, repo, arch, image_ref, scanned_at, critical_count, high_count, findings_json, cve_since, clean_since
		 FROM cve_scans WHERE project || '/' || package IN (`+string(placeholders)+`)`,
		keys...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var project, pkg string
		var scan model.CveScan
		var scannedAtStr string
		var findingsJSON string
		var cveSinceStr, cleanSinceStr sql.NullString
		if err := rows.Scan(&project, &pkg, &scan.Repo, &scan.Arch, &scan.ImageRef,
			&scannedAtStr, &scan.CriticalCount, &scan.HighCount, &findingsJSON,
			&cveSinceStr, &cleanSinceStr); err != nil {
			return err
		}
		var parseErr error
		scan.ScannedAt, parseErr = time.Parse(time.RFC3339, scannedAtStr)
		if parseErr != nil {
			return fmt.Errorf("cve_scans: invalid scanned_at %q: %w", scannedAtStr, parseErr)
		}
		applyNullableTimestamps(&scan, cveSinceStr, cleanSinceStr)
		if findingsJSON != "" && findingsJSON != "[]" {
			_ = json.Unmarshal([]byte(findingsJSON), &scan.Findings)
		}
		k := project + "/" + pkg
		if p, ok := index[k]; ok {
			p.CveScans = append(p.CveScans, scan)
		}
	}
	return rows.Err()
}

// QueryCvePeriods returns completed CVE episodes for a package, newest first.
func QueryCvePeriods(db *sql.DB, project, pkg string) ([]model.CvePeriod, error) {
	rows, err := db.Query(`
		SELECT arch, cve_since, clean_since
		FROM cve_periods
		WHERE project = ? AND package = ?
		ORDER BY cve_since DESC`,
		project, pkg,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var periods []model.CvePeriod
	for rows.Next() {
		var p model.CvePeriod
		p.Project = project
		p.Package = pkg
		var cveSinceStr, cleanSinceStr string
		if err := rows.Scan(&p.Arch, &cveSinceStr, &cleanSinceStr); err != nil {
			return nil, err
		}
		p.CveSince, _ = parseRFC3339(cveSinceStr)
		p.CleanSince, _ = parseRFC3339(cleanSinceStr)
		periods = append(periods, p)
	}
	return periods, rows.Err()
}

// QueryPublishedContainers returns all confirmed container packages with rollup_state='published'.
func QueryPublishedContainers(db *sql.DB) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT` + packageSelectCols + `
		FROM packages WHERE is_container = 1 AND rollup_state = 'published'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}

// GetPackage returns a single package by primary key, or nil if not found.
func GetPackage(db *sql.DB, project, name string) (*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages WHERE project = ? AND name = ?`, project, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pkgs, err := scanPackages(db, rows)
	if err != nil || len(pkgs) == 0 {
		return nil, err
	}
	return pkgs[0], nil
}

// applyNullableTimestamps parses cve_since and clean_since from sql.NullString
// into the corresponding *time.Time fields on a CveScan.
func applyNullableTimestamps(scan *model.CveScan, cveSinceStr, cleanSinceStr sql.NullString) {
	if cveSinceStr.Valid {
		if t, err := parseRFC3339(cveSinceStr.String); err == nil {
			scan.CveSince = &t
		}
	}
	if cleanSinceStr.Valid {
		if t, err := parseRFC3339(cleanSinceStr.String); err == nil {
			scan.CleanSince = &t
		}
	}
}

// parseRFC3339 parses a time string in RFC3339Nano format.
// time.RFC3339Nano also matches plain RFC3339 (fractional seconds are optional).
func parseRFC3339(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

func scanCveRows(rows *sql.Rows) ([]model.CveScan, error) {
	var scans []model.CveScan
	for rows.Next() {
		var scan model.CveScan
		var scannedAtStr, findingsJSON string
		var cveSinceStr, cleanSinceStr sql.NullString
		if err := rows.Scan(&scan.Repo, &scan.Arch, &scan.ImageRef, &scannedAtStr,
			&scan.CriticalCount, &scan.HighCount, &findingsJSON,
			&cveSinceStr, &cleanSinceStr); err != nil {
			return nil, err
		}
		var parseErr error
		scan.ScannedAt, parseErr = time.Parse(time.RFC3339, scannedAtStr)
		if parseErr != nil {
			return nil, fmt.Errorf("cve_scans: invalid scanned_at %q: %w", scannedAtStr, parseErr)
		}
		applyNullableTimestamps(&scan, cveSinceStr, cleanSinceStr)
		if findingsJSON != "" && findingsJSON != "[]" {
			_ = json.Unmarshal([]byte(findingsJSON), &scan.Findings)
		}
		scans = append(scans, scan)
	}
	return scans, rows.Err()
}

// VulnerableImage is one image (a package in one repo) whose latest scan on
// at least one arch has HIGH or CRITICAL findings.
type VulnerableImage struct {
	Project, Package, Repo string
}

// QueryVulnerableImages lists every image that currently carries HIGH or
// CRITICAL CVEs, ordered by project, package, repo.
func QueryVulnerableImages(db *sql.DB) ([]VulnerableImage, error) {
	rows, err := db.Query(`
		SELECT DISTINCT project, package, repo FROM cve_scans
		WHERE critical_count + high_count > 0
		ORDER BY project, package, repo`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VulnerableImage
	for rows.Next() {
		var v VulnerableImage
		if err := rows.Scan(&v.Project, &v.Package, &v.Repo); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
