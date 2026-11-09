package cve

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	hubpkg "github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
)

var obsToDockerPlatform = map[string]string{
	"x86_64":  "amd64",
	"aarch64": "arm64",
	"i586":    "386",
	"armv7l":  "arm/v7",
	"ppc64le": "ppc64le",
	"s390x":   "s390x",
}

// ScanRequest carries the parameters for a single container image CVE scan.
type ScanRequest struct {
	Project    string
	Package    string
	Tags       []string
	PrimaryTag string
	Targets    []model.Target
}

// ExecFn is the signature for running an external command and capturing output.
type ExecFn func(ctx context.Context, name string, args ...string) ([]byte, error)

// URLResolver builds registry and OBS URLs for a target's instance.
type URLResolver interface {
	ImageBase(instance, project, repo, name string) string
	PackageURL(instance, project, pkg string) string
}

// WithURLs sets the URL resolver (the Fleet in production).
func WithURLs(r URLResolver) Option { return func(s *Scanner) { s.urls = r } }

// Scanner queues and executes CVE scans via trivy.
type Scanner struct {
	queue     chan ScanRequest
	db        *sql.DB
	hub       *hubpkg.Hub
	workers   int
	execFn    ExecFn
	enqueueFn func(ScanRequest)
	urls      URLResolver
	// trivyMu serializes trivy invocations: trivy uses a shared FS cache that
	// cannot be accessed by more than one process at a time.
	trivyMu sync.Mutex

	// pending holds the keys of requests that are queued or running, so an
	// identical request (same package, same repos) is not queued twice.
	pendingMu sync.Mutex
	pending   map[string]bool
	// life bounds background enqueueing; set by Start.
	life context.Context
}

// Option is a functional option for Scanner.
type Option func(*Scanner)

// WithExecFn replaces the default os/exec runner (useful in tests).
func WithExecFn(fn ExecFn) Option { return func(s *Scanner) { s.execFn = fn } }

// WithEnqueueFn replaces the internal queue send (useful in tests).
func WithEnqueueFn(fn func(ScanRequest)) Option { return func(s *Scanner) { s.enqueueFn = fn } }

