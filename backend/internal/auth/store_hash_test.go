package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestSessionIDHashedAtRest(t *testing.T) {
	db := newTestDB(t)
	s := NewStore(db)
	tok, err := s.CreateSession("a@percona.com", t0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRow(`SELECT id FROM sessions`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(tok))
	if id == tok || id != hex.EncodeToString(sum[:]) {
		t.Fatalf("stored id %q should be sha256 hex of token", id)
	}
	if email, ok, err := s.SessionEmail(tok, t0); err != nil || !ok || email != "a@percona.com" {
		t.Fatalf("lookup by raw token: %q %v %v", email, ok, err)
	}
	if err := s.DeleteSession(tok); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.SessionEmail(tok, t0); ok {
		t.Fatal("session survived delete")
	}
}
