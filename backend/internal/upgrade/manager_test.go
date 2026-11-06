package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type fakeSource struct {
	rels []Release
	err  error
}

func (f *fakeSource) Releases(context.Context) ([]Release, error) { return f.rels, f.err }

var fixedNow = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

type harness struct {
	m        *Manager
	src      *fakeSource
	applyDir string
	dbPath   string
}

func newHarness(t *testing.T, current string, mod func(*Options)) harness {
	t.Helper()
	db, path := openTestDB(t)
	applyDir := t.TempDir()
	src := &fakeSource{}
	o := Options{DB: db, DBPath: path, Source: src, Current: current, ApplyDir: applyDir,
		Repo: "Percona-Lab/obs-dashboard", Now: func() time.Time { return fixedNow }}
	if mod != nil {
		mod(&o)
	}
	return harness{m: NewManager(o), src: src, applyDir: applyDir, dbPath: path}
}

func (h harness) view(t *testing.T) View {
	t.Helper()
	v, err := h.m.View()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func viewVersions(v View) []string {
	out := []string{}
	for _, r := range v.Releases {
		out = append(out, r.Version+":"+r.Status)
	}
	return out
}

func TestCheckListsOnlyNewer(t *testing.T) {
	h := newHarness(t, "v0.2.0", nil)
	h.src.rels = []Release{rel("v0.1.0", ""), rel("v0.2.0", ""), rel("v0.4.0", ""), rel("v0.3.0", "")}
	if err := h.m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	v := h.view(t)
	if got := strings.Join(viewVersions(v), ","); got != "v0.4.0:available,v0.3.0:available" {
		t.Fatalf("releases = %s", got)
	}
	if v.CheckedAt == nil || !v.CheckedAt.Equal(fixedNow) || v.Current != "v0.2.0" || !v.ApplyEnabled || v.Repo != "Percona-Lab/obs-dashboard" {
		t.Fatalf("view = %+v", v)
	}
}

func TestCheckDevListsAllButCannotApply(t *testing.T) {
	h := newHarness(t, "dev", nil)
	h.src.rels = []Release{rel("v0.1.0", ""), rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	v := h.view(t)
	if len(v.Releases) != 2 || v.ApplyEnabled {
		t.Fatalf("view = %+v", v)
	}
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a@percona.com"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckErrorKeepsState(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	h.src.err = errors.New("github down")
	if err := h.m.Check(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if len(h.view(t).Releases) != 1 {
		t.Fatal("state lost")
	}
}

func TestCheckSupersedesCaughtUpRows(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", ""), rel("v0.3.0", "")}
	_ = h.m.Check(context.Background())
	_ = h.m.st.markFinished("v0.3.0", false, "x", fixedNow) // failed, still newer
	// Restart on v0.2.0 with the same DB.
	h2 := NewManager(Options{DB: h.m.o.DB, DBPath: h.dbPath, Source: h.src, Current: "v0.2.0", ApplyDir: h.applyDir, Now: h.m.o.Now})
	_ = h2.Check(context.Background())
	got := strings.Join(viewVersions(mustView(t, h2)), ",")
	if got != "v0.3.0:failed" {
		t.Fatalf("releases = %s", got)
	}
}

func mustView(t *testing.T, m *Manager) View {
	t.Helper()
	v, err := m.View()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCheckKeepsApplyingStatus(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a@percona.com"); err != nil {
		t.Fatal(err)
	}
	_ = h.m.Check(context.Background())
	v := h.view(t)
	if v.InFlight != "v0.2.0" || v.Releases[0].Status != StatusApplying {
		t.Fatalf("view = %+v", v)
	}
}

var backupRe = regexp.MustCompile(`^pre-v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{8}T[0-9]{6}Z\.db$`)

func TestApplyWritesBackupAndRequest(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	row, err := h.m.Apply(context.Background(), "v0.2.0", "a@percona.com")
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != StatusApplying || row.RequestedBy != "a@percona.com" {
		t.Fatalf("row = %+v", row)
	}
	b, err := os.ReadFile(filepath.Join(h.applyDir, "apply-request"))
	if err != nil {
		t.Fatal(err)
	}
	var req map[string]any
	if err := json.Unmarshal(b, &req); err != nil {
		t.Fatal(err)
	}
	if len(req) != 5 || req["version"] != "v0.2.0" || req["previous"] != "v0.1.0" || req["requested_by"] != "a@percona.com" || req["requested_at"] != "2026-11-06T12:00:00Z" {
		t.Fatalf("request = %v", req)
	}
	name, _ := req["backup"].(string)
	if name != "pre-v0.2.0-20261106T120000Z.db" || !backupRe.MatchString(name) {
		t.Fatalf("backup name %q", name)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.dbPath), "backups", name)); err != nil {
		t.Fatalf("backup file: %v", err)
	}
	fi, _ := os.Stat(filepath.Join(h.applyDir, "apply-request"))
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
}

func TestApplyErrors(t *testing.T) {
	h := newHarness(t, "v0.2.0", nil)
	h.src.rels = []Release{rel("v0.1.0", ""), rel("v0.3.0", ""), rel("v0.4.0", "")}
	_ = h.m.Check(context.Background())
	ctx := context.Background()
	if _, err := h.m.Apply(ctx, "v9.9.9", "a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := h.m.Apply(ctx, "v0.3.0", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Apply(ctx, "v0.4.0", "a"); !errors.Is(err, ErrConflict) {
		t.Errorf("in flight: %v", err)
	}
	h2 := newHarness(t, "v0.1.0", func(o *Options) { o.ApplyDir = "" })
	h2.src.rels = []Release{rel("v0.2.0", "")}
	_ = h2.m.Check(ctx)
	if _, err := h2.m.Apply(ctx, "v0.2.0", "a"); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled: %v", err)
	}
}

