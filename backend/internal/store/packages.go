package store

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
)

// UpsertPackageState inserts or replaces a package row.
func UpsertPackageState(db *sql.DB, p *model.Package, now time.Time) error {
	// Read current targets for state-duration recording (before overwrite).
	var prevTargetsJSON string
	var prevIsContainer sql.NullInt64
	var prevTargets []model.Target
	db.QueryRow(`SELECT targets_json, is_container FROM packages WHERE project = ? AND name = ?`,
		p.Project, p.Name).Scan(&prevTargetsJSON, &prevIsContainer)
	_ = json.Unmarshal([]byte(prevTargetsJSON), &prevTargets)

	targetsJSON, err := json.Marshal(targetsForStorage(p.Targets))
	if err != nil {
		return err
	}

	// Splice container tag when is_container is true now or was true previously.
	isContainer := (p.IsContainer != nil && *p.IsContainer) ||
		(prevIsContainer.Valid && prevIsContainer.Int64 != 0)
	mergedTags := p.Tags
	if isContainer {
		seen := make(map[string]bool, len(p.Tags)+1)
		for _, t := range p.Tags {
			seen[t] = true
		}
		if !seen["container"] {
			mergedTags = append(append([]string(nil), p.Tags...), "container")
		}
	}
	tagsJSON, err := json.Marshal(mergedTags)
	if err != nil {
		return err
	}
	if tagsJSON == nil || string(tagsJSON) == "null" {
		tagsJSON = []byte("[]")
	}
	containerTagsJSON, err := json.Marshal(p.ContainerTags)
	if err != nil {
		return err
	}
	if containerTagsJSON == nil {
		containerTagsJSON = []byte("[]")
	}

	var trigWhat, trigKind sql.NullString
	var trigAt sql.NullTime
	if p.Trigger != nil {
		trigWhat = sql.NullString{String: p.Trigger.What, Valid: true}
		trigKind = sql.NullString{String: p.Trigger.Kind, Valid: true}
		trigAt = sql.NullTime{Time: p.Trigger.At, Valid: true}
	}
	var isContainerVal interface{}
	if p.IsContainer != nil {
		if *p.IsContainer {
			isContainerVal = 1
		} else {
			isContainerVal = 0
		}
	}
	isReleaseVal := 0
	if p.IsRelease {
		isReleaseVal = 1
	}
	settledVal := 0
	if p.Settled {
		settledVal = 1
	}

	_, err = db.Exec(`
		INSERT INTO packages
			(project, name, rollup_state, ok_targets, total_targets,
			 trigger_what, trigger_kind, trigger_at, targets_json, updated_at,
			 state_changed_at, is_container, version, container_tags, tags, is_release, settled)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(project, name) DO UPDATE SET
			rollup_state=excluded.rollup_state,
			ok_targets=excluded.ok_targets,
			total_targets=excluded.total_targets,
			trigger_what=excluded.trigger_what,
			trigger_kind=excluded.trigger_kind,
			trigger_at=excluded.trigger_at,
			targets_json=excluded.targets_json,
			updated_at=excluded.updated_at,
			is_container=CASE WHEN excluded.is_container IS NOT NULL
			                   THEN excluded.is_container ELSE is_container END,
			version=excluded.version,
			container_tags=excluded.container_tags,
			tags=CASE WHEN excluded.tags != '[]' THEN excluded.tags ELSE tags END,
			is_release=CASE WHEN excluded.is_release != 0 THEN 1 ELSE is_release END,
			settled=excluded.settled,
			state_changed_at = CASE
				WHEN excluded.rollup_state != rollup_state THEN excluded.state_changed_at
				WHEN state_changed_at IS NULL              THEN excluded.state_changed_at
				ELSE state_changed_at
			END`,
		p.Project, p.Name, string(p.RollupState),
		p.OKTargets, p.TotalTargets,
		trigWhat, trigKind, trigAt,
		string(targetsJSON), p.UpdatedAt, now,
		isContainerVal, p.Version, string(containerTagsJSON),
		string(tagsJSON), isReleaseVal, settledVal,
	)
	if err != nil {
		return err
	}

	// Record state duration transitions (errors are non-fatal).
	recordStateTransitions(db, p.Project, p.Name, prevTargets, p.Targets, now)
	_ = attachTargetStartedAt(db, []*model.Package{p})
	return nil
}

