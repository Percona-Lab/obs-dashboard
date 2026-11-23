package release

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const connRepo = "percona/obs-packaging"

// newTestConnWith builds a Connection against a fake API with a fixed clock.
func newTestConnWith(t *testing.T, appID, keyB64, pat string) (*Connection, *fakeAppAPI, *time.Time) {
	t.Helper()
	key := sharedTestKey(t)
	c := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	clock := &c
	api := &fakeAppAPI{key: key, now: func() time.Time { return *clock }}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	conn := NewConnection(connRepo, appID, keyB64, pat)
	conn.APIBase = srv.URL
	conn.Now = func() time.Time { return *clock }
	return conn, api, clock
}

// newTestConn: pat == "" gives a healthy app-mode connection, else token mode.
func newTestConn(t *testing.T, pat string) (*Connection, *fakeAppAPI) {
	t.Helper()
	if pat != "" {
		c, api, _ := newTestConnWith(t, "", "", pat)
		return c, api
	}
	c, api, _ := newTestConnWith(t, "123456", testKeyB64(t, sharedTestKey(t), false), "")
	return c, api
}

func TestConnectionMode(t *testing.T) {
	good := testKeyB64(t, sharedTestKey(t), false)
	tests := []struct {
		id, key, pat string
		mode         string
		cfgErr       bool
		nilSource    bool
	}{
		{"1", good, "", ModeApp, false, false},
		{"1", good, "ghp_x", ModeApp, false, false},
		{"1", "", "ghp_x", ModeApp, true, false},
		{"", good, "", ModeApp, true, false},
		{"1", "!!!", "ghp_x", ModeApp, true, false},
		{"", "", "ghp_x", ModeToken, false, false},
		{"", "", "", ModeOff, false, true},
		{" ", " ", " ", ModeOff, false, true},
	}
	for i, tc := range tests {
		c := NewConnection(connRepo, tc.id, tc.key, tc.pat)
		if c.Mode() != tc.mode {
			t.Errorf("row %d: Mode = %q, want %q", i, c.Mode(), tc.mode)
		}
		if (c.ConfigErr() != nil) != tc.cfgErr {
			t.Errorf("row %d: ConfigErr = %v", i, c.ConfigErr())
		}
		if (c.TokenSource() == nil) != tc.nilSource {
			t.Errorf("row %d: TokenSource nil = %v", i, c.TokenSource() == nil)
		}
	}
	if ts := NewConnection(connRepo, "", "", "ghp_x").TokenSource(); ts != StaticToken("ghp_x") {
		t.Errorf("token-mode source = %#v", ts)
	}
	if _, ok := NewConnection(connRepo, "1", "", "").TokenSource().(*AppSource); !ok {
		t.Error("app-mode source is not an *AppSource")
	}
}

func TestConnectionCheckApp(t *testing.T) {
	c, _, clock := newTestConnWith(t, "123456", testKeyB64(t, sharedTestKey(t), false), "")
	st := c.Check(context.Background())
	if st.Mode != ModeApp || st.Repo != connRepo || !st.OK || st.Error != "" || st.FailedStep != "" {
		t.Fatalf("status = %+v", st)
	}
	if st.App == nil || *st.App != (ConnApp{ID: "123456", Slug: "percona-obs-release", Name: "Percona OBS Release"}) {
		t.Errorf("App = %+v", st.App)
	}
	if st.InstallationID != 7654321 {
		t.Errorf("InstallationID = %d", st.InstallationID)
	}
	wantNames := []string{"contents", "pull_requests", "actions", "checks", "metadata"}
	if len(st.Permissions) != 5 {
		t.Fatalf("Permissions = %+v", st.Permissions)
	}
	for i, p := range st.Permissions {
		if p.Name != wantNames[i] {
			t.Errorf("permission %d = %q, want %q", i, p.Name, wantNames[i])
		}
	}
	for _, p := range st.Permissions {
		if !p.OK {
			t.Errorf("permission %+v not ok", p)
		}
	}
	if st.Token == nil || st.Token.MintedAt == nil || st.Token.ExpiresAt == nil {
		t.Fatalf("Token = %+v", st.Token)
	}
	if !st.CheckedAt.Equal(*clock) || st.CheckedAt.Location() != time.UTC {
		t.Errorf("CheckedAt = %v", st.CheckedAt)
	}
}

