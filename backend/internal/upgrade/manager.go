package upgrade

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Errors returned by Apply.
var (
	ErrDisabled           = errors.New("applying upgrades is disabled")
	ErrConflict           = errors.New("upgrade conflict")
	ErrNotFound           = errors.New("unknown release")
	ErrHostUpdateRequired = errors.New("host update required")
)

// HostUpdateError reports that a release needs newer host files first.
type HostUpdateError struct{ RequiresHost string }

func (e *HostUpdateError) Error() string {
	return "host update required: host files must be at least " + e.RequiresHost
}

// Is makes errors.Is(err, ErrHostUpdateRequired) match.
func (e *HostUpdateError) Is(target error) bool { return target == ErrHostUpdateRequired }

const keepBackups = 5

// Options configures a Manager.
type Options struct {
	DB               *sql.DB
	DBPath           string // store.db_path; backups go to <dir>/backups
	Source           ReleaseSource
	Repo             string // owner/name, shown to the UI for host-file URLs
	Current          string // running version (version.Version)
	HostFilesVersion string // OBS_DASHBOARD_HOST_FILES_VERSION
	ApplyDir         string // UPGRADE_APPLY_DIR; "" disables applying
	Interval         time.Duration
	Now              func() time.Time
}

// HostInstruction is one release's "Host changes" text.
type HostInstruction struct {
	Version     string `json:"version"`
	HostChanges string `json:"host_changes"`
}

// ReleaseView is one release row as served to the UI.
type ReleaseView struct {
	Version          string            `json:"version"`
	Notes            string            `json:"notes"`
	URL              string            `json:"url"`
	PublishedAt      time.Time         `json:"published_at"`
	Status           string            `json:"status"`
	RequestedBy      string            `json:"requested_by"`
	RequestedAt      *time.Time        `json:"requested_at"`
	FinishedAt       *time.Time        `json:"finished_at"`
	Error            string            `json:"error"`
	HostChanges      string            `json:"host_changes"`
	RequiresHost     string            `json:"requires_host"`
	Blocked          bool              `json:"blocked"`
	HostInstructions []HostInstruction `json:"host_instructions"`
}

// View is the GET /api/upgrades body.
type View struct {
	Current          string        `json:"current"`
	HostFilesVersion string        `json:"host_files_version"`
	Repo             string        `json:"repo"`
	ApplyEnabled     bool          `json:"apply_enabled"`
	InFlight         string        `json:"in_flight"`
	CheckedAt        *time.Time    `json:"checked_at"`
	Releases         []ReleaseView `json:"releases"`
}

// Manager owns release checks and the apply handshake.
type Manager struct {
	o         Options
	st        store_
	mu        sync.Mutex
	checkedAt *time.Time
}

