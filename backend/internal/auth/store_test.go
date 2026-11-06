package auth

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/percona/obs-dashboard/internal/store"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var t0 = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

func TestStoreAddListUsers(t *testing.T) {
	s := NewStore(newTestDB(t))
	users, err := s.ListUsers()
	if err != nil || users == nil || len(users) != 0 {
		t.Fatalf("empty list: users=%v err=%v", users, err)
	}
	if _, err := s.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUser("ann@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddUser("bob@percona.com", "x@percona.com", t0); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate add: want ErrExists, got %v", err)
	}
	users, err = s.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Email != "ann@percona.com" || users[1].Email != "bob@percona.com" {
		t.Fatalf("unexpected users: %+v", users)
	}
	if users[1].AddedBy != "alice@percona.com" || users[1].AddedAt != "2026-11-06T12:00:00.000000000Z" {
		t.Fatalf("unexpected row: %+v", users[1])
	}
	ok, err := s.HasUser("bob@percona.com")
	if err != nil || !ok {
		t.Fatalf("HasUser: %v %v", ok, err)
	}
}

func TestStoreRemoveUserDeletesSessions(t *testing.T) {
	s := NewStore(newTestDB(t))
	if _, err := s.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	sid, err := s.CreateSession("bob@percona.com", t0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveUser("bob@percona.com"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SessionEmail(sid, t0); ok {
		t.Fatal("session survived user removal")
	}
	if err := s.RemoveUser("bob@percona.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove: want ErrNotFound, got %v", err)
	}
}

func TestSessionRoundTrip(t *testing.T) {
	s := NewStore(newTestDB(t))
	sid, err := s.CreateSession("bob@percona.com", t0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(sid) < 40 {
		t.Fatalf("session id too short: %q", sid)
	}
	email, ok, err := s.SessionEmail(sid, t0.Add(59*time.Minute))
	if err != nil || !ok || email != "bob@percona.com" {
		t.Fatalf("lookup: %q %v %v", email, ok, err)
	}
	if _, ok, _ := s.SessionEmail("nope", t0); ok {
		t.Fatal("unknown id resolved")
	}
	if err := s.DeleteSession(sid); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SessionEmail(sid, t0); ok {
		t.Fatal("deleted session resolved")
	}
}

// Review focus 2: expiry is exact and string comparison is width-safe.
func TestSessionExpiryBoundary(t *testing.T) {
	s := NewStore(newTestDB(t))
	start := t0.Add(123456789 * time.Nanosecond) // non-zero fraction
	sid, err := s.CreateSession("bob@percona.com", start, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SessionEmail(sid, start.Add(time.Hour-time.Nanosecond)); !ok {
		t.Fatal("session expired early")
	}
	if _, ok, _ := s.SessionEmail(sid, start.Add(time.Hour)); ok {
		t.Fatal("session valid at expires_at")
	}
	// The expired row was deleted: even an earlier clock no longer finds it.
	if _, ok, _ := s.SessionEmail(sid, start); ok {
		t.Fatal("expired session row not deleted")
	}
}

func TestPruneExpired(t *testing.T) {
	s := NewStore(newTestDB(t))
	old, _ := s.CreateSession("a@percona.com", t0, time.Minute)
	fresh, _ := s.CreateSession("b@percona.com", t0, 2*time.Hour)
	n, err := s.PruneExpired(t0.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("prune: n=%d err=%v", n, err)
	}
	if _, ok, _ := s.SessionEmail(fresh, t0.Add(time.Hour)); !ok {
		t.Fatal("fresh session pruned")
	}
	if _, ok, _ := s.SessionEmail(old, t0); ok {
		t.Fatal("old session not pruned")
	}
}