// NewScanner creates a Scanner with the given options.
func NewScanner(db *sql.DB, h *hubpkg.Hub, workers int, opts ...Option) *Scanner {
	s := &Scanner{
		queue:   make(chan ScanRequest, 100),
		db:      db,
		hub:     h,
		workers: workers,
		execFn:  defaultExec,
		urls:    obs.SingleFleet(nil, ""),
		pending: map[string]bool{},
		life:    context.Background(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func defaultExec(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Start launches worker goroutines. Call once; they run until ctx is cancelled.
func (s *Scanner) Start(ctx context.Context) {
	s.life = ctx
	for i := 0; i < s.workers; i++ {
		go s.run(ctx)
	}
}

// Enqueue adds a scan request to the queue. Non-blocking: drops and logs a
// warning if the queue is full. A request identical to one already queued or
// running is skipped.
func (s *Scanner) Enqueue(req ScanRequest) {
	if s.enqueueFn != nil {
		s.enqueueFn(req)
		return
	}
	if !s.reserve(req) {
		return
	}
	select {
	case s.queue <- req:
	default:
		s.release(req)
		slog.Warn("cve: scan queue full, dropping", "pkg", req.Project+"/"+req.Package)
	}
}

// requestKey identifies a request by package and the set of repos it scans.
func requestKey(req ScanRequest) string {
	repos := make([]string, 0, len(req.Targets))
	for _, t := range req.Targets {
		repos = append(repos, t.Repo)
	}
	sort.Strings(repos)
	return req.Project + "/" + req.Package + "|" + strings.Join(repos, ",")
}

// reserve marks req as pending; false if an identical request already is.
func (s *Scanner) reserve(req ScanRequest) bool {
	k := requestKey(req)
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if s.pending[k] {
		return false
	}
	s.pending[k] = true
	return true
}

func (s *Scanner) release(req ScanRequest) {
	s.pendingMu.Lock()
	delete(s.pending, requestKey(req))
	s.pendingMu.Unlock()
}

// RescanVulnerable queues a fresh scan of every image that currently carries
// HIGH or CRITICAL CVEs, limited to the vulnerable repos of each package.
// Packages that are no longer published containers are skipped, as are
// images whose identical rescan is already pending. It returns how many
// images were queued. Requests are fed to the queue in the background,
// waiting for room rather than dropping when the queue is full.
func (s *Scanner) RescanVulnerable() (int, error) {
	imgs, err := store.QueryVulnerableImages(s.db)
	if err != nil {
		return 0, err
	}
	repos := map[[2]string]map[string]bool{}
	var order [][2]string
	for _, img := range imgs {
		k := [2]string{img.Project, img.Package}
		if repos[k] == nil {
			repos[k] = map[string]bool{}
			order = append(order, k)
		}
		repos[k][img.Repo] = true
	}

	var reqs []ScanRequest
	images := 0
	for _, k := range order {
		pkg, err := store.GetPackage(s.db, k[0], k[1])
		if err != nil {
			return 0, err
		}
		if pkg == nil || pkg.RollupState != model.RollupPublished || pkg.IsContainer == nil || !*pkg.IsContainer {
			continue
		}
		req := packageToRequest(pkg)
		var targets []model.Target
		scanned := map[string]bool{}
		for _, t := range req.Targets {
			if repos[k][t.Repo] {
				targets = append(targets, t)
				scanned[t.Repo] = true
			}
		}
		if len(targets) == 0 || req.PrimaryTag == "" {
			continue
		}
		req.Targets = targets
		if s.enqueueFn == nil && !s.reserve(req) {
			continue
		}
		reqs = append(reqs, req)
		images += len(scanned)
	}

	go func() {
		for i, req := range reqs {
			if s.enqueueFn != nil {
				s.enqueueFn(req)
				continue
			}
			select {
			case s.queue <- req:
			case <-s.life.Done():
				for _, r := range reqs[i:] {
					s.release(r)
				}
				return
			}
		}
	}()
	slog.Info("cve: rescan of vulnerable images queued", "images", images, "packages", len(reqs))
	return images, nil
}

func (s *Scanner) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-s.queue:
			if !ok {
				return
			}
			s.scanPackage(ctx, req)
			s.release(req)
		}
	}
}

func (s *Scanner) scanPackage(ctx context.Context, req ScanRequest) {
	if req.PrimaryTag == "" || len(req.Targets) == 0 {
		slog.Warn("cve: skipping scan, no tag or no succeeded targets",
			"pkg", req.Project+"/"+req.Package,
			"primary_tag", req.PrimaryTag,
			"targets", len(req.Targets))
		return
	}
	for _, target := range req.Targets {
		if ctx.Err() != nil {
			return
		}
		platform, ok := obsToDockerPlatform[target.Arch]
		if !ok {
			slog.Warn("cve: unknown OBS arch, skipping", "arch", target.Arch)
			continue
		}

		imageRef := s.urls.ImageBase(target.Instance, req.Project, target.Repo, req.Package) + ":" + req.PrimaryTag
		obsURL := s.urls.PackageURL(target.Instance, req.Project, req.Package)
		s.appendEvent(&model.Event{
			ID:       "evt_" + ulid.Make().String(),
			Type:     model.EventCVEScanStarted,
			Tags:     req.Tags,
			Project:  req.Project,
			Package:  req.Package,
			Repo:     target.Repo,
			Arch:     target.Arch,
			What:     "CVE scan started",
			Why:      "",
			Version:  req.PrimaryTag,
			URL:      obsURL,
			At:       time.Now().UTC(),
			Instance: target.Instance,
		})

		slog.Info("cve: scanning", "pkg", req.Package, "arch", target.Arch, "image", imageRef)
		scan, err := s.runTrivy(ctx, imageRef, platform, target.Repo, target.Arch)
		if err != nil {
			slog.Warn("cve: trivy failed", "pkg", req.Package, "arch", target.Arch, "err", err)
			s.appendEvent(&model.Event{
				ID:       "evt_" + ulid.Make().String(),
				Type:     model.EventCVEScanFailed,
				Tags:     req.Tags,
				Project:  req.Project,
				Package:  req.Package,
				Repo:     target.Repo,
				Arch:     target.Arch,
				What:     "CVE scan failed",
				Why:      err.Error(),
				Version:  req.PrimaryTag,
				URL:      obsURL,
				At:       time.Now().UTC(),
				Instance: target.Instance,
			})
			continue
		}

		if err := store.UpsertCveScan(s.db, req.Project, req.Package, scan); err != nil {
			slog.Error("cve: upsert scan", "pkg", req.Package, "err", err)
			continue
		}

		why := "No CVEs found"
		if scan.CriticalCount > 0 || scan.HighCount > 0 {
			why = fmt.Sprintf("CRITICAL: %d, HIGH: %d", scan.CriticalCount, scan.HighCount)
		}
		slog.Info("cve: scan complete", "pkg", req.Package, "arch", target.Arch, "critical", scan.CriticalCount, "high", scan.HighCount)
		s.appendEvent(&model.Event{
			ID:       "evt_" + ulid.Make().String(),
			Type:     model.EventCVEScanFinished,
			Tags:     req.Tags,
			Project:  req.Project,
			Package:  req.Package,
			Repo:     target.Repo,
			Arch:     target.Arch,
			What:     "CVE scan finished",
			Why:      why,
			Version:  req.PrimaryTag,
			URL:      obsURL,
			At:       time.Now().UTC(),
			Instance: target.Instance,
		})
	}

	pkg, err := store.GetPackage(s.db, req.Project, req.Package)
	if err != nil || pkg == nil {
		return
	}
	_ = store.AttachCveScans(s.db, []*model.Package{pkg})
	s.hub.Notify(hubpkg.PackageUpdate(pkg))
}

func (s *Scanner) runTrivy(ctx context.Context, imageRef, platform, repo, arch string) (model.CveScan, error) {
	s.trivyMu.Lock()
	out, err := s.execFn(ctx, "trivy",
		"image",
		"--image-src", "remote",
		"--platform", "linux/"+platform,
		"--severity", "HIGH,CRITICAL",
		"--ignore-unfixed",
		"-f", "json",
		"-q",
		imageRef,
	)
	s.trivyMu.Unlock()
	if err != nil {
		// Exit code 2 means trivy found vulnerabilities — treat as success.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
			return parseTrivyOutput(out, imageRef, repo, arch)
		}
		// Include stderr in the error so failures show the actual trivy message.
		stderr := ""
		if exitErr != nil && len(exitErr.Stderr) > 0 {
			stderr = ": " + strings.TrimSpace(string(exitErr.Stderr))
		}
		return model.CveScan{}, fmt.Errorf("trivy: %w%s", err, stderr)
	}
	return parseTrivyOutput(out, imageRef, repo, arch)
}

