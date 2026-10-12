package obs

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// obsMetrics counts OBS requests by operation label, into a ring of
// per-second buckets for the trailing-minute request rate.
type obsMetrics struct {
	mu     sync.Mutex
	counts map[string]int64
	now    func() time.Time

	ringSec  [60]int64 // unix second each bucket currently holds
	ringHits [60]int64 // request count observed within that second
}

func (m *obsMetrics) inc(op string) {
	m.mu.Lock()
	m.counts[op]++
	sec := m.now().Unix()
	i := sec % 60
	if m.ringSec[i] != sec {
		m.ringSec[i] = sec
		m.ringHits[i] = 0
	}
	m.ringHits[i]++
	m.mu.Unlock()
}

// ratePerSecond returns requests/second over the trailing 60 seconds.
func (m *obsMetrics) ratePerSecond() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := m.now().Unix() - 60
	var total int64
	for i := range m.ringSec {
		if m.ringSec[i] > cutoff {
			total += m.ringHits[i]
		}
	}
	return float64(total) / 60
}

func (m *obsMetrics) snapshot() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.counts))
	for k, v := range m.counts {
		out[k] = v
	}
	return out
}

// Client is an authenticated OBS HTTP client.
type Client struct {
	base     string
	username string
	password string
	http     *http.Client
	metrics  *obsMetrics
	limiter  *minuteLimiter

	pubMu    sync.Mutex
	pubCache map[string]PublishFlags
}

func NewClient(base, username, password string) *Client {
	return &Client{
		base:     strings.TrimRight(base, "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: 30 * time.Second},
		metrics:  &obsMetrics{counts: make(map[string]int64), now: time.Now},
		limiter:  newMinuteLimiter(0),
		pubCache: make(map[string]PublishFlags),
	}
}

// SetMinuteBudget enables the background request limiter: at most n requests
// per clock minute; further background requests block until the next window.
// Interactive-tagged contexts (see Interactive) bypass the limiter.
// n <= 0 disables limiting. Not safe to call concurrently with requests —
// wire it at startup.
func (c *Client) SetMinuteBudget(n int) {
	c.limiter = newMinuteLimiter(n)
}

// MetricsSnapshot returns a copy of the per-operation OBS request counts.
func (c *Client) MetricsSnapshot() map[string]int64 {
	return c.metrics.snapshot()
}

// LimiterStats reports the background rate limiter's absolute gauges.
// The zero value (Enabled: false) means rate limiting is disabled.
type LimiterStats struct {
	Enabled   bool
	Budget    int
	Remaining int64
	Waits     int64
}

// LimiterStats returns the current limiter gauges.
func (c *Client) LimiterStats() LimiterStats {
	if c.limiter.budget <= 0 {
		return LimiterStats{}
	}
	waits, remaining := c.limiter.stats()
	return LimiterStats{Enabled: true, Budget: c.limiter.budget, Remaining: remaining, Waits: waits}
}

// RatePerSecond returns OBS requests per second over the trailing minute,
// counting both background and interactive requests.
func (c *Client) RatePerSecond() float64 {
	return c.metrics.ratePerSecond()
}

func (c *Client) get(ctx context.Context, op, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/xml")
	if !IsInteractive(ctx) {
		if err := c.limiter.acquire(ctx); err != nil {
			return nil, err
		}
	}
	c.metrics.inc(op)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, &StatusError{Path: path, Code: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(body))}
	}
	return resp, nil
}

// getFile fetches a binary artifact from OBS without setting an Accept header,
// so OBS serves the raw file content (e.g. JSON containerinfo files).
func (c *Client) getFile(ctx context.Context, op, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.username, c.password)
	if !IsInteractive(ctx) {
		if err := c.limiter.acquire(ctx); err != nil {
			return nil, err
		}
	}
	c.metrics.inc(op)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, &StatusError{Path: path, Code: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(body))}
	}
	return resp, nil
}

// post issues an authenticated POST request to path with no request body.
// Returns nil on 2xx; returns an error with up to 512 bytes of the response body on non-2xx.
func (c *Client) post(ctx context.Context, op, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	if !IsInteractive(ctx) {
		if err := c.limiter.acquire(ctx); err != nil {
			return err
		}
	}
	c.metrics.inc(op)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &StatusError{Path: path, Code: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(body))}
	}
	return nil
}

