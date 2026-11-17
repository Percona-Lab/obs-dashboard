package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

// logicalProject maps a raw logical project to the Overview row it belongs
// to: tier version roots (ppg:devel:<V>, ppg:staging:<V>) absorb their
// :containers:* subprojects; any other direct subproject (extras, tde, …)
// is its own row (absorbing its subtree) — matching the version selector's
// <V>:<sub> granularity, and the releases tree (ppg:releases:<V>) splits the
// same way. The common trees and each PR collapse to one row each. The legacy two-tier shape ppg:<V> maps onto the
// staging row — staging is the renamed continuation of ppg:<V>, so
// pre-migration duration/event rows still inside the stats windows merge
// into the staging rows instead of rendering ghost sections. Unknown shapes
// return "" (excluded).
func logicalProject(project string) string {
	rel := strings.Split(project, ":")
	switch rel[0] {
	case "PR":
		if len(rel) >= 2 {
			return "PR:" + rel[1]
		}
		return ""
	case "common":
		return "common"
	case "ppg":
		if len(rel) < 2 {
			return ""
		}
		switch rel[1] {
		case "common":
			return "ppg:common"
		case "releases", "devel", "staging":
			if len(rel) < 3 {
				return ""
			}
			return tierRow(rel[1], rel[2], rel[3:])
		default:
			// Legacy two-tier shape: ppg:<V>[:<sub>…] → the staging row.
			return tierRow("staging", rel[1], rel[2:])
		}
	}
	return ""
}

// meanHours returns the arithmetic mean of hours, or 0 for none.
func meanHours(hours []float64) float64 {
	if len(hours) == 0 {
		return 0
	}
	sum := 0.0
	for _, h := range hours {
		sum += h
	}
	return sum / float64(len(hours))
}

// tierRow builds the Overview row name for a tier version root and its
// subproject tail: containers are absorbed into the version row; any other
// direct subproject gets its own row.
func tierRow(tier, version string, sub []string) string {
	row := "ppg:" + tier + ":" + version
	if len(sub) > 0 && sub[0] != "containers" {
		row += ":" + sub[0]
	}
	return row
}

// ── snapshot types (snake_case JSON, app convention) ──

type OverviewCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type OverviewImage struct {
	Project        string  `json:"project"` // raw OBS project (logical rows can aggregate several)
	Name           string  `json:"name"`
	Repo           string  `json:"repo"`
	BaseOS         string  `json:"base_os"`
	Critical       int     `json:"critical"`
	High           int     `json:"high"`
	OldestOpenDays int     `json:"oldest_open_days"` // 0 = none open / unknown
	AvgFixHours    float64 `json:"avg_fix_hours"`    // mean of closed episodes; 0 = none yet
}

// OverviewFixStat is the mean time from first vulnerable scan to clean scan
// over every closed CVE episode of a group of images.
type OverviewFixStat struct {
	AvgHours float64 `json:"avg_hours"` // 0 when Episodes == 0
	Episodes int     `json:"episodes"`
}

// OverviewFixTime splits CVE fix times by release stage. PR and devel
// projects are excluded: they are in development.
type OverviewFixTime struct {
	Released OverviewFixStat `json:"released"`
	Staging  OverviewFixStat `json:"staging"`
}

type OverviewProject struct {
	Project    string          `json:"project"`
	Rebuilds   int             `json:"rebuilds"`
	TopPackage *OverviewCount  `json:"top_package,omitempty"`
	Images     []OverviewImage `json:"images"`
}

type OverviewSnapshot struct {
	Window                     string             `json:"window"`
	GeneratedAt                string             `json:"generated_at"`
	PreviousWindowRebuildTotal int                `json:"previous_window_rebuild_total"`
	TopRepo                    *OverviewCount     `json:"top_repo,omitempty"`
	Projects                   []OverviewProject  `json:"projects"`
	ByInstance                 []OverviewInstance `json:"by_instance"`
	FixTime                    OverviewFixTime    `json:"fix_time"`
}

// OverviewInstance is one instance's live target tally.
type OverviewInstance struct {
	Instance string `json:"instance"`
	OK       int    `json:"ok"`
	Failing  int    `json:"failing"`
	Building int    `json:"building"`
	Blocked  int    `json:"blocked"`
}

var overviewWindows = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"48h": 48 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

