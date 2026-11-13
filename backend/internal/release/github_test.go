package release

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newGHClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{APIBase: srv.URL, Repo: "percona/obs-packaging", Token: "ghp_SECRET", HTTP: srv.Client()}
}

func TestGitHubAuthHeadersAndETag(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer ghp_SECRET" ||
			r.Header.Get("Accept") != "application/vnd.github+json" ||
			r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Errorf("bad headers: %v", r.Header)
		}
		seen = append(seen, r.Header.Get("If-None-Match"))
		if r.Header.Get("If-None-Match") == `"e1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"e1"`)
		fmt.Fprint(w, `[{"number":5,"title":"release x","html_url":"u","state":"open",
			"head":{"ref":"release/x","sha":"abc","repo":{"full_name":"percona/obs-packaging"}},
			"base":{"repo":{"full_name":"percona/obs-packaging"}},
			"labels":[{"name":"obs-sync"}],"created_at":"2026-01-02T03:04:05Z"}]`)
	}))
	first, err := c.OpenPulls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.OpenPulls(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Number != 5 || first[0].HeadSHA != "abc" || first[0].HeadRef != "release/x" ||
		first[0].Labels[0] != "obs-sync" || first[0].HeadRepo != "percona/obs-packaging" {
		t.Fatalf("bad decode: %+v", first)
	}
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("304 result differs: %+v vs %+v", first, second)
	}
	if len(seen) != 2 || seen[0] != "" || seen[1] != `"e1"` {
		t.Fatalf("If-None-Match sequence: %q", seen)
	}
}

func TestGitHubErrorHidesToken(t *testing.T) {
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom ghp_SECRET", 500)
	}))
	_, err := c.OpenPulls(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "ghp_SECRET") || strings.Contains(fmt.Sprintf("%+v", err), "Bearer") {
		t.Fatal("leaks token")
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 500 {
		t.Fatal("want APIError 500")
	}
}

func TestGitHubNotFound(t *testing.T) {
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	_, err := c.File(context.Background(), "a/b.md", "main")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestGitHubMergeConflict(t *testing.T) {
	for _, code := range []int{405, 409, 422} {
		var body map[string]string
		c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "PUT" || r.URL.Path != "/repos/percona/obs-packaging/pulls/7/merge" {
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(code)
		}))
		err := c.MergePull(context.Background(), 7, "headsha")
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("code %d: got %v", code, err)
		}
		if body["merge_method"] != "rebase" || body["sha"] != "headsha" {
			t.Fatalf("body %v", body)
		}
	}
}

func TestGitHubRemoveLabelMissingIsOK(t *testing.T) {
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method %s", r.Method)
		}
		w.WriteHeader(404)
	}))
	if err := c.RemoveLabel(context.Background(), 3, "obs-sync"); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubDeleteBranchMissingIsOK(t *testing.T) {
	for _, code := range []int{404, 422, 204} {
		c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/repos/percona/obs-packaging/git/refs/heads/release/b" {
				t.Errorf("path %s", r.URL.Path)
			}
			w.WriteHeader(code)
		}))
		if err := c.DeleteBranch(context.Background(), "release/b"); err != nil {
			t.Fatalf("code %d: %v", code, err)
		}
	}
}

// amendServer fakes the git data API with one commit C1 on release/b.
type amendServer struct {
	t       *testing.T
	refSHA  string
	calls   []string
	created map[string]any
	patch   map[string]any
}

func (a *amendServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/repos/percona/obs-packaging/")
	a.calls = append(a.calls, r.Method+" "+p)
	var in map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&in)
	}
	switch {
	case r.Method == "GET" && p == "git/ref/heads/release/b":
		fmt.Fprintf(w, `{"ref":"refs/heads/release/b","object":{"sha":%q,"type":"commit"}}`, a.refSHA)
	case r.Method == "GET" && p == "git/commits/C1":
		fmt.Fprint(w, `{"sha":"C1","message":"release: x\n\nSigned-off-by: A <a@x>",
			"tree":{"sha":"T1"},"parents":[{"sha":"P"}],
			"author":{"name":"A","email":"a@x","date":"2026-01-02T03:04:05Z"}}`)
	case r.Method == "POST" && p == "git/blobs":
		raw, _ := base64.StdEncoding.DecodeString(in["content"].(string))
		if string(raw) != "new" || in["encoding"] != "base64" {
			a.t.Errorf("blob %v", in)
		}
		fmt.Fprint(w, `{"sha":"B1"}`)
	case r.Method == "POST" && p == "git/trees":
		a.created = map[string]any{"tree": in}
		fmt.Fprint(w, `{"sha":"T2"}`)
	case r.Method == "POST" && p == "git/commits":
		a.created["commit"] = in
		fmt.Fprint(w, `{"sha":"C2"}`)
	case r.Method == "PATCH" && p == "git/refs/heads/release/b":
		a.patch = in
		fmt.Fprint(w, `{}`)
	default:
		a.t.Errorf("unexpected %s %s", r.Method, p)
		http.NotFound(w, r)
	}
}