func targetsForStorage(targets []model.Target) []model.Target {
	out := make([]model.Target, len(targets))
	for i, t := range targets {
		t.StartedAt = nil
		out[i] = t
	}
	return out
}

// recordStateTransitions updates target_state_durations when a target's state
// changes or a new target appears. Called only from UpsertPackageState.
func recordStateTransitions(db *sql.DB, project, pkg string, prev, next []model.Target, now time.Time) {
	nowStr := now.UTC().Format(time.RFC3339Nano)
	oldByKey := make(map[string]model.Target, len(prev))
	for _, t := range prev {
		oldByKey[t.Repo+"/"+t.Arch] = t
	}
	newKeys := make(map[string]bool, len(next))
	for _, t := range next {
		key := t.Repo + "/" + t.Arch
		newKeys[key] = true
		old, existed := oldByKey[key]
		if existed && old.State == t.State {
			continue // no change
		}
		if existed {
			// Close the previous open entry.
			db.Exec(`
				UPDATE target_state_durations
				SET exited_at = ?,
				    duration_ms = CAST((julianday(?) - julianday(entered_at)) * 86400000 AS INTEGER)
				WHERE project = ? AND package = ? AND repo = ? AND arch = ?
				  AND state = ? AND exited_at IS NULL`,
				nowStr, nowStr, project, pkg, t.Repo, t.Arch, old.State,
			)
		}
		// Open a new entry for the new state.
		db.Exec(`
			INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			project, pkg, t.Repo, t.Arch, t.State, nowStr,
		)
	}
	// Close open entries for targets that no longer exist in next.
	for _, t := range prev {
		if !newKeys[t.Repo+"/"+t.Arch] {
			db.Exec(`
				UPDATE target_state_durations
				SET exited_at = ?,
				    duration_ms = CAST((julianday(?) - julianday(entered_at)) * 86400000 AS INTEGER)
				WHERE project = ? AND package = ? AND repo = ? AND arch = ?
				  AND exited_at IS NULL`,
				nowStr, nowStr, project, pkg, t.Repo, t.Arch,
			)
		}
	}
}

// DeletePackagesByProject removes all package rows for an exact project name.
// Used by the poller to garbage-collect projects that no longer exist in OBS.
func DeletePackagesByProject(db *sql.DB, project string) error {
	if _, err := db.Exec(`DELETE FROM target_state_durations WHERE project = ?`, project); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM events WHERE project = ?`, project); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM cve_scans WHERE project = ?`, project); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM cve_periods WHERE project = ?`, project); err != nil {
		return err
	}
	res, err := db.Exec(`DELETE FROM packages WHERE project = ?`, project)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		slog.Info("store: deleted packages for project", "project", project, "count", n)
	}
	return nil
}

// DeletePackage removes a single package row.
// Used by the MQ consumer on package.delete events.
func DeletePackage(db *sql.DB, project, name string) error {
	if _, err := db.Exec(`DELETE FROM target_state_durations WHERE project = ? AND package = ?`, project, name); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM events WHERE project = ? AND package = ?`, project, name); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM cve_scans WHERE project = ? AND package = ?`, project, name); err != nil {
		return err
	}
	if _, err := db.Exec(`DELETE FROM cve_periods WHERE project = ? AND package = ?`, project, name); err != nil {
		return err
	}
	res, err := db.Exec(`DELETE FROM packages WHERE project = ? AND name = ?`, project, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		slog.Info("store: deleted package", "project", project, "pkg", name)
	}
	return nil
}

const packageSelectCols = ` project, name, rollup_state, ok_targets, total_targets,
	trigger_what, trigger_kind, trigger_at, targets_json, updated_at,
	state_changed_at, is_container, version, container_tags, tags, is_release, settled`