func TestApplyRetriesFailed(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	_ = h.m.st.markApplying("v0.2.0", "a", fixedNow)
	_ = h.m.st.markFinished("v0.2.0", false, "boom", fixedNow)
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestApplyBackupFailureWritesNothing(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	// A file where the backups directory should be makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(filepath.Dir(h.dbPath), "backups"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a"); err == nil {
		t.Fatal("want error")
	}
	if _, err := os.Stat(filepath.Join(h.applyDir, "apply-request")); !os.IsNotExist(err) {
		t.Fatal("request written despite failed backup")
	}
	if h.view(t).Releases[0].Status != StatusAvailable {
		t.Fatal("row changed")
	}
}

func TestBackupPruneKeepsFive(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	dir := filepath.Join(filepath.Dir(h.dbPath), "backups")
	_ = os.MkdirAll(dir, 0o750)
	for i := 0; i < 7; i++ {
		p := filepath.Join(dir, "pre-v0.0.1-2026010"+string(rune('1'+i))+"T000000Z.db")
		_ = os.WriteFile(p, []byte("x"), 0o640)
		old := time.Now().Add(-time.Duration(10-i) * time.Hour) // mtimes are wall-clock
		_ = os.Chtimes(p, old, old)
	}
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a"); err != nil {
		t.Fatal(err)
	}
	entries, _ := filepath.Glob(filepath.Join(dir, "pre-*.db"))
	if len(entries) != 5 {
		t.Fatalf("kept %d backups", len(entries))
	}
	if _, err := os.Stat(filepath.Join(dir, "pre-v0.2.0-20261106T120000Z.db")); err != nil {
		t.Fatal("newest backup pruned")
	}
}

func TestHostRequirement(t *testing.T) {
	cases := []struct {
		name, current, hostFiles, target string
		wantReq                          string
		wantBlocked                      bool
		wantInstr                        int
	}{
		{"unknown host files", "v0.1.0", "", "v0.3.0", "v0.2.0", true, 1},
		{"host files behind", "v0.1.0", "v0.1.0", "v0.3.0", "v0.2.0", true, 1},
		{"host files equal", "v0.1.0", "v0.2.0", "v0.3.0", "v0.2.0", false, 1},
		{"host files ahead", "v0.1.0", "v0.5.0", "v0.3.0", "v0.2.0", false, 1},
		{"host change already behind current", "v0.2.0", "", "v0.3.0", "", false, 0},
		{"target before host change", "v0.1.0", "", "v0.1.5", "", false, 0},
		{"two host changes", "v0.1.0", "v0.2.0", "v0.4.0", "v0.4.0", true, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.current, func(o *Options) { o.HostFilesVersion = c.hostFiles })
			h.src.rels = []Release{rel("v0.1.5", ""), rel("v0.2.0", "add FOO"), rel("v0.3.0", ""), rel("v0.4.0", "add BAR")}
			_ = h.m.Check(context.Background())
			var got *ReleaseView
			v := h.view(t)
			for i := range v.Releases {
				if v.Releases[i].Version == c.target {
					got = &v.Releases[i]
				}
			}
			if got == nil {
				t.Fatalf("target %s not in view", c.target)
			}
			if got.RequiresHost != c.wantReq || got.Blocked != c.wantBlocked || len(got.HostInstructions) != c.wantInstr || got.HostInstructions == nil {
				t.Fatalf("got req=%q blocked=%v instr=%v", got.RequiresHost, got.Blocked, got.HostInstructions)
			}
			if c.wantInstr == 2 && (got.HostInstructions[0].Version != "v0.2.0" || got.HostInstructions[1].Version != "v0.4.0") {
				t.Fatalf("order %v", got.HostInstructions)
			}
		})
	}
}