func TestGitHubAmendFile(t *testing.T) {
	a := &amendServer{t: t, refSHA: "C1"}
	c := newGHClient(t, a)
	got, err := c.AmendFile(context.Background(), "release/b", "C1", "root/ppg/releases/18/CHANGELOG.md", []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "C2" {
		t.Fatalf("sha %q", got)
	}
	tree := a.created["tree"].(map[string]any)
	if tree["base_tree"] != "T1" {
		t.Errorf("base_tree %v", tree["base_tree"])
	}
	ent := tree["tree"].([]any)[0].(map[string]any)
	if ent["path"] != "root/ppg/releases/18/CHANGELOG.md" || ent["sha"] != "B1" || ent["type"] != "blob" {
		t.Errorf("tree entry %v", ent)
	}
	cm := a.created["commit"].(map[string]any)
	if cm["message"] != "release: x\n\nSigned-off-by: A <a@x>" || cm["tree"] != "T2" {
		t.Errorf("commit %v", cm)
	}
	if ps := cm["parents"].([]any); len(ps) != 1 || ps[0] != "P" {
		t.Errorf("parents %v", ps)
	}
	au := cm["author"].(map[string]any)
	if au["name"] != "A" || au["email"] != "a@x" {
		t.Errorf("author %v", au)
	}
	if a.patch["sha"] != "C2" || a.patch["force"] != true {
		t.Errorf("patch %v", a.patch)
	}
}

func TestGitHubAmendFileStaleHead(t *testing.T) {
	a := &amendServer{t: t, refSHA: "C2"}
	c := newGHClient(t, a)
	_, err := c.AmendFile(context.Background(), "release/b", "C1", "f.md", []byte("new"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("got %v", err)
	}
	for _, call := range a.calls {
		if strings.HasPrefix(call, "POST") || strings.HasPrefix(call, "PATCH") {
			t.Fatalf("wrote despite stale head: %v", a.calls)
		}
	}
}

func TestGitHubArtifactUnzips(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("changelogs.json")
	_, _ = f.Write([]byte(`{"a":"b"}`))
	_ = zw.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/percona/obs-packaging/actions/runs/9/artifacts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "generated-changelogs" {
			t.Errorf("query %v", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"artifacts":[{"id":77,"name":"generated-changelogs"}]}`)
	})
	mux.HandleFunc("/repos/percona/obs-packaging/actions/artifacts/77/zip", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/download/77", http.StatusFound)
	})
	mux.HandleFunc("/download/77", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	})
	c := newGHClient(t, mux)
	got, err := c.Artifact(context.Background(), 9, "generated-changelogs")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"a":"b"}` {
		t.Fatalf("got %q", got)
	}
}

func TestGitHubArtifactMissing(t *testing.T) {
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"artifacts":[]}`)
	}))
	if _, err := c.Artifact(context.Background(), 9, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestGitHubPagination(t *testing.T) {
	var srvURL string
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"jobs":[{"id":2,"name":"b","status":"completed","conclusion":"success"}]}`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?per_page=100&page=2>; rel="next", <%s/last>; rel="last"`, srvURL, r.URL.Path, srvURL))
		fmt.Fprint(w, `{"jobs":[{"id":1,"name":"a","status":"completed","conclusion":"failure"}]}`)
	}))
	srvURL = c.APIBase
	jobs, err := c.Jobs(context.Background(), 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 || jobs[0].ID != 1 || jobs[1].ID != 2 || jobs[0].Conclusion != "failure" {
		t.Fatalf("jobs %+v", jobs)
	}
}

func TestGitHubMergedPullsStopsAtSince(t *testing.T) {
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "closed" || r.URL.Query().Get("sort") != "updated" {
			t.Errorf("query %v", r.URL.RawQuery)
		}
		fmt.Fprint(w, `[
		{"number":3,"merged_at":"2026-03-02T00:00:00Z","updated_at":"2026-03-02T00:00:00Z","merge_commit_sha":"m3","head":{},"base":{}},
		{"number":2,"merged_at":null,"updated_at":"2026-03-01T12:00:00Z","head":{},"base":{}},
		{"number":1,"merged_at":"2026-02-01T00:00:00Z","updated_at":"2026-02-01T00:00:00Z","head":{},"base":{}}]`)
	}))
	got, err := c.MergedPulls(context.Background(), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 3 || got[0].MergeSHA != "m3" || got[0].MergedAt == nil {
		t.Fatalf("got %+v", got)
	}
}

func TestGitHubMergedPullsOnlyMergedInWindow(t *testing.T) {
	now := time.Now().UTC()
	ts := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[
		{"number":2,"merged_at":%q,"updated_at":%q,"head":{},"base":{}},
		{"number":1,"merged_at":%q,"updated_at":%q,"head":{},"base":{}}]`,
			ts(-48*time.Hour), ts(-time.Hour), ts(-30*24*time.Hour), ts(-time.Hour))
	}))
	got, err := c.MergedPulls(context.Background(), now.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestGitHubRunsAndWorkflowFile(t *testing.T) {
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("head_sha") != "abc" {
			t.Errorf("query %v", r.URL.RawQuery)
		}
		fmt.Fprint(w, `{"workflow_runs":[{"id":4,"name":"Release PR 7","path":".github/workflows/obs-release-pr.yml","event":"pull_request","head_sha":"abc","status":"completed","conclusion":"success","html_url":"h","created_at":"2026-01-02T03:04:05Z"}]}`)
	}))
	runs, err := c.RunsForSHA(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].WorkflowFile != "obs-release-pr.yml" || runs[0].ID != 4 || runs[0].Name != "Release PR 7" {
		t.Fatalf("runs %+v", runs)
	}
}

