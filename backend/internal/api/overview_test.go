package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

func TestLogicalProject(t *testing.T) {
	cases := []struct{ project, want string }{
		// three-tier shapes
		{"ppg:staging:17", "ppg:staging:17"},
		{"ppg:staging:17:containers:ubi9", "ppg:staging:17"},
		{"ppg:staging:16:extras", "ppg:staging:16:extras"},
		{"ppg:staging:16:extras:containers:ubi9", "ppg:staging:16:extras"},
		{"ppg:staging:16:tde", "ppg:staging:16:tde"},
		{"ppg:devel:18", "ppg:devel:18"},
		{"ppg:devel:18:containers:ubi9", "ppg:devel:18"},
		{"ppg:devel", ""},
		{"ppg:staging", ""},
		// legacy two-tier shapes map onto staging (renamed continuation):
		// pre-migration duration/event rows inside the stats windows merge
		// into the staging rows instead of rendering ghost sections.
		{"ppg:17", "ppg:staging:17"},
		{"ppg:17:containers:ubi9", "ppg:staging:17"},
		{"ppg:16:extras", "ppg:staging:16:extras"},
		// unchanged shapes
		{"ppg:common", "ppg:common"},
		{"ppg:common:deps", "ppg:common"},
		{"common:containers:ubi8", "common"},
		// releases split per version like the tiers: containers absorbed,
		// other subprojects get their own row, the bare root is excluded.
		{"ppg:releases:17", "ppg:releases:17"},
		{"ppg:releases:17:containers:ubi9", "ppg:releases:17"},
		{"ppg:releases:18:tarballs:ssl3", "ppg:releases:18:tarballs"},
		{"ppg:releases", ""},
		{"PR:pr-124:ppg:staging:16:extras", "PR:pr-124"},
		{"PR:pr-33:ppg:18:containers:ubi9", "PR:pr-33"},
		{"isv:other:ppg:17", ""},
		{"ppg", ""},
	}
	for _, c := range cases {
		if got := logicalProject(c.project); got != c.want {
			t.Errorf("logicalProject(%q) = %q, want %q", c.project, got, c.want)
		}
	}
}