// NewManager returns a Manager; zero Interval means 1h, nil Now means time.Now.
func NewManager(o Options) *Manager {
	if o.Interval <= 0 {
		o.Interval = time.Hour
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Manager{o: o, st: store_{o.DB}}
}

// ApplyEnabled reports whether this deployment can apply upgrades.
func (m *Manager) ApplyEnabled() bool { return m.o.ApplyDir != "" && ValidVersion(m.o.Current) }

func (m *Manager) hostFiles() string {
	if ValidVersion(m.o.HostFilesVersion) {
		return m.o.HostFilesVersion
	}
	return ""
}

// Check fetches releases and records the ones newer than the running version
// (all of them on a dev build). The network call runs outside the lock.
func (m *Manager) Check(ctx context.Context) error {
	rels, err := m.o.Source.Releases(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dev := !ValidVersion(m.o.Current)
	for _, r := range rels {
		if dev || Newer(r.Version, m.o.Current) {
			if err := m.st.upsert(r); err != nil {
				return err
			}
		}
	}
	if !dev {
		rows, err := m.st.all()
		if err != nil {
			return err
		}
		for _, r := range rows {
			if (r.Status == StatusAvailable || r.Status == StatusFailed) && !Newer(r.Version, m.o.Current) {
				if err := m.st.setStatus(r.Version, StatusSuperseded); err != nil {
					return err
				}
			}
		}
	}
	now := m.o.Now().UTC()
	m.checkedAt = &now
	return nil
}

// requirement returns the highest release in (current, target] with host
// changes, and every such release's instructions, oldest first.
func (m *Manager) requirement(rows []Row, target string) (string, []HostInstruction) {
	dev := !ValidVersion(m.o.Current)
	ins := []HostInstruction{}
	for i := len(rows) - 1; i >= 0; i-- { // rows are newest first
		r := rows[i]
		if r.HostChanges == "" || Newer(r.Version, target) {
			continue
		}
		if !dev && !Newer(r.Version, m.o.Current) {
			continue
		}
		ins = append(ins, HostInstruction{Version: r.Version, HostChanges: r.HostChanges})
	}
	if len(ins) == 0 {
		return "", ins
	}
	return ins[len(ins)-1].Version, ins
}

func (m *Manager) blocked(requires string) bool {
	if requires == "" {
		return false
	}
	hf := m.hostFiles()
	return hf == "" || Newer(requires, hf)
}

func (m *Manager) releaseView(rows []Row, r Row) ReleaseView {
	req, ins := m.requirement(rows, r.Version)
	return ReleaseView{
		Version: r.Version, Notes: r.Notes, URL: r.URL, PublishedAt: r.PublishedAt,
		Status: r.Status, RequestedBy: r.RequestedBy, RequestedAt: r.RequestedAt,
		FinishedAt: r.FinishedAt, Error: r.Error, HostChanges: r.HostChanges,
		RequiresHost: req, Blocked: m.blocked(req), HostInstructions: ins,
	}
}

// View returns the UI state: pending rows plus the last 10 finished ones.
func (m *Manager) View() (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, err := m.st.all()
	if err != nil {
		return View{}, err
	}
	v := View{
		Current: m.o.Current, HostFilesVersion: m.hostFiles(), Repo: m.o.Repo,
		ApplyEnabled: m.ApplyEnabled(), CheckedAt: m.checkedAt, Releases: []ReleaseView{},
	}
	history := 0
	for _, r := range rows {
		switch r.Status {
		case StatusAvailable, StatusApplying:
		case StatusApplied, StatusFailed:
			if history >= 10 {
				continue
			}
			history++
		default:
			continue
		}
		if r.Status == StatusApplying {
			v.InFlight = r.Version
		}
		v.Releases = append(v.Releases, m.releaseView(rows, r))
	}
	return v, nil
}

// Apply backs up the database, writes the apply request and marks the row
// applying. Nothing is written when any step before the request fails.
func (m *Manager) Apply(ctx context.Context, version, actor string) (ReleaseView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.ApplyEnabled() {
		return ReleaseView{}, ErrDisabled
	}
	inFlight, err := m.st.applying()
	if err != nil {
		return ReleaseView{}, err
	}
	if inFlight != "" {
		return ReleaseView{}, fmt.Errorf("%w: %s is already being applied", ErrConflict, inFlight)
	}
	rows, err := m.st.all()
	if err != nil {
		return ReleaseView{}, err
	}
	idx := -1
	for i, r := range rows {
		if r.Version == version {
			idx = i
		}
	}
	if idx < 0 {
		return ReleaseView{}, ErrNotFound
	}
	row := rows[idx]
	if (row.Status != StatusAvailable && row.Status != StatusFailed) || !Newer(version, m.o.Current) {
		return ReleaseView{}, fmt.Errorf("%w: %s is not applicable", ErrConflict, version)
	}
	if req, _ := m.requirement(rows, version); m.blocked(req) {
		return ReleaseView{}, &HostUpdateError{RequiresHost: req}
	}
	backup, err := m.backup(version)
	if err != nil {
		return ReleaseView{}, err
	}
	now := m.o.Now().UTC()
	if err := writeAtomic(m.o.ApplyDir, requestFile, Request{
		Version: version, Previous: m.o.Current, Backup: backup,
		RequestedBy: actor, RequestedAt: now.Format(time.RFC3339),
	}); err != nil {
		return ReleaseView{}, fmt.Errorf("write %s: %w", requestFile, err)
	}
	if err := m.st.markApplying(version, actor, now); err != nil {
		return ReleaseView{}, err
	}
	slog.Info("upgrades: apply requested", "version", version, "previous", m.o.Current, "by", actor, "backup", backup)
	row.Status, row.RequestedBy, row.RequestedAt, row.FinishedAt, row.Error = StatusApplying, actor, &now, nil, ""
	rows[idx] = row
	return m.releaseView(rows, row), nil
}

// backup writes a consistent copy of the database next to it and keeps the
// newest keepBackups copies.
func (m *Manager) backup(target string) (string, error) {
	dir := filepath.Join(filepath.Dir(m.o.DBPath), "backups")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("backup dir: %w", err)
	}
	name := fmt.Sprintf("pre-%s-%s.db", target, m.o.Now().UTC().Format("20060102T150405Z"))
	path := filepath.Join(dir, name)
	if _, err := m.o.DB.Exec(`VACUUM INTO ?`, path); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("backup: %w", err)
	}
	pruneBackups(dir, keepBackups)
	return name, nil
}