// buildOverviewSnapshot assembles the snapshot from raw store rows.
// Aggregation rules: rebuilds/top_package per logical project, top_repo global,
// per-image CVE counts as max across archs, oldest_open_days from the oldest
// non-nil CveSince among vulnerable archs, avg_fix_hours as the mean of the
// image's closed episodes, and fix_time as the mean over every closed episode
// of released and staging images (PR and devel excluded).
func buildOverviewSnapshot(window string, now time.Time,
	cur, prev []store.BuildCompletion, scans []store.OverviewCveScan, periods []store.OverviewCvePeriod,
) OverviewSnapshot {
	type projAgg struct {
		rebuilds int
		pkgCount map[string]int
		images   map[string]*OverviewImage
	}
	agg := map[string]*projAgg{}
	getAgg := func(logical string) *projAgg {
		a, ok := agg[logical]
		if !ok {
			a = &projAgg{pkgCount: map[string]int{}, images: map[string]*OverviewImage{}}
			agg[logical] = a
		}
		return a
	}

	repoCount := map[string]int{}
	for _, e := range cur {
		logical := logicalProject(e.Project)
		if logical == "" {
			continue
		}
		a := getAgg(logical)
		a.rebuilds++
		a.pkgCount[e.Package]++
		repoCount[e.Repo]++
	}

	prevTotal := 0
	for _, e := range prev {
		if logicalProject(e.Project) != "" {
			prevTotal++
		}
	}

	type imgKey struct{ project, pkg, repo string }
	imgSince := map[imgKey]*time.Time{}
	imgAt := map[imgKey]*OverviewImage{}
	imgLogical := map[imgKey]string{}
	for _, s := range scans {
		logical := logicalProject(s.Project)
		if logical == "" {
			continue
		}
		k := imgKey{s.Project, s.Package, s.Repo}
		img, ok := imgAt[k]
		if !ok {
			img = &OverviewImage{Project: s.Project, Name: s.Package, Repo: s.Repo, BaseOS: deriveBaseOS(s.Project, s.Repo)}
			imgAt[k] = img
			imgLogical[k] = logical
		}
		if s.Critical > img.Critical {
			img.Critical = s.Critical
		}
		if s.High > img.High {
			img.High = s.High
		}
		if (s.Critical > 0 || s.High > 0) && s.CveSince != nil {
			if cur := imgSince[k]; cur == nil || s.CveSince.Before(*cur) {
				imgSince[k] = s.CveSince
			}
		}
	}
	for k, since := range imgSince {
		imgAt[k].OldestOpenDays = int(now.Sub(*since).Hours() / 24)
	}

	fixHours := map[imgKey][]float64{}
	var released, staging []float64
	for _, p := range periods {
		hours := p.CleanSince.Sub(p.CveSince).Hours()
		k := imgKey{p.Project, p.Package, p.Repo}
		fixHours[k] = append(fixHours[k], hours)
		switch logical := logicalProject(p.Project); {
		case strings.HasPrefix(logical, "ppg:releases:"):
			released = append(released, hours)
		case strings.HasPrefix(logical, "ppg:staging:"):
			staging = append(staging, hours)
		}
	}
	for k, hours := range fixHours {
		img, ok := imgAt[k]
		if !ok {
			continue // period for an image with no current scan row
		}
		img.AvgFixHours = meanHours(hours)
	}

	for k, img := range imgAt {
		getAgg(imgLogical[k]).images[k.project+"/"+k.pkg+"/"+k.repo] = img
	}

	projects := []OverviewProject{}
	for logical, a := range agg {
		if a.rebuilds == 0 && len(a.images) == 0 {
			continue
		}
		p := OverviewProject{Project: logical, Rebuilds: a.rebuilds, Images: []OverviewImage{}}
		var topPkg string
		for name, n := range a.pkgCount {
			if topPkg == "" || n > a.pkgCount[topPkg] || (n == a.pkgCount[topPkg] && name < topPkg) {
				topPkg = name
			}
		}
		if topPkg != "" {
			p.TopPackage = &OverviewCount{Name: topPkg, Count: a.pkgCount[topPkg]}
		}
		var names []string
		for k := range a.images {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			p.Images = append(p.Images, *a.images[k])
		}
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Rebuilds != projects[j].Rebuilds {
			return projects[i].Rebuilds > projects[j].Rebuilds
		}
		return projects[i].Project < projects[j].Project
	})

	snap := OverviewSnapshot{
		Window:                     window,
		GeneratedAt:                now.Format(time.RFC3339),
		PreviousWindowRebuildTotal: prevTotal,
		Projects:                   projects,
		FixTime: OverviewFixTime{
			Released: OverviewFixStat{AvgHours: meanHours(released), Episodes: len(released)},
			Staging:  OverviewFixStat{AvgHours: meanHours(staging), Episodes: len(staging)},
		},
	}
	var topRepo string
	for name, n := range repoCount {
		if topRepo == "" || n > repoCount[topRepo] || (n == repoCount[topRepo] && name < topRepo) {
			topRepo = name
		}
	}
	if topRepo != "" {
		snap.TopRepo = &OverviewCount{Name: topRepo, Count: repoCount[topRepo]}
	}
	return snap
}

// ── handler ──

// mergeInstanceCounts folds unstamped (pre-migration) targets into the
// default instance and returns rows sorted by slug.
func mergeInstanceCounts(counts []store.InstanceTargetCounts, defaultSlug string) []OverviewInstance {
	by := map[string]*OverviewInstance{}
	for _, c := range counts {
		slug := c.Instance
		if slug == "" {
			slug = defaultSlug
		}
		o := by[slug]
		if o == nil {
			o = &OverviewInstance{Instance: slug}
			by[slug] = o
		}
		o.OK += c.OK
		o.Failing += c.Failing
		o.Building += c.Building
		o.Blocked += c.Blocked
	}
	out := make([]OverviewInstance, 0, len(by))
	for _, o := range by {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

// overviewHandler serves GET /api/overview?window=24h|48h|7d.
func overviewHandler(db *sql.DB, defaultSlug string, cache *ttlCache[OverviewSnapshot]) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		window := r.URL.Query().Get("window")
		if window == "" {
			window = "24h"
		}
		dur, ok := overviewWindows[window]
		if !ok {
			http.Error(w, "invalid window (24h|48h|7d)", http.StatusBadRequest)
			return
		}
		snap, err := cache.Get(r.Context(), window, func(ctx context.Context) (OverviewSnapshot, error) {
			now := time.Now().UTC()
			cur, err := store.QueryBuildCompletions(db, now.Add(-dur), now)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			prev, err := store.QueryBuildCompletions(db, now.Add(-2*dur), now.Add(-dur))
			if err != nil {
				return OverviewSnapshot{}, err
			}
			scans, err := store.QueryAllCveScans(db)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			periods, err := store.QueryAllCvePeriods(db)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			snap := buildOverviewSnapshot(window, now, cur, prev, scans, periods)
			counts, err := store.QueryTargetCountsByInstance(db)
			if err != nil {
				return OverviewSnapshot{}, err
			}
			snap.ByInstance = mergeInstanceCounts(counts, defaultSlug)
			return snap, nil
		})
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	}
}