func TestOverviewSnapshotBuilder(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return now.Add(time.Duration(-n) * 24 * time.Hour) }

	cur := []store.BuildCompletion{
		{Project: "ppg:17", Package: "pkg-a", Repo: "UBI_9"},
		{Project: "ppg:17:containers:ubi9", Package: "img-x", Repo: "images"},
		{Project: "ppg:17", Package: "pkg-a", Repo: "Debian_12"},
		{Project: "PR:pr-9:ppg:18", Package: "pkg-b", Repo: "UBI_9"},
	}
	prev := []store.BuildCompletion{{Project: "ppg:17", Package: "pkg-a", Repo: "UBI_9"}}
	scans := []store.OverviewCveScan{
		{Project: "ppg:17:containers:ubi9", Package: "img-x", Arch: "x86_64", Critical: 2, High: 6, CveSince: ptrTime(day(34))},
		{Project: "ppg:17:containers:ubi9", Package: "img-x", Arch: "aarch64", Critical: 1, High: 7, CveSince: ptrTime(day(10))},
		{Project: "ppg:releases:17:containers:ubi9", Package: "img-r", Arch: "x86_64", Critical: 0, High: 48, CveSince: nil},
		{Project: "common:containers:ubi9", Package: "img-clean", Arch: "x86_64", Critical: 0, High: 0, CveSince: nil},
	}
	periods := []store.OverviewCvePeriod{
		{Project: "ppg:17:containers:ubi9", Package: "img-x", CveSince: day(30), CleanSince: day(21)}, // 9d
		{Project: "ppg:17:containers:ubi9", Package: "img-x", CveSince: day(60), CleanSince: day(49)}, // 11d
	}

	s := buildOverviewSnapshot("24h", now, cur, prev, scans, periods)

	if s.PreviousWindowRebuildTotal != 1 {
		t.Fatalf("prev total = %d", s.PreviousWindowRebuildTotal)
	}
	if s.TopRepo == nil || s.TopRepo.Name != "UBI_9" || s.TopRepo.Count != 2 {
		t.Fatalf("top_repo = %+v", s.TopRepo)
	}
	p17 := findProject(t, s, "ppg:staging:17")
	if p17.Rebuilds != 3 || p17.TopPackage.Name != "pkg-a" || p17.TopPackage.Count != 2 {
		t.Fatalf("ppg:staging:17 = %+v", p17)
	}
	if len(p17.Images) != 1 || p17.Images[0].Critical != 2 || p17.Images[0].High != 7 {
		t.Fatalf("img-x max-across-archs failed: %+v", p17.Images)
	}
	if p17.Images[0].Project != "ppg:17:containers:ubi9" {
		t.Fatalf("img-x project = %+v", p17.Images[0])
	}
	if p17.Images[0].OldestOpenDays != 34 || p17.Images[0].AvgFixHours != 240 { // mean(9d,11d)=10d
		t.Fatalf("img-x ages = %+v", p17.Images[0])
	}
	rel := findProject(t, s, "ppg:releases:17")
	if rel.Rebuilds != 0 || rel.Images[0].OldestOpenDays != 0 || rel.Images[0].AvgFixHours != 0 {
		t.Fatalf("releases = %+v", rel)
	}
	findProject(t, s, "common")
	pr := findProject(t, s, "PR:pr-9")
	if pr.Rebuilds != 1 {
		t.Fatalf("pr = %+v", pr)
	}
	if s.Projects[0].Project != "ppg:staging:17" {
		t.Fatalf("sort order: %v", s.Projects[0].Project)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func findProject(t *testing.T, s OverviewSnapshot, name string) OverviewProject {
	t.Helper()
	for _, p := range s.Projects {
		if p.Project == name {
			return p
		}
	}
	t.Fatalf("project %s missing from snapshot: %+v", name, s.Projects)
	return OverviewProject{}
}

func TestMergeInstanceCounts(t *testing.T) {
	cases := []struct {
		name        string
		counts      []store.InstanceTargetCounts
		defaultSlug string
		want        []OverviewInstance
	}{
		{
			name:        "empty instance folds into default slug",
			counts:      []store.InstanceTargetCounts{{Instance: "", OK: 3, Failing: 1}},
			defaultSlug: "opensuse",
			want:        []OverviewInstance{{Instance: "opensuse", OK: 3, Failing: 1}},
		},
		{
			name: "empty instance sums into default slug that already has stamped counts",
			counts: []store.InstanceTargetCounts{
				{Instance: "", OK: 3, Failing: 1},
				{Instance: "opensuse", OK: 2, Building: 4},
			},
			defaultSlug: "opensuse",
			want:        []OverviewInstance{{Instance: "opensuse", OK: 5, Failing: 1, Building: 4}},
		},
		{
			name: "already-labeled instances pass through untouched",
			counts: []store.InstanceTargetCounts{
				{Instance: "percona", OK: 1, Blocked: 2},
			},
			defaultSlug: "opensuse",
			want:        []OverviewInstance{{Instance: "percona", OK: 1, Blocked: 2}},
		},
		{
			name: "output sorted by slug",
			counts: []store.InstanceTargetCounts{
				{Instance: "zzz", OK: 1},
				{Instance: "aaa", OK: 2},
				{Instance: "", Failing: 1},
			},
			defaultSlug: "mmm",
			want: []OverviewInstance{
				{Instance: "aaa", OK: 2},
				{Instance: "mmm", Failing: 1},
				{Instance: "zzz", OK: 1},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mergeInstanceCounts(c.counts, c.defaultSlug)
			if len(got) != len(c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %+v, want %+v", got, c.want)
				}
			}
		})
	}
}

func TestOverviewHandlerWindowValidation(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := overviewHandler(db, "opensuse", newTTLCache[OverviewSnapshot](time.Minute))

	for _, tc := range []struct {
		q    string
		code int
	}{{"", 200}, {"?window=24h", 200}, {"?window=48h", 200}, {"?window=7d", 200}, {"?window=1h", 400}} {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(http.MethodGet, "/api/overview"+tc.q, nil))
		if w.Code != tc.code {
			t.Fatalf("window %q → %d, want %d", tc.q, w.Code, tc.code)
		}
	}
}