// Rebuild triggers a rebuild of a specific package target on OBS.
// project is path-escaped; repo, arch, and pkg are query-escaped.
func (c *Client) Rebuild(ctx context.Context, project, repo, arch, pkg string) error {
	path := fmt.Sprintf("/build/%s?cmd=rebuild&repository=%s&arch=%s&package=%s",
		url.PathEscape(project),
		url.QueryEscape(repo),
		url.QueryEscape(arch),
		url.QueryEscape(pkg),
	)
	return c.post(ctx, "rebuild", path)
}

// PublishFlags answers whether a repository publishes, resolved from a project's
// _meta <publish> block. Zero value = everything publishes (safe default).
// A Fleet-built composite delegates each repo to its owning instance's flags.
type PublishFlags struct {
	hasDefault     bool
	defaultPublish bool
	perRepo        map[string]bool

	delegate map[string]PublishFlags // slug → that instance's flags
	owner    func(repo string) string
}

// Publishes reports whether repo publishes for this project.
func (f PublishFlags) Publishes(repo string) bool {
	if f.owner != nil {
		if d, ok := f.delegate[f.owner(repo)]; ok {
			return d.Publishes(repo)
		}
	}
	if f.perRepo != nil {
		if v, ok := f.perRepo[repo]; ok {
			return v
		}
	}
	if !f.hasDefault {
		return true
	}
	return f.defaultPublish
}

type publishMetaXML struct {
	Publish *struct {
		Disable []struct {
			Repository string `xml:"repository,attr"`
		} `xml:"disable"`
		Enable []struct {
			Repository string `xml:"repository,attr"`
		} `xml:"enable"`
	} `xml:"publish"`
}

// parsePublishFlags resolves publish rules from a project _meta document.
//
// Per-repo rules override the project-level default. Among conflicting
// same-repo rules, the enable/disable applied is last-of-its-type processed
// (not document order); a self-contradictory _meta with the same repo both
// enabled and disabled is not resolved by document order, but real projects
// don't emit such contradictory rules. Arch-scoped disable/enable attributes
// are not supported; resolution is per-repo only.
func parsePublishFlags(metaXML []byte) (PublishFlags, error) {
	var m publishMetaXML
	if err := xml.Unmarshal(metaXML, &m); err != nil {
		return PublishFlags{}, err
	}
	f := PublishFlags{defaultPublish: true, hasDefault: true, perRepo: map[string]bool{}}
	if m.Publish == nil {
		return f, nil
	}
	for _, d := range m.Publish.Disable {
		if d.Repository == "" {
			f.defaultPublish = false
		} else {
			f.perRepo[d.Repository] = false
		}
	}
	for _, e := range m.Publish.Enable {
		if e.Repository == "" {
			f.defaultPublish = true
		} else {
			f.perRepo[e.Repository] = true
		}
	}
	return f, nil
}

