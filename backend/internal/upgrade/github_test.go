package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const releasesJSON = `[
 {"tag_name":"v0.3.0","body":"## Added\n- x\n\n### Host changes\n\nAdd FOO=1 to env.\n\n### Fixed\n- y","html_url":"https://h/v0.3.0","draft":false,"prerelease":false,"published_at":"2026-11-03T10:00:00Z"},
 {"tag_name":"v0.2.0","body":"notes","html_url":"https://h/v0.2.0","draft":false,"prerelease":false,"published_at":"2026-11-02T10:00:00Z"},
 {"tag_name":"v0.4.0","body":"","html_url":"","draft":true,"prerelease":false,"published_at":null},
 {"tag_name":"v0.5.0-rc1","body":"","html_url":"","draft":false,"prerelease":true,"published_at":"2026-11-04T10:00:00Z"},
 {"tag_name":"latest","body":"","html_url":"","draft":false,"prerelease":false,"published_at":"2026-11-04T10:00:00Z"},
 {"tag_name":"v0.6.0","body":"","html_url":"","draft":false,"prerelease":false,"published_at":null}
]`

func TestGitHubSourceFiltersReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Percona-Lab/obs-dashboard/releases" || r.URL.Query().Get("per_page") != "30" {
			t.Errorf("unexpected request %s", r.URL)
		}
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		_, _ = w.Write([]byte(releasesJSON))
	}))
	defer srv.Close()

	rels, err := GitHubSource{APIBase: srv.URL, Repo: "Percona-Lab/obs-dashboard"}.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 2 || rels[0].Version != "v0.3.0" || rels[1].Version != "v0.2.0" {
		t.Fatalf("releases = %+v", rels)
	}
	if rels[0].HostChanges != "Add FOO=1 to env." || rels[0].URL != "https://h/v0.3.0" || rels[0].PublishedAt.IsZero() {
		t.Fatalf("v0.3.0 = %+v", rels[0])
	}
	if rels[1].HostChanges != "" {
		t.Fatalf("v0.2.0 host changes = %q", rels[1].HostChanges)
	}
}

func TestGitHubSourceHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := (GitHubSource{APIBase: srv.URL, Repo: "a/b"}).Releases(context.Background()); err == nil {
		t.Fatal("want error on 403")
	}
}

func TestHostChanges(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"absent":         {"## Added\n- x", ""},
		"h3":             {"### Host changes\nnew env\n### Fixed\n- y", "new env"},
		"h2 last":        {"## Host changes\n\nline1\nline2\n", "line1\nline2"},
		"h4 mixed case":  {"#### HOST Changes\nA\n#### Other\nB", "A"},
		"deeper kept":    {"### Host changes\nA\n#### Detail\nB\n## Next\nC", "A\n#### Detail\nB"},
		"crlf":           {"### Host changes\r\nAdd BAR\r\n### Fixed\r\n- y\r\n", "Add BAR"},
		"h1 not matched": {"# Host changes\nA", ""},
		"h5 not matched": {"##### Host changes\nA", ""},
	}
	for name, c := range cases {
		if got := HostChanges(c.in); got != c.want {
			t.Errorf("%s: HostChanges = %q, want %q", name, got, c.want)
		}
	}
}