func TestOverviewHandlerByInstance(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	pkgs := []*model.Package{
		{Project: "ppg:17", Name: "a", RollupState: model.RollupSucceeded, UpdatedAt: now, Targets: []model.Target{
			{Repo: "R", Arch: "x", State: "succeeded", Instance: "opensuse"},
			{Repo: "D", Arch: "y", State: "failed", Instance: "percona"},
		}},
		// Unstamped target: pre-migration data, must fold into the default slug.
		{Project: "ppg:18", Name: "b", RollupState: model.RollupSucceeded, UpdatedAt: now, Targets: []model.Target{
			{Repo: "R2", Arch: "x", State: "succeeded"},
		}},
	}
	for _, p := range pkgs {
		if err := store.UpsertPackageState(db, p, now); err != nil {
			t.Fatal(err)
		}
	}

	h := overviewHandler(db, "opensuse", newTTLCache[OverviewSnapshot](time.Minute))
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var s OverviewSnapshot
	if err := json.NewDecoder(w.Body).Decode(&s); err != nil {
		t.Fatal(err)
	}
	want := []OverviewInstance{
		{Instance: "opensuse", OK: 2},
		{Instance: "percona", Failing: 1},
	}
	if len(s.ByInstance) != len(want) {
		t.Fatalf("by_instance = %+v, want %+v", s.ByInstance, want)
	}
	for i := range want {
		if s.ByInstance[i] != want[i] {
			t.Fatalf("by_instance = %+v, want %+v", s.ByInstance, want)
		}
	}
}

