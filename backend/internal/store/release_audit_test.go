package store

import (
	"testing"
	"time"
)

func TestReleaseAuditRoundTrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cut := ReleaseAudit{
		At:     time.Date(2026, 11, 10, 9, 0, 0, 0, time.FixedZone("CET", 3600)),
		Email:  "a@example.com",
		Action: "cut",
		Branch: "release/x",
	}
	rel := ReleaseAudit{
		At:     time.Date(2026, 11, 10, 10, 30, 0, 500000000, time.UTC),
		Email:  "b@example.com",
		Action: "release",
		Branch: "release/x",
		PR:     7,
		Detail: "lane a\nlane b",
	}
	if err := AddReleaseAudit(db, cut); err != nil {
		t.Fatal(err)
	}
	if err := AddReleaseAudit(db, rel); err != nil {
		t.Fatal(err)
	}

	byBranch, err := ReleaseAuditByBranch(db, []string{"release/x", "release/none"})
	if err != nil {
		t.Fatal(err)
	}
	got := byBranch["release/x"]
	if len(got) != 2 {
		t.Fatalf("by branch: got %d rows, want 2: %+v", len(got), got)
	}
	if got[0].Action != "cut" || got[1].Action != "release" {
		t.Fatalf("rows not oldest first: %+v", got)
	}
	if !got[0].At.Equal(cut.At) || got[0].At.Location() != time.UTC {
		t.Errorf("cut.At = %v (%v), want %v in UTC", got[0].At, got[0].At.Location(), cut.At)
	}
	if !got[1].At.Equal(rel.At) {
		t.Errorf("release.At = %v, want %v", got[1].At, rel.At)
	}
	if got[1].PR != 7 || got[1].Detail != "lane a\nlane b" || got[1].Email != "b@example.com" {
		t.Errorf("release row fields lost: %+v", got[1])
	}
	if len(byBranch["release/none"]) != 0 {
		t.Errorf("unknown branch returned rows: %+v", byBranch["release/none"])
	}

	byPR, err := ReleaseAuditByPR(db, []int{7}, "release")
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := byPR[7]; !ok || r.Action != "release" || r.Detail != "lane a\nlane b" {
		t.Fatalf("by PR: got %+v", byPR)
	}

	empty, err := ReleaseAuditByPR(db, nil, "release")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty PR input: got %v, %v", empty, err)
	}
	emptyB, err := ReleaseAuditByBranch(db, nil)
	if err != nil || emptyB == nil || len(emptyB) != 0 {
		t.Fatalf("empty branch input: got %v, %v", emptyB, err)
	}
}
