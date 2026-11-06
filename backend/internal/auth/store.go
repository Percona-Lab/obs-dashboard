// Package auth owns sign-in: the Google OIDC flow, the allowlist, sessions,
// and the HTTP middleware that gates state-changing routes.
package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"
)

// tsLayout is RFC3339 with a fixed nine-digit fraction. Always formatted in
// UTC, so stored timestamps compare correctly as strings. Never bind a raw
// time.Time: the modernc driver stores it in a different format.
const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

func ts(t time.Time) string { return t.UTC().Format(tsLayout) }

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
)

// User is one allowlist row.
type User struct {
	Email   string `json:"email"`
	AddedBy string `json:"added_by"`
	AddedAt string `json:"added_at"`
}

// Store persists the allowlist and sessions.
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// randomToken returns 32 random bytes, base64url without padding.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT email, added_by, added_at FROM allowed_users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Email, &u.AddedBy, &u.AddedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) HasUser(email string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM allowed_users WHERE email = ?`, email).Scan(&n)
	return n > 0, err
}

// AddUser inserts an allowlist row; ErrExists when the email is already there.
func (s *Store) AddUser(email, addedBy string, now time.Time) (User, error) {
	u := User{Email: email, AddedBy: addedBy, AddedAt: ts(now)}
	res, err := s.db.Exec(
		`INSERT INTO allowed_users (email, added_by, added_at) VALUES (?, ?, ?) ON CONFLICT(email) DO NOTHING`,
		u.Email, u.AddedBy, u.AddedAt)
	if err != nil {
		return User{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return User{}, ErrExists
	}
	return u, nil
}

// RemoveUser deletes the allowlist row and every session of that email.
func (s *Store) RemoveUser(email string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM allowed_users WHERE email = ?`, email)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE email = ?`, email); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateSession stores a new session and returns its opaque id.
func (s *Store) CreateSession(email string, now time.Time, ttl time.Duration) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`INSERT INTO sessions (id, email, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		id, email, ts(now), ts(now.Add(ttl)))
	if err != nil {
		return "", err
	}
	return id, nil
}

// SessionEmail resolves an unexpired session. An expired row is deleted.
func (s *Store) SessionEmail(id string, now time.Time) (string, bool, error) {
	var email, expires string
	err := s.db.QueryRow(`SELECT email, expires_at FROM sessions WHERE id = ?`, id).Scan(&email, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if expires <= ts(now) {
		_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
		return "", false, err
	}
	return email, true, nil
}

func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// PruneExpired deletes every session whose expiry is at or before now.
func (s *Store) PruneExpired(now time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, ts(now))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