// ProjectPublishFlags returns the (cached) publish flags for a project. The cache
// never expires — publish config is immutable for the repo lifetime — and is
// cleared explicitly via EvictPublishFlags when a project is removed.
func (c *Client) ProjectPublishFlags(ctx context.Context, project string) (PublishFlags, error) {
	c.pubMu.Lock()
	if f, ok := c.pubCache[project]; ok {
		c.pubMu.Unlock()
		return f, nil
	}
	c.pubMu.Unlock()

	resp, err := c.get(ctx, "publish_flags", "/source/"+url.PathEscape(project)+"/_meta")
	if err != nil {
		return PublishFlags{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return PublishFlags{}, err
	}
	f, err := parsePublishFlags(body)
	if err != nil {
		return PublishFlags{}, err
	}

	c.pubMu.Lock()
	c.pubCache[project] = f
	c.pubMu.Unlock()
	return f, nil
}

// EvictPublishFlags removes a project's cached publish flags (call on project removal).
func (c *Client) EvictPublishFlags(project string) {
	c.pubMu.Lock()
	delete(c.pubCache, project)
	c.pubMu.Unlock()
}

// --- XML response types ---

type directoryListing struct {
	Entries []struct {
		Name string `xml:"name,attr"`
	} `xml:"entry"`
}

type resultList struct {
	Results []buildResult `xml:"result"`
}

type buildResult struct {
	Project    string        `xml:"project,attr"`
	Repository string        `xml:"repository,attr"`
	Arch       string        `xml:"arch,attr"`
	State      string        `xml:"state,attr"`
	Statuses   []buildStatus `xml:"status"`
}

type buildStatus struct {
	Package string `xml:"package,attr"`
	Code    string `xml:"code,attr"`
	Versrel string `xml:"versrel,attr"`
	Details string `xml:"details"`
}

// HistoryEntry represents one entry from /_history.
type HistoryEntry struct {
	Revision int    `xml:"rev,attr"`
	Reason   string `xml:"reason"`
}

// DepInfo represents a package dependency from /_builddepinfo.
type DepInfo struct {
	Package string   `xml:"name,attr"`
	Deps    []string `xml:"pkgdep"`
	Error   string   `xml:"error"`
}

// SourceCommit represents one entry from /source/<project>/<pkg>/_history.
type SourceCommit struct {
	Rev     int    `xml:"rev,attr"`
	Comment string `xml:"comment"`
	Time    int64  `xml:"time"`
}

// PackageBuildState is a flattened build result.
type PackageBuildState struct {
	Project string
	Repo    string
	Arch    string
	Package string
	State   string
	Details string
	Versrel string // version-release string, e.g. "17.5-1"; empty if not available

	Instance string // slug of the OBS instance that reported it; set by Fleet
}

// BinaryArtifact is one binary entry from OBS _result?view=binarylist.
type BinaryArtifact struct {
	Project  string
	Repo     string
	Arch     string
	Package  string
	Filename string
	Size     int64
	MTime    int64
	BuiltAt  time.Time

	Instance string // slug of the OBS instance that reported it; set by Fleet
}

// BuildReasonResult represents the result of a build reason query.
type BuildReasonResult struct {
	Explain  string
	Packages []string
}

type buildReasonChangeXML struct {
	Key string `xml:"key,attr"`
}

type buildReasonXML struct {
	Explain string                 `xml:"explain"`
	Changes []buildReasonChangeXML `xml:"packagechange"`
}

// SearchProjects returns all OBS projects whose names start with the given prefix
// (exclusive of the prefix itself). Uses the OBS search API.
func (c *Client) SearchProjects(ctx context.Context, prefix string) ([]string, error) {
	// XPath: starts-with(@name,'prefix:') to catch all sub-namespaces
	path := "/search/project/id?match=starts-with(@name,'" + prefix + ":" + "')"
	resp, err := c.get(ctx, "search_projects", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var col struct {
		Projects []struct {
			Name string `xml:"name,attr"`
		} `xml:"project"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&col); err != nil {
		return nil, fmt.Errorf("parse search/project/id: %w", err)
	}

	names := make([]string, 0, len(col.Projects))
	for _, p := range col.Projects {
		names = append(names, p.Name)
	}
	return names, nil
}

// BuildResults fetches all package build states for a project, plus the
// repo-level state per "repo/arch" — the same publish state PublishStateTask
// reads ("published" once the publisher has synced that repo·arch). Both come
// from the single _result response.
func (c *Client) BuildResults(ctx context.Context, project string) ([]PackageBuildState, map[string]string, error) {
	resp, err := c.get(ctx, "build_results", "/build/"+project+"/_result")
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	var out []PackageBuildState
	repoStates := make(map[string]string, len(rl.Results))
	for _, r := range rl.Results {
		repoStates[r.Repository+"/"+r.Arch] = r.State
		for _, s := range r.Statuses {
			out = append(out, PackageBuildState{
				Project: project,
				Repo:    r.Repository,
				Arch:    r.Arch,
				Package: s.Package,
				State:   s.Code,
				Details: s.Details,
			})
		}
	}
	return out, repoStates, nil
}

// projectDir fetches a /build/… directory listing and returns the entry names.
func (c *Client) projectDir(ctx context.Context, op, path string) ([]string, error) {
	resp, err := c.get(ctx, op, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var dir directoryListing
	if err := xml.NewDecoder(resp.Body).Decode(&dir); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	names := make([]string, 0, len(dir.Entries))
	for _, e := range dir.Entries {
		if e.Name != "" {
			names = append(names, e.Name)
		}
	}
	return names, nil
}

// ProjectRepos returns the repository names configured for an OBS project.
// Calls GET /build/{project}/.
func (c *Client) ProjectRepos(ctx context.Context, project string) ([]string, error) {
	return c.projectDir(ctx, "project_repos", "/build/"+url.PathEscape(project)+"/")
}

// ProjectRepoArchs returns the architectures available for a repository.
// Calls GET /build/{project}/{repo}/.
func (c *Client) ProjectRepoArchs(ctx context.Context, project, repo string) ([]string, error) {
	return c.projectDir(ctx, "project_repo_archs", "/build/"+url.PathEscape(project)+"/"+url.PathEscape(repo)+"/")
}

// ProjectRepoPackages returns the package names built in a specific repo/arch.
// Calls GET /build/{project}/{repo}/{arch}/.
func (c *Client) ProjectRepoPackages(ctx context.Context, project, repo, arch string) ([]string, error) {
	return c.projectDir(ctx, "project_repo_packages", "/build/"+url.PathEscape(project)+"/"+
		url.PathEscape(repo)+"/"+url.PathEscape(arch)+"/")
}

// ProjectBuildResults fetches all build states for a project with version info
// (view=versrel). Use this for release projects where the version comes from
// OBS rather than from the DB.
func (c *Client) ProjectBuildResults(ctx context.Context, project string) ([]PackageBuildState, error) {
	path := fmt.Sprintf("/build/%s/_result?view=versrel", url.PathEscape(project))
	resp, err := c.get(ctx, "project_build_results", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	var out []PackageBuildState
	for _, r := range rl.Results {
		for _, s := range r.Statuses {
			out = append(out, PackageBuildState{
				Project: project,
				Repo:    r.Repository,
				Arch:    r.Arch,
				Package: s.Package,
				State:   s.Code,
				Details: s.Details,
				Versrel: s.Versrel,
			})
		}
	}
	return out, nil
}

// BuildLog returns the tail of a package build log.
func (c *Client) BuildLog(ctx context.Context, project, repo, arch, pkg string, tailBytes int) (string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s/_log?last=1&nostream=1", project, repo, arch, pkg)
	resp, err := c.get(ctx, "build_log", path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(tailBytes)))
	return string(body), err
}

// PackageHistory returns build history entries for a package target.
func (c *Client) PackageHistory(ctx context.Context, project, repo, arch, pkg string) ([]HistoryEntry, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s/_history", project, repo, arch, pkg)
	resp, err := c.get(ctx, "package_history", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var hist struct {
		Entries []HistoryEntry `xml:"entry"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&hist); err != nil {
		return nil, err
	}
	return hist.Entries, nil
}

// BuildDepInfo returns dependency info for a repo+arch.
func (c *Client) BuildDepInfo(ctx context.Context, project, repo, arch string) ([]DepInfo, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/_builddepinfo", project, repo, arch)
	resp, err := c.get(ctx, "build_dep_info", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Packages []DepInfo `xml:"package"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Packages, nil
}

// PackageBlockedReasons returns a map of "repo/arch" → blocked reason for all
// blocked targets of pkg in project, using the _result?view=status endpoint.
// Targets with no details or not in blocked state are omitted from the map.
func (c *Client) PackageBlockedReasons(ctx context.Context, project, pkg string) (map[string]string, error) {
	path := fmt.Sprintf("/build/%s/_result?package=%s&view=status",
		url.PathEscape(project), url.QueryEscape(pkg))
	resp, err := c.get(ctx, "blocked_reasons", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	reasons := make(map[string]string)
	for _, r := range rl.Results {
		for _, s := range r.Statuses {
			if s.Code == "blocked" && s.Details != "" {
				reasons[r.Repository+"/"+r.Arch] = s.Details
			}
		}
	}
	return reasons, nil
}

// SourceHistory returns commit history for a source package.
func (c *Client) SourceHistory(ctx context.Context, project, pkg string) ([]SourceCommit, error) {
	path := fmt.Sprintf("/source/%s/%s/_history", project, pkg)
	resp, err := c.get(ctx, "source_history", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var hist struct {
		Revisions []SourceCommit `xml:"revision"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&hist); err != nil {
		return nil, err
	}
	return hist.Revisions, nil
}

// PackageBuildResults fetches build states for a specific package across all targets.
func (c *Client) PackageBuildResults(ctx context.Context, project, pkg string) ([]PackageBuildState, error) {
	path := fmt.Sprintf("/build/%s/_result?package=%s&view=status",
		url.PathEscape(project), url.QueryEscape(pkg))
	resp, err := c.get(ctx, "package_build_results", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	var out []PackageBuildState
	for _, r := range rl.Results {
		for _, s := range r.Statuses {
			out = append(out, PackageBuildState{
				Project: project,
				Repo:    r.Repository,
				Arch:    r.Arch,
				Package: s.Package,
				State:   s.Code,
				Details: s.Details,
			})
		}
	}
	return out, nil
}

// RepoPublishStates returns a map of "repo/arch" → publish state by reading
// the r.State attribute from the OBS _result?package=…&view=status XML.
func (c *Client) RepoPublishStates(ctx context.Context, project, pkg string) (map[string]string, error) {
	path := fmt.Sprintf("/build/%s/_result?package=%s&view=status",
		url.PathEscape(project), url.QueryEscape(pkg))
	resp, err := c.get(ctx, "publish_states", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result: %w", project, err)
	}

	states := make(map[string]string, len(rl.Results))
	for _, r := range rl.Results {
		states[r.Repository+"/"+r.Arch] = r.State
	}
	return states, nil
}

// PackageBuildReason fetches the build reason for a specific package target.
func (c *Client) PackageBuildReason(ctx context.Context, project, repo, arch, pkg string) (BuildReasonResult, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s/_reason",
		url.PathEscape(project), url.PathEscape(repo),
		url.PathEscape(arch), url.PathEscape(pkg))
	resp, err := c.get(ctx, "build_reason", path)
	if err != nil {
		return BuildReasonResult{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return BuildReasonResult{}, err
	}

	var raw buildReasonXML
	if err := xml.Unmarshal(body, &raw); err != nil {
		return BuildReasonResult{}, fmt.Errorf("parse build reason: %w", err)
	}

	result := BuildReasonResult{Explain: raw.Explain}
	for _, ch := range raw.Changes {
		if ch.Key != "" {
			result.Packages = append(result.Packages, ch.Key)
		}
	}
	return result, nil
}

// PackageIsContainer returns true if the package's source contains a Dockerfile
// or a .kiwi file, indicating it produces a container image.
// Uses /source/{project}/{package}?view=info which returns a <sourceinfo>
// element listing all source filenames.
func (c *Client) PackageIsContainer(ctx context.Context, project, pkg string) (bool, error) {
	path := fmt.Sprintf("/source/%s/%s?view=info",
		url.PathEscape(project), url.PathEscape(pkg))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return false, err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/xml")
	c.metrics.inc("is_container")
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return false, fmt.Errorf("OBS %s: %s — %s", path, resp.Status, strings.TrimSpace(string(body)))
	}

	var info struct {
		Filenames []string `xml:"filename"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&info); err != nil {
		return false, fmt.Errorf("parse source info: %w", err)
	}
	for _, fn := range info.Filenames {
		if fn == "Dockerfile" || strings.HasSuffix(fn, ".kiwi") {
			return true, nil
		}
	}
	return false, nil
}

// PackageVersionResult returns the versrel string (e.g. "17.5-1") from the first
// successfully built target, or "" if the package has not been built yet.
func (c *Client) PackageVersionResult(ctx context.Context, project, pkg string) (string, error) {
	path := fmt.Sprintf("/build/%s/_result?view=versrel&package=%s",
		url.PathEscape(project), url.QueryEscape(pkg))
	resp, err := c.get(ctx, "version", path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var rl resultList
	if err := xml.NewDecoder(resp.Body).Decode(&rl); err != nil {
		return "", fmt.Errorf("parse versrel result: %w", err)
	}
	for _, r := range rl.Results {
		for _, s := range r.Statuses {
			if s.Versrel != "" {
				return s.Versrel, nil
			}
		}
	}
	return "", nil
}

// PackageAggregateSource reads /source/<project>/<pkg>/_aggregate and returns
// the source project and package the aggregate pulls its binaries from. ok is
// false (with nil error) when the package has no _aggregate file, i.e. it is
// not an aggregate. When the aggregate lists no <package>, OBS aggregates the
// same package name from the source project; when it lists several, the one
// matching pkg wins, else the first. Only the first <aggregate> is used.
func (c *Client) PackageAggregateSource(ctx context.Context, project, pkg string) (srcProject, srcPkg string, ok bool, err error) {
	path := fmt.Sprintf("/source/%s/%s/_aggregate", url.PathEscape(project), url.PathEscape(pkg))
	resp, err := c.get(ctx, "aggregate_source", path)
	if err != nil {
		if IsNotFound(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	defer resp.Body.Close()

	var list struct {
		Aggregates []struct {
			Project  string   `xml:"project,attr"`
			Packages []string `xml:"package"`
		} `xml:"aggregate"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", "", false, fmt.Errorf("parse _aggregate for %s/%s: %w", project, pkg, err)
	}
	if len(list.Aggregates) == 0 || list.Aggregates[0].Project == "" {
		return "", "", false, nil
	}
	agg := list.Aggregates[0]
	srcPkg = pkg
	if len(agg.Packages) > 0 {
		srcPkg = agg.Packages[0]
		for _, name := range agg.Packages {
			if name == pkg {
				srcPkg = name
				break
			}
		}
	}
	return agg.Project, srcPkg, true, nil
}

// ProjectBinaryList returns every binary entry from _result?view=binarylist.
func (c *Client) ProjectBinaryList(ctx context.Context, project string) ([]BinaryArtifact, error) {
	path := fmt.Sprintf("/build/%s/_result?view=binarylist", url.PathEscape(project))
	resp, err := c.get(ctx, "binary_list", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw struct {
		Results []struct {
			Project    string `xml:"project,attr"`
			Repository string `xml:"repository,attr"`
			Arch       string `xml:"arch,attr"`
			BinaryList []struct {
				Package  string `xml:"package,attr"`
				Binaries []struct {
					Filename string `xml:"filename,attr"`
					Size     int64  `xml:"size,attr"`
					MTime    int64  `xml:"mtime,attr"`
				} `xml:"binary"`
			} `xml:"binarylist"`
		} `xml:"result"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse /build/%s/_result?view=binarylist: %w", project, err)
	}

	var out []BinaryArtifact
	for _, result := range raw.Results {
		resultProject := result.Project
		if resultProject == "" {
			resultProject = project
		}
		for _, list := range result.BinaryList {
			for _, binary := range list.Binaries {
				artifact := BinaryArtifact{
					Project:  resultProject,
					Repo:     result.Repository,
					Arch:     result.Arch,
					Package:  list.Package,
					Filename: binary.Filename,
					Size:     binary.Size,
					MTime:    binary.MTime,
				}
				if binary.MTime > 0 {
					artifact.BuiltAt = time.Unix(binary.MTime, 0).UTC()
				}
				out = append(out, artifact)
			}
		}
	}
	return out, nil
}

// IsDistributableBinary returns true for binary files that users can install:
// .rpm (excluding .src.rpm and debuginfo/debugsource) and .deb (excluding dbgsym/dbg).
func IsDistributableBinary(filename string) bool {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".src.rpm"):
		return false
	case strings.HasSuffix(lower, ".rpm"):
		return !strings.Contains(lower, "-debuginfo") && !strings.Contains(lower, "-debugsource")
	case strings.HasSuffix(lower, ".deb"):
		return !strings.Contains(lower, "-dbgsym_") && !strings.Contains(lower, "-dbg_")
	default:
		return false
	}
}

// PackageBinaries returns the distributable binary filenames for a package build target.
// Source packages, debug info/symbols, and non-package artifacts are excluded.
func (c *Client) PackageBinaries(ctx context.Context, project, repo, arch, pkg string) ([]string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s",
		url.PathEscape(project), url.PathEscape(repo),
		url.PathEscape(arch), url.PathEscape(pkg))
	resp, err := c.get(ctx, "package_binaries", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var listing struct {
		Binaries []struct {
			Filename string `xml:"filename,attr"`
		} `xml:"binary"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return nil, fmt.Errorf("parse binary list: %w", err)
	}

	var out []string
	for _, b := range listing.Binaries {
		if IsDistributableBinary(b.Filename) {
			out = append(out, b.Filename)
		}
	}
	return out, nil
}

// PackageContainerInfoFilename returns the filename of the .containerinfo binary
// artifact for the given package target, or "" if the build hasn't produced one yet.
func (c *Client) PackageContainerInfoFilename(ctx context.Context, project, repo, arch, pkg string) (string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s",
		url.PathEscape(project), url.PathEscape(repo),
		url.PathEscape(arch), url.PathEscape(pkg))
	resp, err := c.get(ctx, "container_info_filename", path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var listing struct {
		Binaries []struct {
			Filename string `xml:"filename,attr"`
		} `xml:"binary"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return "", fmt.Errorf("parse binary list: %w", err)
	}
	for _, b := range listing.Binaries {
		if strings.HasSuffix(b.Filename, ".containerinfo") {
			return b.Filename, nil
		}
	}
	return "", nil
}

// PackageContainerTags fetches a .containerinfo JSON file and returns all tags
// with the "imagename:" prefix stripped (everything up to and including the last
// ":"), e.g. ["18.4-1-1.7", "18.4-1"]. Returns nil if tags is empty.
func (c *Client) PackageContainerTags(ctx context.Context, project, repo, arch, pkg, filename string) ([]string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/%s/%s",
		url.PathEscape(project), url.PathEscape(repo),
		url.PathEscape(arch), url.PathEscape(pkg),
		url.PathEscape(filename))
	resp, err := c.getFile(ctx, "container_tags", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var info struct {
		Tags []string `json:"tags"`
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("parse containerinfo: %w", err)
	}
	if len(info.Tags) == 0 {
		return nil, nil
	}
	tags := make([]string, 0, len(info.Tags))
	for _, raw := range info.Tags {
		tag := raw
		if idx := strings.LastIndex(raw, ":"); idx >= 0 {
			tag = raw[idx+1:]
		}
		tags = append(tags, tag)
	}
	return tags, nil
}

// stripEpoch removes the "N:" epoch prefix from an EVR string if present.
// "2:16.4-2.3" → "16.4-2.3"; "3.5.30-2.1" → "3.5.30-2.1" (unchanged).
func stripEpoch(evr string) string {
	if i := strings.Index(evr, ":"); i >= 0 {
		return evr[i+1:]
	}
	return evr
}

// RepoBinaryVersions returns a map of filename → evr for all binaries in the
// given (project, repo, arch) target. Epoch prefixes are stripped from the evr
// values. Returns an empty map (not an error) if the list is empty.
func (c *Client) RepoBinaryVersions(ctx context.Context, project, repo, arch string) (map[string]string, error) {
	path := fmt.Sprintf("/build/%s/%s/%s/_repository?view=binaryversions&withevr=1",
		url.PathEscape(project), url.PathEscape(repo), url.PathEscape(arch))
	resp, err := c.get(ctx, "binary_versions", path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw struct {
		Binaries []struct {
			Name string `xml:"name,attr"`
			EVR  string `xml:"evr,attr"`
		} `xml:"binary"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse binaryversionlist for %s/%s/%s: %w", project, repo, arch, err)
	}

	out := make(map[string]string, len(raw.Binaries))
	for _, b := range raw.Binaries {
		if b.Name != "" && b.EVR != "" {
			out[b.Name] = stripEpoch(b.EVR)
		}
	}
	return out, nil
}