func TestGitHubDispatchTagAndCommitPull(t *testing.T) {
	var dispatched map[string]any
	mux := http.NewServeMux()
	base := "/repos/percona/obs-packaging/"
	mux.HandleFunc(base+"actions/workflows/obs-release-cut.yml/dispatches", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&dispatched)
		w.WriteHeader(204)
	})
	mux.HandleFunc(base+"git/ref/tags/v1", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":{"type":"tag","sha":"TAGOBJ"}}`)
	})
	mux.HandleFunc(base+"git/tags/TAGOBJ", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":{"type":"commit","sha":"COMMIT"}}`)
	})
	mux.HandleFunc(base+"commits/none/pulls", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
	mux.HandleFunc(base+"commits/has/pulls", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"number":12,"html_url":"https://x/12"}]`)
	})
	c := newGHClient(t, mux)
	ctx := context.Background()
	if err := c.Dispatch(ctx, WorkflowCut, map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if dispatched["ref"] != "main" || dispatched["inputs"].(map[string]any)["k"] != "v" {
		t.Fatalf("dispatch %v", dispatched)
	}
	if sha, err := c.TagSHA(ctx, "v1"); err != nil || sha != "COMMIT" {
		t.Fatalf("tag %q %v", sha, err)
	}
	if n, u, err := c.PullForCommit(ctx, "none"); err != nil || n != 0 || u != "" {
		t.Fatalf("none %d %q %v", n, u, err)
	}
	if n, u, err := c.PullForCommit(ctx, "has"); err != nil || n != 12 || u != "https://x/12" {
		t.Fatalf("has %d %q %v", n, u, err)
	}
}

func TestGitHubFileDecodesContent(t *testing.T) {
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/percona/obs-packaging/contents/docs/a b.json" || r.URL.Query().Get("ref") != "main" {
			t.Errorf("url %s", r.URL.String())
		}
		fmt.Fprintf(w, `{"encoding":"base64","content":%q}`, base64.StdEncoding.EncodeToString([]byte("hello"))+"\n")
	}))
	got, err := c.File(context.Background(), "docs/a b.json", "main")
	if err != nil || string(got) != "hello" {
		t.Fatalf("%q %v", got, err)
	}
}

// TestGitHubPaginationSurvives304 checks that a conditional request answered
// with 304 and no Link header still follows the cached Link to page 2.
func TestGitHubPaginationSurvives304(t *testing.T) {
	var srvURL string
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		etag := `"p1"`
		body := `{"jobs":[{"id":1,"name":"a","status":"completed","conclusion":"failure"}]}`
		if page == "2" {
			etag = `"p2"`
			body = `{"jobs":[{"id":2,"name":"b","status":"completed","conclusion":"success"}]}`
		}
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified) // deliberately no Link header
			return
		}
		w.Header().Set("ETag", etag)
		if page != "2" {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?per_page=100&page=2>; rel="next"`, srvURL, r.URL.Path))
		}
		fmt.Fprint(w, body)
	}))
	srvURL = c.APIBase
	first, err := c.Jobs(context.Background(), 11)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Jobs(context.Background(), 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("first %+v second %+v", first, second)
	}
}