func TestConnectionCheckFailedSteps(t *testing.T) {
	good := testKeyB64(t, sharedTestKey(t), false)
	tests := []struct {
		name  string
		key   string
		fault func(*fakeAppAPI)
		step  string
		err   string
		check func(t *testing.T, st ConnStatus)
	}{
		{"config", "!!!", nil, "config", "RELEASE_GITHUB_APP_PRIVATE_KEY is not valid base64", func(t *testing.T, st ConnStatus) {
			if st.App != nil || st.Token != nil {
				t.Errorf("App=%v Token=%v", st.App, st.Token)
			}
		}},
		{"app", good, func(f *fakeAppAPI) { f.appStatus = 401 }, "app", "github GET /app: HTTP 401", func(t *testing.T, st ConnStatus) {
			if st.App != nil || st.Token == nil {
				t.Errorf("App=%v Token=%v", st.App, st.Token)
			}
		}},
		{"installation", good, func(f *fakeAppAPI) { f.instStatus = 404 }, "installation", "github GET /repos/percona/obs-packaging/installation: HTTP 404", func(t *testing.T, st ConnStatus) {
			if st.App == nil || st.Permissions != nil {
				t.Errorf("App=%v Permissions=%v", st.App, st.Permissions)
			}
		}},
		{"token", good, func(f *fakeAppAPI) { f.mintStatus = 403 }, "token", "github POST /app/installations/7654321/access_tokens: HTTP 403", func(t *testing.T, st ConnStatus) {
			if st.Permissions == nil || st.Token == nil || st.Token.LastError != st.Error {
				t.Errorf("Permissions=%v Token=%+v", st.Permissions, st.Token)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, api, _ := newTestConnWith(t, "123456", tc.key, "")
			if tc.fault != nil {
				api.set(tc.fault)
			}
			st := c.Check(context.Background())
			if st.OK || st.FailedStep != tc.step || st.Error != tc.err {
				t.Fatalf("OK=%v step=%q err=%q", st.OK, st.FailedStep, st.Error)
			}
			tc.check(t, st)
		})
	}
}

func TestConnectionCheckIgnoresFailureMemory(t *testing.T) {
	c, api := newTestConn(t, "")
	ctx := context.Background()
	api.set(func(f *fakeAppAPI) { f.mintStatus = 500 })
	if _, err := c.TokenSource().Token(ctx); err == nil {
		t.Fatal("Token succeeded during outage")
	}
	api.set(func(f *fakeAppAPI) { f.mintStatus = 0 })
	if st := c.Check(ctx); !st.OK {
		t.Fatalf("Check = %+v", st)
	}
}

func TestConnectionPermissions(t *testing.T) {
	c, api := newTestConn(t, "")
	api.set(func(f *fakeAppAPI) {
		f.permissions = map[string]string{"contents": "read", "pull_requests": "admin", "actions": "write", "metadata": "read"}
	})
	st := c.Check(context.Background())
	if !st.OK {
		t.Fatalf("OK = false: %+v", st)
	}
	want := []ConnPermission{
		{"contents", "write", "read", false},
		{"pull_requests", "write", "admin", true},
		{"actions", "write", "write", true},
		{"checks", "read", "none", false},
		{"metadata", "read", "read", true},
	}
	if len(st.Permissions) != len(want) {
		t.Fatalf("Permissions = %+v", st.Permissions)
	}
	for i := range want {
		if st.Permissions[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, st.Permissions[i], want[i])
		}
	}
}

func TestConnectionCheckToken(t *testing.T) {
	c, api := newTestConn(t, "ghp_x")
	st := c.Check(context.Background())
	if st.Mode != ModeToken || !st.OK || st.App != nil || st.Token != nil || st.Repo != connRepo {
		t.Fatalf("status = %+v", st)
	}
	if got := api.lastRepoAuth(); got != "Bearer ghp_x" {
		t.Errorf("Authorization = %q", got)
	}
	api.set(func(f *fakeAppAPI) { f.repoStatus = 404 })
	st = c.Check(context.Background())
	if st.OK || st.FailedStep != "repository" || st.Error != "github GET /repos/percona/obs-packaging: HTTP 404" {
		t.Fatalf("status = %+v", st)
	}
}

func TestConnectionCheckOff(t *testing.T) {
	c, api, clock := newTestConnWith(t, "", "", "")
	st := c.Check(context.Background())
	if st.Mode != ModeOff || st.OK || st.Error != "" || st.FailedStep != "" {
		t.Fatalf("status = %+v", st)
	}
	if !st.CheckedAt.Equal(*clock) {
		t.Errorf("CheckedAt = %v", st.CheckedAt)
	}
	if n := api.requestCount(); n != 0 {
		t.Errorf("requests = %d", n)
	}
}

func TestConnectionCheckNoSecrets(t *testing.T) {
	good := testKeyB64(t, sharedTestKey(t), false)
	forbidden := []string{good, "BEGIN", "eyJ", "ghs_", "ghp_x"}
	scenarios := map[string]struct {
		id, key, pat string
		fault        func(*fakeAppAPI)
	}{
		"healthy":      {"123456", good, "", nil},
		"config":       {"123456", "!!!", "", nil},
		"app":          {"123456", good, "", func(f *fakeAppAPI) { f.appStatus = 401 }},
		"installation": {"123456", good, "", func(f *fakeAppAPI) { f.instStatus = 404 }},
		"token":        {"123456", good, "", func(f *fakeAppAPI) { f.mintStatus = 403 }},
		"pat":          {"", "", "ghp_x", nil},
		"pat-fail":     {"", "", "ghp_x", func(f *fakeAppAPI) { f.repoStatus = 401 }},
	}
	for name, sc := range scenarios {
		t.Run(name, func(t *testing.T) {
			c, api, _ := newTestConnWith(t, sc.id, sc.key, sc.pat)
			if sc.fault != nil {
				api.set(sc.fault)
			}
			b, err := json.Marshal(c.Check(context.Background()))
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range forbidden {
				if strings.Contains(string(b), f) {
					t.Errorf("status JSON contains %q", f[:min(len(f), 4)])
				}
			}
		})
	}
}
