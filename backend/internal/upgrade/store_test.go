package upgrade

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "obsboard.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func rel(v, host string) Release {
	return Release{Version: v, Notes: "notes " + v, URL: "https://h/" + v, PublishedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), HostChanges: host}
}

func TestStoreUpsertAndOrder(t *testing.T) {
	db, _ := openTestDB(t)
	s := store_{db}
	for _, v := range []string{"v0.9.0", "v0.10.0", "v0.2.0"} {
		if err := s.upsert(rel(v, "")); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.all()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Version != "v0.10.0" || rows[1].Version != "v0.9.0" || rows[2].Version != "v0.2.0" {
		t.Fatalf("order: %v", versions(rows))
	}
	if rows[0].Status != StatusAvailable {
		t.Fatalf("status %q", rows[0].Status)
	}
}

func TestStoreUpsertKeepsStatusAndRevivesSuperseded(t *testing.T) {
	db, _ := openTestDB(t)
	s := store_{db}
	now := time.Date(2026, 11, 6, 12, 0, 0, 123, time.UTC)
	_ = s.upsert(rel("v0.2.0", ""))
	_ = s.upsert(rel("v0.3.0", ""))
	if err := s.markApplying("v0.2.0", "a@percona.com", now); err != nil {
		t.Fatal(err)
	}
	if err := s.setStatus("v0.3.0", StatusSuperseded); err != nil {
		t.Fatal(err)
	}
	r2 := rel("v0.2.0", "new host text")
	r2.Notes = "edited"
	_ = s.upsert(r2)
	_ = s.upsert(rel("v0.3.0", ""))
	rows, _ := s.all()
	byV := map[string]Row{}
	for _, r := range rows {
		byV[r.Version] = r
	}
	if r := byV["v0.2.0"]; r.Status != StatusApplying || r.Notes != "edited" || r.HostChanges != "new host text" || r.RequestedBy != "a@percona.com" || r.RequestedAt == nil || !r.RequestedAt.Equal(now) {
		t.Fatalf("v0.2.0 = %+v", r)
	}
	if byV["v0.3.0"].Status != StatusAvailable {
		t.Fatalf("superseded not revived: %q", byV["v0.3.0"].Status)
	}
}

func TestStoreMarkFinished(t *testing.T) {
	db, _ := openTestDB(t)
	s := store_{db}
	_ = s.upsert(rel("v0.2.0", ""))
	at := time.Date(2026, 11, 6, 12, 3, 0, 0, time.UTC)
	_ = s.markApplying("v0.2.0", "a@percona.com", at)
	if err := s.markFinished("v0.2.0", false, "boom", at); err != nil {
		t.Fatal(err)
	}
	v, err := s.applying()
	if err != nil || v != "" {
		t.Fatalf("applying = %q, %v", v, err)
	}
	rows, _ := s.all()
	if rows[0].Status != StatusFailed || rows[0].Error != "boom" || rows[0].FinishedAt == nil || !rows[0].FinishedAt.Equal(at) {
		t.Fatalf("row = %+v", rows[0])
	}
}

func versions(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Version
	}
	return out
}