func TestApplyBlockedByHost(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "add FOO")}
	_ = h.m.Check(context.Background())
	_, err := h.m.Apply(context.Background(), "v0.2.0", "a")
	var hu *HostUpdateError
	if !errors.Is(err, ErrHostUpdateRequired) || !errors.As(err, &hu) || hu.RequiresHost != "v0.2.0" {
		t.Fatalf("err = %v", err)
	}
}

func writeStatusFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "apply-status"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func applyingHarness(t *testing.T) harness {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	if _, err := h.m.Apply(context.Background(), "v0.2.0", "a"); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(h.applyDir, "apply-request")) // host consumed it
	return h
}

func TestReconcileStatusOK(t *testing.T) {
	h := applyingHarness(t)
	writeStatusFile(t, h.applyDir, `{"version":"v0.2.0","previous":"v0.1.0","ok":true,"error":"","finished_at":"2026-11-06T12:03:10Z"}`)
	h.m.Reconcile(false)
	r := h.view(t).Releases[0]
	if r.Status != StatusApplied || r.FinishedAt == nil || r.FinishedAt.Format(time.RFC3339) != "2026-11-06T12:03:10Z" {
		t.Fatalf("row = %+v", r)
	}
	if _, err := os.Stat(filepath.Join(h.applyDir, "apply-status")); !os.IsNotExist(err) {
		t.Fatal("status file not consumed")
	}
}

func TestReconcileStatusFailed(t *testing.T) {
	h := applyingHarness(t)
	writeStatusFile(t, h.applyDir, `{"version":"v0.2.0","previous":"v0.1.0","ok":false,"error":"health check timed out; rolled back to v0.1.0","finished_at":"2026-11-06T12:03:10Z"}`)
	h.m.Reconcile(false)
	r := h.view(t).Releases[0]
	if r.Status != StatusFailed || !strings.Contains(r.Error, "rolled back") || h.view(t).InFlight != "" {
		t.Fatalf("row = %+v", r)
	}
}

func TestReconcileStatusWithMismatchedVersionReleasesLock(t *testing.T) {
	h := applyingHarness(t)
	writeStatusFile(t, h.applyDir, `{"version":"","previous":"v0.1.0","ok":false,"error":"invalid version in request: ''","finished_at":"2026-11-06T12:03:10Z"}`)
	h.m.Reconcile(false)
	if v := h.view(t); v.InFlight != "" || v.Releases[0].Status != StatusFailed {
		t.Fatalf("view = %+v", v)
	}
}

func TestReconcileMalformedStatusIsKept(t *testing.T) {
	h := applyingHarness(t)
	writeStatusFile(t, h.applyDir, `{not json`)
	h.m.Reconcile(false)
	if _, err := os.Stat(filepath.Join(h.applyDir, "apply-status")); err != nil {
		t.Fatal("malformed status removed")
	}
	if h.view(t).InFlight != "v0.2.0" {
		t.Fatal("row changed")
	}
}

func TestReconcileStartupInterrupted(t *testing.T) {
	h := applyingHarness(t)
	h.m.Reconcile(false)
	if h.view(t).InFlight != "v0.2.0" {
		t.Fatal("periodic reconcile must not mark interrupted")
	}
	h.m.Reconcile(true)
	r := h.view(t).Releases[0]
	if r.Status != StatusFailed || r.Error != "interrupted" {
		t.Fatalf("row = %+v", r)
	}
}

func TestReconcileStartupKeepsPendingRequest(t *testing.T) {
	h := newHarness(t, "v0.1.0", nil)
	h.src.rels = []Release{rel("v0.2.0", "")}
	_ = h.m.Check(context.Background())
	_, _ = h.m.Apply(context.Background(), "v0.2.0", "a") // request still on disk
	h.m.Reconcile(true)
	if h.view(t).InFlight != "v0.2.0" {
		t.Fatal("pending request marked interrupted")
	}
}

func TestValidateApplyDir(t *testing.T) {
	dir := t.TempDir()
	if err := ValidateApplyDir(dir); err != nil {
		t.Fatalf("dir: %v", err)
	}
	if err := ValidateApplyDir(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing: want error")
	}
	f := filepath.Join(dir, "file")
	_ = os.WriteFile(f, nil, 0o644)
	if err := ValidateApplyDir(f); err == nil {
		t.Fatal("file: want error")
	}
}
