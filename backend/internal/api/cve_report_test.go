package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/cvereport"
	"github.com/percona/obs-dashboard/internal/model"
	"github.com/percona/obs-dashboard/internal/store"
)

func seedReportScan(t *testing.T, db *sql.DB, project string, ids ...string) {
	t.Helper()
	var findings []model.CveFinding
	for _, id := range ids {
		findings = append(findings, model.CveFinding{
			ID: id, PkgName: "openssl", InstalledVersion: "1", FixedVersion: "2",
			Severity: "HIGH", Class: "os-pkgs", Vendor: "Red Hat, Inc.", Src: "openssl",
		})
	}
	scan := model.CveScan{
		Repo: "ubi9", Arch: "x86_64", ImageRef: "img", ScannedAt: time.Now().UTC(),
		HighCount: len(ids), Findings: findings,
	}
	if err := store.UpsertCveScan(db, project, "pg", scan); err != nil {
		t.Fatalf("UpsertCveScan: %v", err)
	}
}

func TestCveReportHandler(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	h := cveReportHandler(db, newTTLCache[cvereport.Report](time.Minute))

	// Empty DB: arrays are never null.
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/cve/report", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty: expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "null") {
		t.Fatalf("empty: body contains null: %s", body)
	}
	for _, want := range []string{`"ours":[]`, `"released":[]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("empty: body missing %s: %s", want, body)
		}
	}

	seedReportScan(t, db, "ppg:staging:18:containers", "CVE-1")
	seedReportScan(t, db, "ppg:releases:18:containers", "CVE-1", "CVE-2")
	h = cveReportHandler(db, newTTLCache[cvereport.Report](time.Minute))
	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/cve/report", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var rep cvereport.Report
	if err := json.NewDecoder(rec.Body).Decode(&rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rep.Staging.BaseOS) != 1 {
		t.Fatalf("staging.base_os: expected 1 row, got %d", len(rep.Staging.BaseOS))
	}
	if len(rep.Released) != 1 || rep.Released[0].Total != 2 || rep.Released[0].FixedInStaging != 1 {
		t.Fatalf("released: unexpected %+v", rep.Released)
	}
}

func TestCveReportHandlerStoreError(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	db.Close()
	h := cveReportHandler(db, newTTLCache[cvereport.Report](time.Minute))
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/cve/report", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
}

func TestCveReportRoute(t *testing.T) {
	router := setupTestServer(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cve/report", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected JSON, got %q", ct)
	}
}