func pruneBackups(dir string, keep int) {
	paths, err := filepath.Glob(filepath.Join(dir, "pre-*.db"))
	if err != nil || len(paths) <= keep {
		return
	}
	type entry struct {
		path string
		mod  time.Time
	}
	entries := make([]entry, 0, len(paths))
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			entries = append(entries, entry{p, fi.ModTime()})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })
	for _, e := range entries[min(keep, len(entries)):] {
		if err := os.Remove(e.path); err != nil {
			slog.Warn("upgrades: prune backup", "path", e.path, "err", err)
		}
	}
}

// Reconcile consumes the host's apply-status. At startup it also fails an
// applying row whose request and status are both gone.
func (m *Manager) Reconcile(startup bool) {
	if m.o.ApplyDir == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	st, present, err := readStatus(m.o.ApplyDir)
	if err != nil {
		slog.Error("upgrades: cannot read apply-status; leaving it for an operator", "err", err)
		return
	}
	applying, err := m.st.applying()
	if err != nil {
		slog.Error("upgrades: reconcile", "err", err)
		return
	}
	now := m.o.Now().UTC()
	if present {
		target := applying
		if st.Version != "" && st.Version == applying {
			target = st.Version
		}
		if target == "" {
			target = st.Version
		}
		finished := now
		if t, err := time.Parse(time.RFC3339, st.FinishedAt); err == nil {
			finished = t
		}
		if target != "" {
			if err := m.st.markFinished(target, st.OK, st.Error, finished); err != nil {
				slog.Error("upgrades: reconcile", "err", err)
				return
			}
		}
		if err := os.Remove(filepath.Join(m.o.ApplyDir, statusFile)); err != nil {
			slog.Warn("upgrades: remove apply-status", "err", err)
		}
		slog.Info("upgrades: apply finished", "version", target, "ok", st.OK, "error", st.Error)
		return
	}
	if startup && applying != "" {
		if _, err := os.Stat(filepath.Join(m.o.ApplyDir, requestFile)); os.IsNotExist(err) {
			if err := m.st.markFinished(applying, false, "interrupted", now); err != nil {
				slog.Error("upgrades: reconcile", "err", err)
			}
			slog.Warn("upgrades: in-flight upgrade was interrupted", "version", applying)
		}
	}
}

// Run checks for releases now and every Interval, and reconciles the apply
// status every 15s while applying is enabled. Call Reconcile(true) before it.
func (m *Manager) Run(ctx context.Context) {
	m.checkLogged(ctx)
	check := time.NewTicker(m.o.Interval)
	defer check.Stop()
	var reconcile <-chan time.Time
	if m.o.ApplyDir != "" {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		reconcile = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-check.C:
			m.checkLogged(ctx)
		case <-reconcile:
			m.Reconcile(false)
		}
	}
}

func (m *Manager) checkLogged(ctx context.Context) {
	if err := m.Check(ctx); err != nil {
		slog.Warn("upgrades: release check failed", "err", err)
	}
}