// scanPackages is a helper that extracts the scan loop pattern used by multiple query functions.
// It expects rows to have been created with the standard package column order defined
// in packageSelectCols: project, name, rollup_state, ok_targets, total_targets,
// trigger_what, trigger_kind, trigger_at, targets_json, updated_at, state_changed_at,
// is_container, version, container_tags, tags, is_release, settled.
func scanPackages(db *sql.DB, rows *sql.Rows) ([]*model.Package, error) {
	pkgs := make([]*model.Package, 0)
	for rows.Next() {
		p := &model.Package{}
		var trigWhat, trigKind sql.NullString
		var trigAt sql.NullTime
		var targetsJSON string
		var stateChangedAt sql.NullTime
		var isContainerNull sql.NullInt64
		var containerTagsJSON string
		var tagsJSON string
		var isRelease int
		var settled int
		if err := rows.Scan(
			&p.Project, &p.Name, &p.RollupState,
			&p.OKTargets, &p.TotalTargets,
			&trigWhat, &trigKind, &trigAt,
			&targetsJSON, &p.UpdatedAt,
			&stateChangedAt, &isContainerNull, &p.Version,
			&containerTagsJSON, &tagsJSON, &isRelease, &settled,
		); err != nil {
			return nil, err
		}
		if isContainerNull.Valid {
			v := isContainerNull.Int64 != 0
			p.IsContainer = &v
		}
		if trigWhat.Valid {
			p.Trigger = &model.Trigger{
				What: trigWhat.String,
				Kind: trigKind.String,
				At:   trigAt.Time,
			}
		}
		if stateChangedAt.Valid {
			t := stateChangedAt.Time
			p.StateChangedAt = &t
		}
		if err := json.Unmarshal([]byte(targetsJSON), &p.Targets); err != nil {
			return nil, err
		}
		if containerTagsJSON != "" && containerTagsJSON != "[]" {
			if err := json.Unmarshal([]byte(containerTagsJSON), &p.ContainerTags); err != nil {
				return nil, err
			}
		}
		if tagsJSON != "" && tagsJSON != "[]" {
			if err := json.Unmarshal([]byte(tagsJSON), &p.Tags); err != nil {
				return nil, err
			}
		}
		p.IsRelease = isRelease != 0
		p.Settled = settled != 0
		pkgs = append(pkgs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := attachTargetStartedAt(db, pkgs); err != nil {
		return nil, err
	}
	return pkgs, nil
}

func attachTargetStartedAt(db *sql.DB, pkgs []*model.Package) error {
	if len(pkgs) == 0 {
		return nil
	}

	type targetKey struct {
		project string
		pkg     string
		repo    string
		arch    string
		state   string
	}

	type packageKey struct {
		project string
		pkg     string
	}

	seenPackages := make(map[packageKey]bool, len(pkgs))
	packageKeys := make([]packageKey, 0, len(pkgs))
	for _, pkg := range pkgs {
		if len(pkg.Targets) == 0 {
			continue
		}
		k := packageKey{project: pkg.Project, pkg: pkg.Name}
		if seenPackages[k] {
			continue
		}
		seenPackages[k] = true
		packageKeys = append(packageKeys, k)
	}
	if len(packageKeys) == 0 {
		return nil
	}

	startedAt := make(map[targetKey]time.Time)
	const chunkSize = 400
	for start := 0; start < len(packageKeys); start += chunkSize {
		end := start + chunkSize
		if end > len(packageKeys) {
			end = len(packageKeys)
		}
		chunk := packageKeys[start:end]
		values := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*2)
		for i, k := range chunk {
			values[i] = "(?, ?)"
			args = append(args, k.project, k.pkg)
		}

		rows, err := db.Query(`
			WITH wanted(project, pkg_name) AS (VALUES `+strings.Join(values, ", ")+`)
			SELECT d.project, d.package, d.repo, d.arch, d.state, d.entered_at
			FROM target_state_durations d
			JOIN wanted w ON w.project = d.project AND w.pkg_name = d.package
			WHERE d.exited_at IS NULL`, args...)
		if err != nil {
			return err
		}

		for rows.Next() {
			var k targetKey
			var enteredAt string
			if err := rows.Scan(&k.project, &k.pkg, &k.repo, &k.arch, &k.state, &enteredAt); err != nil {
				rows.Close()
				return err
			}
			t, err := time.Parse(time.RFC3339Nano, enteredAt)
			if err != nil {
				rows.Close()
				return err
			}
			startedAt[k] = t
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}

	for _, pkg := range pkgs {
		for i, target := range pkg.Targets {
			k := targetKey{
				project: pkg.Project,
				pkg:     pkg.Name,
				repo:    target.Repo,
				arch:    target.Arch,
				state:   target.State,
			}
			if t, ok := startedAt[k]; ok {
				start := t
				pkg.Targets[i].StartedAt = &start
			}
		}
	}
	return nil
}

// BlockedTarget identifies one build target currently stuck in blocked state.
type BlockedTarget struct {
	Project   string
	Package   string
	Repo      string
	Arch      string
	EnteredAt time.Time
}

// QueryStaleBlockedTargets returns targets whose CURRENT state is 'blocked'
// and was entered before cutoff, restricted to non-release projects
// (devel/staging/PRs). Consumed by the unblocker sweeper.
func QueryStaleBlockedTargets(db *sql.DB, cutoff time.Time) ([]BlockedTarget, error) {
	rows, err := db.Query(`
		SELECT d.project, d.package, d.repo, d.arch, d.entered_at
		FROM target_state_durations d
		JOIN packages p ON p.project = d.project AND p.name = d.package
		WHERE d.state = 'blocked' AND d.exited_at IS NULL
		  AND d.entered_at < ?
		  AND p.is_release = 0
		ORDER BY d.entered_at`,
		cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlockedTarget
	for rows.Next() {
		var t BlockedTarget
		var entered string
		if err := rows.Scan(&t.Project, &t.Package, &t.Repo, &t.Arch, &entered); err != nil {
			return nil, err
		}
		ts, err := time.Parse(time.RFC3339Nano, entered)
		if err != nil {
			return nil, err
		}
		t.EnteredAt = ts
		out = append(out, t)
	}
	return out, rows.Err()
}

// QueryDistinctRepos returns the sorted list of distinct OBS repository names
// (the "repo" field from targets_json) for non-container packages matching the
// given project prefix.
func QueryDistinctRepos(db *sql.DB, projectPrefix string) ([]string, error) {
	// Exclude confirmed container images (is_container=1) so that their build
	// repos aren't returned as package repos. Packages in :containers: subprojects
	// with is_container=0 (e.g. PR builds) are intentionally included.
	rows, err := db.Query(
		`SELECT targets_json FROM packages
		 WHERE project LIKE ? AND (is_container IS NULL OR is_container = 0)`,
		projectPrefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seen := map[string]struct{}{}
	for rows.Next() {
		var targetsJSON string
		if err := rows.Scan(&targetsJSON); err != nil {
			return nil, err
		}
		var targets []struct {
			Repo string `json:"repo"`
		}
		if err := json.Unmarshal([]byte(targetsJSON), &targets); err != nil {
			continue
		}
		for _, t := range targets {
			if t.Repo != "" {
				seen[t.Repo] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	repos := make([]string, 0, len(seen))
	for r := range seen {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	return repos, nil
}

func scanDistinctRepos(rows *sql.Rows) ([]string, error) {
	defer rows.Close()

	seen := map[string]struct{}{}
	for rows.Next() {
		var targetsJSON string
		if err := rows.Scan(&targetsJSON); err != nil {
			return nil, err
		}
		var targets []struct {
			Repo string `json:"repo"`
		}
		if err := json.Unmarshal([]byte(targetsJSON), &targets); err != nil {
			continue
		}
		for _, t := range targets {
			if t.Repo != "" {
				seen[t.Repo] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	repos := make([]string, 0, len(seen))
	for r := range seen {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	return repos, nil
}

// QueryPackages returns all packages for a given OBS project prefix.
func QueryPackages(db *sql.DB, projectPrefix string) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages WHERE project LIKE ? ORDER BY project, name`,
		projectPrefix+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}

// QueryPRBuildPackages returns all packages under a PR (every subproject), matching
// the whole-PR project prefix PR:<pr>.
func QueryPRBuildPackages(db *sql.DB, pr string) ([]*model.Package, error) {
	p := "PR:" + pr
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages
		WHERE is_release = 0
		  AND (project = ? OR project LIKE ? || ':%')
		ORDER BY project, name`,
		p, p,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}

// QueryBuildPackages returns build packages for one product tier
// (product:tier[:version] subtree, subprojects included — the client
// splits them into version-extension entries) plus the shared common
// projects: product:common and common appear in both devel and
// staging views, in the versioned and version-less branches alike (the
// board only ever issues the version-less "_" query and filters client-side).
// product is e.g. "ppg", tier is "devel"/"staging", version is "17".
// When version is "_" or "", all versions under the product:tier subtree are returned.
func QueryBuildPackages(db *sql.DB, product, tier, version string) ([]*model.Package, error) {
	gp := "common"
	cp := product + ":common"
	var rows *sql.Rows
	var err error
	if version == "_" || version == "" {
		pp := product + ":" + tier
		rows, err = db.Query(`SELECT`+packageSelectCols+`
			FROM packages
			WHERE is_release = 0
			  AND (  (project = ? OR project LIKE ? || ':%')
			      OR (project = ? OR project LIKE ? || ':%')
			      OR (project = ? OR project LIKE ? || ':%') )
			ORDER BY project, name`,
			pp, pp, cp, cp, gp, gp,
		)
	} else {
		vp := product + ":" + tier + ":" + version
		rows, err = db.Query(`SELECT`+packageSelectCols+`
			FROM packages
			WHERE is_release = 0
			  AND (  (project = ? OR project LIKE ? || ':%')
			      OR (project = ? OR project LIKE ? || ':%')
			      OR (project = ? OR project LIKE ? || ':%') )
			ORDER BY project, name`,
			vp, vp, cp, cp, gp, gp,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}

// QueryPRDistinctRepos returns the distinct build repos across all of a PR's
// packages (every subproject), matching the whole-PR project prefix PR:<pr>.
func QueryPRDistinctRepos(db *sql.DB, pr string) ([]string, error) {
	p := "PR:" + pr
	rows, err := db.Query(
		`SELECT targets_json FROM packages
		 WHERE (is_container IS NULL OR is_container = 0)
		   AND (project = ? OR project LIKE ? || ':%')`,
		p, p,
	)
	if err != nil {
		return nil, err
	}
	return scanDistinctRepos(rows)
}

// QueryReleasePackages returns packages in the release subtree (is_release=1).
// prefix is e.g. "ppg:releases".
func QueryReleasePackages(db *sql.DB, prefix string) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages
		WHERE (project = ? OR project LIKE ? || ':%')
		  AND is_release = 1
		ORDER BY project, name`,
		prefix, prefix,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}

// GetActivePackages returns packages that have not settled and still need
// worker attention: the working set the worker seeds on startup. A package
// is excluded once the worker has determined there is nothing left to poll
// for it (settled = 1); this governs working-set membership only and is
// independent of the displayed rollup_state.
func GetActivePackages(db *sql.DB) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT` + packageSelectCols + `
		FROM packages
		WHERE settled = 0
		ORDER BY project, name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}

// GetVersionlessBuiltPackages returns settled, built, non-container packages
// whose version is still empty. Used once at startup to re-seed the working
// set so VersionTask can resolve aggregate packages (which report no versrel
// of their own) without waiting for their next build; each gets one pass and
// settles out again.
func GetVersionlessBuiltPackages(db *sql.DB) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages
		WHERE settled = 1
		  AND version = ''
		  AND COALESCE(is_container, 0) = 0
		  AND rollup_state IN (?, ?)
		ORDER BY project, name`,
		string(model.RollupSucceeded), string(model.RollupPublished),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}

// GetFinishedPackagesByProject returns succeeded packages for a project.
// Used by the MQ consumer on repo.published to signal packages for a publish
// state re-check via the worker pool.
func GetFinishedPackagesByProject(db *sql.DB, project string) ([]*model.Package, error) {
	rows, err := db.Query(`SELECT`+packageSelectCols+`
		FROM packages WHERE project = ? AND rollup_state = 'succeeded'`,
		project,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPackages(db, rows)
}