type trivyOutput struct {
	Results []struct {
		Vulnerabilities []struct {
			VulnerabilityID  string `json:"VulnerabilityID"`
			PkgName          string `json:"PkgName"`
			InstalledVersion string `json:"InstalledVersion"`
			FixedVersion     string `json:"FixedVersion"`
			Severity         string `json:"Severity"`
			Title            string `json:"Title"`
		} `json:"Vulnerabilities"`
	} `json:"Results"`
}

func parseTrivyOutput(data []byte, imageRef, repo, arch string) (model.CveScan, error) {
	var raw trivyOutput
	if err := json.Unmarshal(data, &raw); err != nil {
		return model.CveScan{}, fmt.Errorf("parse trivy JSON: %w", err)
	}
	scan := model.CveScan{
		Repo:      repo,
		Arch:      arch,
		ImageRef:  imageRef,
		ScannedAt: time.Now().UTC(),
	}
	for _, result := range raw.Results {
		for _, v := range result.Vulnerabilities {
			f := model.CveFinding{
				ID:               v.VulnerabilityID,
				PkgName:          v.PkgName,
				InstalledVersion: v.InstalledVersion,
				FixedVersion:     v.FixedVersion,
				Severity:         v.Severity,
				Title:            v.Title,
			}
			scan.Findings = append(scan.Findings, f)
			switch v.Severity {
			case "CRITICAL":
				scan.CriticalCount++
			case "HIGH":
				scan.HighCount++
			}
		}
	}
	return scan, nil
}

func (s *Scanner) appendEvent(evt *model.Event) {
	if err := store.AppendEvent(s.db, evt); err != nil {
		slog.Error("cve: append event", "err", err)
		return
	}
	s.hub.Notify(hubpkg.NewEvent(evt))
}

// ScanNow runs one request synchronously (tests).
func (s *Scanner) ScanNow(ctx context.Context, req ScanRequest) { s.scanPackage(ctx, req) }

// SucceededTargets returns targets with state "succeeded". For release containers
// whose builds are intentionally disabled, all targets have state "disabled" —
// in that case the disabled targets are returned as a fallback so the scanner
// still knows which arches/repos to scan.
func SucceededTargets(targets []model.Target) []model.Target {
	var succeeded, disabled []model.Target
	for _, t := range targets {
		switch t.State {
		case "succeeded":
			succeeded = append(succeeded, t)
		case "disabled":
			disabled = append(disabled, t)
		}
	}
	if len(succeeded) > 0 {
		return succeeded
	}
	return disabled
}
