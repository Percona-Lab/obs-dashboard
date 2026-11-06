package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func (hs *harness) send(method, path, body, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	return hs.do(req, cookies...)
}

type usersBody struct {
	Admins []string `json:"admins"`
	Users  []User   `json:"users"`
}

func listUsers(t *testing.T, hs *harness, sess *http.Cookie) usersBody {
	t.Helper()
	rec := hs.send(http.MethodGet, "/api/settings/users", "", "", sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var b usersBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUsersAddAndList(t *testing.T) {
	hs := newHarness(t, "alice@percona.com", "root@percona.com")
	sess := hs.signIn("alice@percona.com")
	rec := hs.send(http.MethodPost, "/api/settings/users", `{"email":"  Bob@Percona.com "}`, testOrigin, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body.String())
	}
	var u User
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatal(err)
	}
	if u.Email != "bob@percona.com" || u.AddedBy != "alice@percona.com" || u.AddedAt == "" {
		t.Fatalf("row: %+v", u)
	}
	b := listUsers(t, hs, sess)
	if strings.Join(b.Admins, ",") != "alice@percona.com,root@percona.com" || len(b.Users) != 1 || b.Users[0].Email != "bob@percona.com" {
		t.Fatalf("list: %+v", b)
	}
}

func TestUsersAddRejects(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	sess := hs.signIn("alice@percona.com")
	for body, want := range map[string]int{
		`not json`:                      http.StatusBadRequest,
		`{"email":"bob@gmail.com"}`:     http.StatusBadRequest,
		`{"email":"@percona.com"}`:      http.StatusBadRequest,
		`{"email":"a@b@percona.com"}`:   http.StatusBadRequest,
		`{"email":"ALICE@percona.com"}`: http.StatusConflict,
	} {
		if rec := hs.send(http.MethodPost, "/api/settings/users", body, testOrigin, sess); rec.Code != want {
			t.Errorf("%s: got %d want %d (%s)", body, rec.Code, want, rec.Body.String())
		}
	}
	if rec := hs.send(http.MethodPost, "/api/settings/users", `{"email":"bob@percona.com"}`, testOrigin, sess); rec.Code != http.StatusCreated {
		t.Fatalf("first add: %d", rec.Code)
	}
	if rec := hs.send(http.MethodPost, "/api/settings/users", `{"email":"bob@percona.com"}`, testOrigin, sess); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate add: %d", rec.Code)
	}
}

func TestUsersRemove(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if _, err := hs.svc.store.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := hs.svc.store.AddUser("carol@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	carol := hs.signIn("carol@percona.com")
	for path, want := range map[string]int{
		"/api/settings/users/alice@percona.com":  http.StatusBadRequest, // config admin
		"/api/settings/users/carol@percona.com":  http.StatusBadRequest, // self
		"/api/settings/users/nobody@percona.com": http.StatusNotFound,
		"/api/settings/users/bob@percona.com":    http.StatusNoContent,
	} {
		if rec := hs.send(http.MethodDelete, path, "", testOrigin, carol); rec.Code != want {
			t.Errorf("%s: got %d want %d (%s)", path, rec.Code, want, rec.Body.String())
		}
	}
	if b := listUsers(t, hs, carol); len(b.Users) != 1 || b.Users[0].Email != "carol@percona.com" {
		t.Fatalf("after remove: %+v", b.Users)
	}
}

// Review focus 5.
func TestRemoveUserNormalizesPath(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if _, err := hs.svc.store.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	sess := hs.signIn("alice@percona.com")
	if rec := hs.send(http.MethodDelete, "/api/settings/users/Bob%40Percona.com", "", testOrigin, sess); rec.Code != http.StatusNoContent {
		t.Fatalf("encoded delete: %d %s", rec.Code, rec.Body.String())
	}
}

// Review focus 3.
func TestRemovedUserLosesAccess(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if _, err := hs.svc.store.AddUser("bob@percona.com", "alice@percona.com", t0); err != nil {
		t.Fatal(err)
	}
	bob := hs.signIn("bob@percona.com")
	alice := hs.signIn("alice@percona.com")
	if rec := hs.post("/api/protected", testOrigin, bob); rec.Code != http.StatusNoContent {
		t.Fatalf("bob before removal: %d", rec.Code)
	}
	if rec := hs.send(http.MethodDelete, "/api/settings/users/bob@percona.com", "", testOrigin, alice); rec.Code != http.StatusNoContent {
		t.Fatalf("remove bob: %d", rec.Code)
	}
	if rec := hs.post("/api/protected", testOrigin, bob); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bob after removal: %d", rec.Code)
	}
}

func TestUsersRequireSessionAndOrigin(t *testing.T) {
	hs := newHarness(t, "alice@percona.com")
	if rec := hs.send(http.MethodGet, "/api/settings/users", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d", rec.Code)
	}
	if rec := hs.send(http.MethodPost, "/api/settings/users", `{"email":"bob@percona.com"}`, testOrigin); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous add: %d", rec.Code)
	}
	sess := hs.signIn("alice@percona.com")
	if rec := hs.send(http.MethodPost, "/api/settings/users", `{"email":"bob@percona.com"}`, "http://evil.test", sess); rec.Code != http.StatusForbidden {
		t.Fatalf("bad origin add: %d", rec.Code)
	}
	if rec := hs.send(http.MethodDelete, "/api/settings/users/bob@percona.com", "", "http://evil.test", sess); rec.Code != http.StatusForbidden {
		t.Fatalf("bad origin delete: %d", rec.Code)
	}
}