func TestOverviewHandlerCaches(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := overviewHandler(db, "opensuse", newTTLCache[OverviewSnapshot](time.Minute))

	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	var s1 OverviewSnapshot
	if err := json.NewDecoder(w.Body).Decode(&s1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO target_state_durations (project, package, repo, arch, state, entered_at)
		VALUES ('ppg:17','p','r','x86_64','finished',?)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	var s2 OverviewSnapshot
	if err := json.NewDecoder(w.Body).Decode(&s2); err != nil {
		t.Fatal(err)
	}
	if len(s2.Projects) != len(s1.Projects) {
		t.Fatalf("second request bypassed the cache: %d vs %d projects", len(s2.Projects), len(s1.Projects))
	}
}

func TestOverviewTopTieBreakDeterministic(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	// Two packages and two repos, all at equal counts: the
	// lexicographically-smaller name must win regardless of map order.
	cur := []store.BuildCompletion{
		{Project: "ppg:17", Package: "pkg-b", Repo: "UBI_9"},
		{Project: "ppg:17", Package: "pkg-a", Repo: "Debian_12"},
	}

	for i := 0; i < 20; i++ {
		s := buildOverviewSnapshot("24h", now, cur, nil, nil, nil)
		if s.TopRepo == nil || s.TopRepo.Name != "Debian_12" || s.TopRepo.Count != 1 {
			t.Fatalf("top_repo tie-break = %+v, want Debian_12/1", s.TopRepo)
		}
		p := findProject(t, s, "ppg:staging:17")
		if p.TopPackage == nil || p.TopPackage.Name != "pkg-a" || p.TopPackage.Count != 1 {
			t.Fatalf("top_package tie-break = %+v, want pkg-a/1", p.TopPackage)
		}
	}
}

func TestOverviewEmptySnapshotProjectsNotNull(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	s := buildOverviewSnapshot("24h", now, nil, nil, nil, nil)
	if s.Projects == nil {
		t.Fatal("Projects is nil; must be an empty slice")
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"projects":[]`) {
		t.Fatalf("empty snapshot JSON = %s, want \"projects\":[]", b)
	}
}

func TestOverviewSnapshotSplitsByRepo(t *testing.T) {
	now := time.Now().UTC()
	day := func(n int) time.Time { return now.Add(time.Duration(-n) * 24 * time.Hour) }
	scans := []store.OverviewCveScan{
		{Project: "ppg:staging:17:containers", Package: "pdp", Repo: "ubi8", Arch: "x86_64", Critical: 0, High: 1, CveSince: nil},
		{Project: "ppg:staging:17:containers", Package: "pdp", Repo: "ubi9", Arch: "x86_64", Critical: 3, High: 2, CveSince: ptrTime(day(5))},
	}
	s := buildOverviewSnapshot("24h", now, nil, nil, scans, nil)

	var imgs []OverviewImage
	for _, pr := range s.Projects {
		for _, img := range pr.Images {
			if img.Name == "pdp" {
				imgs = append(imgs, img)
			}
		}
	}
	if len(imgs) != 2 {
		t.Fatalf("want 2 pdp images (ubi8 + ubi9), got %d: %+v", len(imgs), imgs)
	}
	byOS := map[string]OverviewImage{}
	for _, img := range imgs {
		byOS[img.BaseOS] = img
	}
	if byOS["UBI 8"].Repo != "ubi8" || byOS["UBI 8"].Critical != 0 || byOS["UBI 8"].High != 1 {
		t.Fatalf("ubi8 image wrong: %+v", byOS["UBI 8"])
	}
	if byOS["UBI 9"].Repo != "ubi9" || byOS["UBI 9"].Critical != 3 || byOS["UBI 9"].High != 2 {
		t.Fatalf("ubi9 image wrong: %+v", byOS["UBI 9"])
	}
	if byOS["UBI 9"].OldestOpenDays != 5 {
		t.Fatalf("ubi9 oldest_open_days = %d, want 5", byOS["UBI 9"].OldestOpenDays)
	}
}

func TestOverviewFixTimeSplitsReleasedAndStagingAndSkipsPRs(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return now.Add(-time.Duration(h) * time.Hour) }
	scans := []store.OverviewCveScan{
		{Project: "ppg:staging:17:containers", Package: "img-s", Repo: "ubi9", Arch: "x86_64"},
	}
	periods := []store.OverviewCvePeriod{
		{Project: "ppg:releases:17:containers", Package: "img-r", Repo: "ubi9", CveSince: at(100), CleanSince: at(52)}, // 48h
		{Project: "ppg:releases:18:containers", Package: "img-r", Repo: "ubi8", CveSince: at(30), CleanSince: at(6)},   // 24h
		{Project: "ppg:staging:17:containers", Package: "img-s", Repo: "ubi9", CveSince: at(10), CleanSince: at(4)},    // 6h
		{Project: "PR:pr-101:ppg:staging:17:containers", Package: "img-p", Repo: "ubi9", CveSince: at(8), CleanSince: at(7)},
		{Project: "ppg:devel:17:containers", Package: "img-d", Repo: "ubi9", CveSince: at(9), CleanSince: at(3)},
	}

	s := buildOverviewSnapshot("24h", now, nil, nil, scans, periods)

	if s.FixTime.Released.Episodes != 2 || s.FixTime.Released.AvgHours != 36 {
		t.Errorf("released = %+v, want 2 episodes averaging 36h", s.FixTime.Released)
	}
	if s.FixTime.Staging.Episodes != 1 || s.FixTime.Staging.AvgHours != 6 {
		t.Errorf("staging = %+v, want 1 episode of 6h (PR and devel excluded)", s.FixTime.Staging)
	}
	img := findProject(t, s, "ppg:staging:17").Images[0]
	if img.AvgFixHours != 6 {
		t.Errorf("per-image avg_fix_hours = %v, want 6 (sub-day fixes must not round to 0)", img.AvgFixHours)
	}
}

func TestOverviewFixTimeEmpty(t *testing.T) {
	s := buildOverviewSnapshot("24h", time.Now(), nil, nil, nil, nil)
	if s.FixTime.Released.Episodes != 0 || s.FixTime.Staging.Episodes != 0 {
		t.Errorf("fix_time = %+v, want zero episodes", s.FixTime)
	}
}
