package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/auth"
	"github.com/percona/obs-dashboard/internal/hub"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/obs"
	"github.com/percona/obs-dashboard/internal/store"
)

// stubOBSServer returns a test HTTP server that replies with an empty OBS
// _result?view=versrel XML response for any request. Used so that releases
// handlers (which require an OBS client) return empty data rather than 503.
func stubOBSServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<resultlist state=""></resultlist>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setupTestServer(t *testing.T) http.Handler {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	obsSrv := stubOBSServer(t)
	obsClient := obs.SingleFleet(obs.NewClient(obsSrv.URL, "user", "pass"), "isv:percona")
	return NewRouter(db, hub.New(), obsClient, fakeStatter{}, new(atomic.Bool), time.Duration(0), fakePollState{s: "active"}, auth.NewService(db, auth.Options{}), nil)
}

func TestPackagesHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/staging/17/packages", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// Must be a JSON array (not null).
	if string(result) == "null" {
		t.Fatal("expected JSON array, got null")
	}

	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestEventsHandler_WindowParam(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/staging/17/events?window=1440", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if string(result) == "null" {
		t.Fatal("expected JSON array, got null")
	}

	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestEventsHandler_DateRangeParam(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/staging/17/events?from=2026-01-01&to=2026-12-31", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if string(result) == "null" {
		t.Fatal("expected JSON array, got null")
	}

	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestEventsHandler_DefaultWindow(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/staging/17/events", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var arr []interface{}
	if err := json.NewDecoder(rec.Body).Decode(&arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestEventsHandler_InvalidWindow(t *testing.T) {
	router := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/staging/17/events?window=notanumber", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPRContextPackagesHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/17/packages", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(result) == "null" {
		t.Fatal("expected JSON array, got null")
	}
	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestPRContextEventsHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/17/events", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var result json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(result) == "null" {
		t.Fatal("expected JSON array, got null")
	}
	var arr []interface{}
	if err := json.Unmarshal(result, &arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestPRContextEventsHandler_WindowParam(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/17/events?window=60", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var arr []interface{}
	if err := json.NewDecoder(rec.Body).Decode(&arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestPRContextEventsHandler_InvalidWindow(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/17/events?window=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestReleasesPackagesHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/releases/ppg/17/packages", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var arr []interface{}
	if err := json.NewDecoder(rec.Body).Decode(&arr); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestReleasesReposHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/releases/ppg/17/repos", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp ReposResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RPM == nil || resp.DEB == nil {
		t.Fatal("expected non-nil rpm and deb slices")
	}
}

func TestPRReposHandler_EmptyDB(t *testing.T) {
	router := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/pr/pr-92/17/repos", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp ReposResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.RPM == nil || resp.DEB == nil {
		t.Fatal("expected non-nil rpm and deb slices")
	}
}

func TestReposSubprojectParam(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	obsSrv := stubOBSServer(t)
	obsClient := obs.SingleFleet(obs.NewClient(obsSrv.URL, "user", "pass"), "isv:percona")
	router := NewRouter(db, hub.New(), obsClient, fakeStatter{}, new(atomic.Bool), time.Duration(0), fakePollState{s: "active"}, auth.NewService(db, auth.Options{}), nil)

	falseVal := false
	now := time.Now()
	mainPkg := &model.Package{
		Project: "ppg:staging:18", Name: "percona-postgresql18",
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		IsContainer: &falseVal,
		Targets:     []model.Target{{Repo: "Debian_13", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   now,
	}
	if err := store.UpsertPackageState(db, mainPkg, mainPkg.UpdatedAt); err != nil {
		t.Fatalf("seed main pkg: %v", err)
	}
	extrasPkg := &model.Package{
		Project: "ppg:staging:18:extras", Name: "percona-postgresql18-extras",
		RollupState: model.RollupSucceeded, OKTargets: 1, TotalTargets: 1,
		IsContainer: &falseVal,
		Targets:     []model.Target{{Repo: "UBI_9", Arch: "x86_64", State: "succeeded"}},
		UpdatedAt:   now,
	}
	if err := store.UpsertPackageState(db, extrasPkg, extrasPkg.UpdatedAt); err != nil {
		t.Fatalf("seed extras pkg: %v", err)
	}

	containsRepo := func(resp ReposResponse, obsName string) bool {
		for _, r := range resp.RPM {
			if r.OBS == obsName {
				return true
			}
		}
		for _, r := range resp.DEB {
			if r.OBS == obsName {
				return true
			}
		}
		return false
	}

	// 1. subproject=extras returns only extras repos.
	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/staging/18/repos?subproject=extras", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp ReposResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !containsRepo(resp, "UBI_9") {
		t.Errorf("expected UBI_9 in extras response, got %+v", resp)
	}
	if containsRepo(resp, "Debian_13") {
		t.Errorf("did not expect Debian_13 in extras response, got %+v", resp)
	}

	// 2. no subproject param: unchanged behaviour, main repo present.
	req2 := httptest.NewRequest(http.MethodGet, "/api/products/ppg/staging/18/repos", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec2.Code)
	}
	var resp2 ReposResponse
	if err := json.NewDecoder(rec2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !containsRepo(resp2, "Debian_13") {
		t.Errorf("expected Debian_13 in default response, got %+v", resp2)
	}

	// 3. invalid subproject param -> 400.
	req3 := httptest.NewRequest(http.MethodGet, "/api/products/ppg/staging/18/repos?subproject=EX%25TRAS", nil)
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec3.Code)
	}
}

func TestRebuildHandler_Success(t *testing.T) {
	obsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer obsSrv.Close()

	obsClient := obs.SingleFleet(obs.NewClient(obsSrv.URL, "user", "pass"), "isv:percona")
	handler := rebuildHandler(obsClient)

	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(oldLogger)

	body := `{"project":"ppg:17","repo":"RockyLinux_9","arch":"x86_64","package":"percona-pg_tde"}`
	req := httptest.NewRequest(http.MethodPost, "/api/rebuild", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp)
	}
	out := logBuf.String()
	if !strings.Contains(out, "api: rebuild triggered") || !strings.Contains(out, "percona-pg_tde") {
		t.Errorf("expected info log for the manual rebuild trigger, got: %q", out)
	}
}

func TestRebuildHandler_MissingField(t *testing.T) {
	obsClient := obs.SingleFleet(obs.NewClient("http://example.com", "user", "pass"), "isv:percona")
	handler := rebuildHandler(obsClient)

	// missing arch and package
	body := `{"project":"ppg:17","repo":"RockyLinux_9"}`
	req := httptest.NewRequest(http.MethodPost, "/api/rebuild", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRebuildHandler_OBSError(t *testing.T) {
	obsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "permission denied", http.StatusForbidden)
	}))
	defer obsSrv.Close()

	obsClient := obs.SingleFleet(obs.NewClient(obsSrv.URL, "user", "pass"), "isv:percona")
	handler := rebuildHandler(obsClient)

	body := `{"project":"ppg:17","repo":"RockyLinux_9","arch":"x86_64","package":"percona-pg_tde"}`
	req := httptest.NewRequest(http.MethodPost, "/api/rebuild", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRebuildHandler_InvalidJSON(t *testing.T) {
	obsClient := obs.SingleFleet(obs.NewClient("http://example.com", "user", "pass"), "isv:percona")
	handler := rebuildHandler(obsClient)

	req := httptest.NewRequest(http.MethodPost, "/api/rebuild", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}

func TestTelemetryEndpoint(t *testing.T) {
	var enabled atomic.Bool
	set := telemetrySetHandler(&enabled)
	status := telemetryStatusHandler(&enabled, 60*time.Second)

	// enable
	req := httptest.NewRequest(http.MethodPost, "/api/telemetry?enabled=true", nil)
	w := httptest.NewRecorder()
	set(w, req)
	if w.Code != http.StatusOK || !enabled.Load() {
		t.Fatalf("enable failed: code=%d enabled=%v", w.Code, enabled.Load())
	}

	// status reflects it
	w = httptest.NewRecorder()
	status(w, httptest.NewRequest(http.MethodGet, "/api/telemetry", nil))
	var body struct {
		Enabled  bool   `json:"enabled"`
		Interval string `json:"interval"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Enabled || body.Interval != "1m0s" {
		t.Fatalf("status = %+v", body)
	}

	// invalid → 400
	w = httptest.NewRecorder()
	set(w, httptest.NewRequest(http.MethodPost, "/api/telemetry", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing enabled: code=%d, want 400", w.Code)
	}
}

func TestCveScansHandler(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Seed one scan with a finding; UpsertCveScan sets cve_since itself.
	scan := model.CveScan{
		Arch:          "x86_64",
		ImageRef:      "registry.example/percona-postgresql17:latest",
		ScannedAt:     time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		CriticalCount: 1,
		HighCount:     0,
		Findings: []model.CveFinding{{
			ID:               "CVE-2026-0001",
			PkgName:          "openssl",
			InstalledVersion: "3.0.1",
			FixedVersion:     "3.0.2",
			Severity:         "CRITICAL",
			Title:            "test vulnerability",
		}},
	}
	if err := store.UpsertCveScan(db, "ppg:17:containers", "percona-postgresql17", scan); err != nil {
		t.Fatalf("UpsertCveScan: %v", err)
	}

	h := cveScansHandler(db)

	t.Run("seeded rows returned with findings", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet,
			"/api/cve/scans?project=ppg:17:containers&package=percona-postgresql17", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		var scans []model.CveScan
		if err := json.NewDecoder(rec.Body).Decode(&scans); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(scans) != 1 {
			t.Fatalf("expected 1 scan, got %d", len(scans))
		}
		got := scans[0]
		if got.Arch != "x86_64" || got.CriticalCount != 1 {
			t.Fatalf("unexpected scan: %+v", got)
		}
		if got.CveSince == nil {
			t.Fatal("expected cve_since to be set for a vulnerable scan")
		}
		if len(got.Findings) != 1 || got.Findings[0].ID != "CVE-2026-0001" {
			t.Fatalf("unexpected findings: %+v", got.Findings)
		}
	})

	t.Run("missing params rejected", func(t *testing.T) {
		for _, url := range []string{
			"/api/cve/scans",
			"/api/cve/scans?project=ppg:17:containers",
			"/api/cve/scans?package=percona-postgresql17",
			"/api/cve/scans?project=&package=percona-postgresql17",
			"/api/cve/scans?project=ppg:17:containers&package=",
		} {
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, url, nil))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400, got %d", url, rec.Code)
			}
		}
	})

	t.Run("unknown pair returns empty array", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h(rec, httptest.NewRequest(http.MethodGet,
			"/api/cve/scans?project=nope&package=missing", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
		if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
			t.Fatalf("expected [], got %q", body)
		}
	})
}

func TestCveScansRoute(t *testing.T) {
	router := setupTestServer(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/api/cve/scans?project=p&package=k", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("route not registered or failing: expected 200, got %d", rec.Code)
	}
}

func TestProductsTierValidation(t *testing.T) {
	router := setupTestServer(t)
	for _, path := range []string{
		"/api/products/ppg/bogus/17/packages",
		"/api/products/ppg/bogus/17/events",
		"/api/products/ppg/bogus/17/repos",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "tier must be devel or staging") {
			t.Fatalf("%s: unexpected body %q", path, rec.Body.String())
		}
	}
}

func TestProductsTierRoutes(t *testing.T) {
	router := setupTestServer(t)
	for _, path := range []string{
		"/api/products/ppg/devel/18/packages",
		"/api/products/ppg/staging/17/repos",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, rec.Code)
		}
	}
}

// The board events feed must include the shared common trees alongside the
// tier subtree — common packages appear in both tier views, so do their events.
func TestEventsHandlerIncludesCommonTrees(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	obsSrv := stubOBSServer(t)
	obsClient := obs.SingleFleet(obs.NewClient(obsSrv.URL, "user", "pass"), "isv:percona")
	router := NewRouter(db, hub.New(), obsClient, fakeStatter{}, new(atomic.Bool), time.Duration(0), fakePollState{s: "active"}, auth.NewService(db, auth.Options{}), nil)

	now := time.Now().UTC()
	seed := func(id, project string) {
		if err := store.AppendEvent(db, &model.Event{
			ID: id, Type: model.EventBuildStarted, Project: project,
			Package: "p", What: "w", At: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed("evt_staging", "ppg:staging:17")
	seed("evt_ppg_common", "ppg:common")
	seed("evt_global_common", "common")
	seed("evt_devel", "ppg:devel:17")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/products/ppg/staging/17/events?window=60", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var events []model.Event
	if err := json.NewDecoder(rec.Body).Decode(&events); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	ids := make(map[string]bool, len(events))
	for _, e := range events {
		ids[e.ID] = true
	}

	for _, want := range []string{"evt_staging", "evt_ppg_common", "evt_global_common"} {
		if !ids[want] {
			t.Errorf("expected event %q in response, got ids %v", want, ids)
		}
	}
	if ids["evt_devel"] {
		t.Errorf("did not expect evt_devel in staging tier response, got ids %v", ids)
	}
}