// TestGitHubForeignNextLinkRefused checks that a next link pointing at another
// host is never requested. The code returns page 1's items and no error.
func TestGitHubForeignNextLinkRefused(t *testing.T) {
	var called bool
	var mu sync.Mutex
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		called = true
		mu.Unlock()
		fmt.Fprint(w, `{"jobs":[]}`)
	}))
	defer foreign.Close()
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", fmt.Sprintf(`<%s/jobs?page=2>; rel="next"`, foreign.URL))
		fmt.Fprint(w, `{"jobs":[{"id":1,"name":"a","status":"completed","conclusion":"failure"}]}`)
	}))
	jobs, err := c.Jobs(context.Background(), 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("jobs %+v", jobs)
	}
	mu.Lock()
	defer mu.Unlock()
	if called {
		t.Fatal("request sent to foreign host")
	}
}

func TestGitHubHasClosedPull(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{{`[{"number":9,"state":"closed"}]`, true}, {`[]`, false}} {
		c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if r.URL.Path != "/repos/percona/obs-packaging/pulls" || q.Get("state") != "closed" ||
				q.Get("head") != "percona:release/b" || q.Get("per_page") != "1" {
				t.Errorf("request %s?%s", r.URL.Path, r.URL.RawQuery)
			}
			fmt.Fprint(w, tc.body)
		}))
		got, err := c.HasClosedPull(context.Background(), "release/b")
		if err != nil || got != tc.want {
			t.Errorf("body %s: got %v, %v; want %v", tc.body, got, err, tc.want)
		}
	}
}

func TestGitHubFileUnusable(t *testing.T) {
	bodies := map[string]string{
		"directory":  `[{"type":"file","name":"a"},{"type":"file","name":"b"}]`,
		"symlink":    `{"type":"symlink","target":"../x","encoding":"base64","content":""}`,
		"submodule":  `{"type":"submodule","submodule_git_url":"git://x"}`,
		"too large":  `{"type":"file","encoding":"none","content":"","size":2000000}`,
		"bad base64": `{"type":"file","encoding":"base64","content":"!!!"}`,
	}
	for name, body := range bodies {
		c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		_, err := c.File(context.Background(), "root/x/CHANGELOG.md", "main")
		if !errors.Is(err, ErrUnusable) {
			t.Errorf("%s: err = %v, want ErrUnusable", name, err)
		}
		if errors.Is(err, ErrNotFound) {
			t.Errorf("%s: must not read as not found", name)
		}
	}
}

func TestGitHubETagCacheBounded(t *testing.T) {
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"`+r.URL.Path+`"`)
		fmt.Fprint(w, `[]`)
	}))
	ctx := context.Background()
	for i := 0; i < maxCacheEntries+25; i++ {
		if _, err := c.Jobs(ctx, int64(i)); err == nil {
			t.Fatal("want a decode error for a bare list") // the body is not a jobs object
		}
		// Keep the first entry in use: it must survive the eviction.
		if _, _, err := c.do(ctx, http.MethodGet, c.repoPath("actions/runs/0/jobs?per_page=100"), nil, true); err != nil {
			t.Fatal(err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) != maxCacheEntries {
		t.Fatalf("cache holds %d entries, want %d", len(c.cache), maxCacheEntries)
	}
	if _, ok := c.cache[c.base()+c.repoPath("actions/runs/0/jobs?per_page=100")]; !ok {
		t.Error("the entry in constant use was evicted")
	}
	if _, ok := c.cache[c.base()+c.repoPath("actions/runs/1/jobs?per_page=100")]; ok {
		t.Error("the oldest unused entry was kept")
	}
}

func TestGitHubRunActorAndApprove(t *testing.T) {
	var approved string
	mux := http.NewServeMux()
	base := "/repos/percona/obs-packaging/"
	mux.HandleFunc(base+"actions/runs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"workflow_runs":[{"id":7,"event":"pull_request","actor":{"login":"github-actions[bot]"},"conclusion":"action_required"}]}`)
	})
	mux.HandleFunc(base+"actions/runs/7/approve", func(w http.ResponseWriter, r *http.Request) {
		approved = r.Method
		w.WriteHeader(201)
		fmt.Fprint(w, `{}`)
	})
	c := newGHClient(t, mux)
	ctx := context.Background()
	runs, err := c.RunsForSHA(ctx, "h")
	if err != nil || len(runs) != 1 || runs[0].Actor != ActorActions {
		t.Fatalf("runs %+v %v", runs, err)
	}
	if err := c.ApproveRun(ctx, 7); err != nil || approved != http.MethodPost {
		t.Fatalf("approve %q %v", approved, err)
	}
}

func TestClientComment(t *testing.T) {
	var body map[string]string
	c := newGHClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/repos/percona/obs-packaging/issues/7/comments" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	if err := c.Comment(context.Background(), 7, "hello"); err != nil {
		t.Fatal(err)
	}
	if body["body"] != "hello" {
		t.Fatalf("body %v", body)
	}
}
