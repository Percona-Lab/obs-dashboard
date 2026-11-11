package release

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("github: not found")
	ErrConflict = errors.New("github: conflict")
)

// APIError is a failed GitHub call. It carries method, path (no query) and
// status only, so it is safe to log and to show: never a header, token or
// response body.
type APIError struct {
	Method, Path string
	Status       int
	kind         error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github %s %s: HTTP %d", e.Method, e.Path, e.Status)
}

// Unwrap exposes ErrNotFound / ErrConflict where the status maps to one.
func (e *APIError) Unwrap() error { return e.kind }

type Pull struct {
	Number                                               int
	Title, HTMLURL, HeadRef, HeadSHA, HeadRepo, BaseRepo string
	Labels                                               []string
	State                                                string
	MergedAt                                             *time.Time
	MergeSHA                                             string
	CreatedAt                                            time.Time
}

type Ref struct{ Name, SHA string } // Name without refs/heads/

type Run struct {
	ID                                                              int64
	Name, WorkflowFile, Event, HeadSHA, Status, Conclusion, HTMLURL string
	CreatedAt                                                       time.Time
}

type Job struct {
	ID                                int64
	Name, Status, Conclusion, HTMLURL string
	StartedAt, CompletedAt            *time.Time
}

type Annotation struct{ Level, Title, Message string } // Level: notice|warning|failure

type GHRelease struct {
	Tag, Body, HTMLURL string
	PublishedAt        time.Time
}

// GitHub is every GitHub call the release feature makes.
type GitHub interface {
	OpenPulls(ctx context.Context) ([]Pull, error)
	MergedPulls(ctx context.Context, since time.Time) ([]Pull, error)
	PullFiles(ctx context.Context, number int) ([]string, error)
	CreatePull(ctx context.Context, head, title, body string) (Pull, error)
	ClosePull(ctx context.Context, number int) error
	MergePull(ctx context.Context, number int, headSHA string) error
	AddLabel(ctx context.Context, number int, label string) error
	RemoveLabel(ctx context.Context, number int, label string) error
	Branches(ctx context.Context, prefix string) ([]Ref, error)
	DeleteBranch(ctx context.Context, branch string) error
	File(ctx context.Context, path, ref string) ([]byte, error)
	AmendFile(ctx context.Context, branch, expectHead, path string, content []byte) (string, error)
	Dispatch(ctx context.Context, workflow string, inputs map[string]string) error
	WorkflowRuns(ctx context.Context, workflow string, limit int) ([]Run, error)
	RunsForSHA(ctx context.Context, sha string) ([]Run, error)
	Jobs(ctx context.Context, runID int64) ([]Job, error)
	Annotations(ctx context.Context, jobID int64) ([]Annotation, error)
	Artifact(ctx context.Context, runID int64, name string) ([]byte, error)
	Releases(ctx context.Context, page, perPage int) ([]GHRelease, error)
	TagSHA(ctx context.Context, tag string) (string, error)
	PullForCommit(ctx context.Context, sha string) (int, string, error)
}

// Client talks to the GitHub REST API with a bearer token. It implements GitHub.
type Client struct {
	APIBase string // default https://api.github.com
	Repo    string // owner/name
	Token   string
	HTTP    *http.Client

	mu    sync.Mutex
	cache map[string]cachedBody
}

var _ GitHub = (*Client)(nil)

type cachedBody struct {
	etag string
	body []byte
}

const (
	maxJSONBody     = 8 << 20
	maxCacheBody    = 2 << 20
	maxArtifactFile = 4 << 20
	maxArtifactZip  = 16 << 20
	maxPages        = 50
)

func (c *Client) base() string {
	if c.APIBase == "" {
		return "https://api.github.com"
	}
	return strings.TrimRight(c.APIBase, "/")
}

func (c *Client) repoPath(rest string) string { return "/repos/" + c.Repo + "/" + rest }

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// do performs one request. target is a path under APIBase or an absolute URL
// on APIBase. GETs with cache=true use the ETag cache. It returns the response
// headers and body; non-2xx statuses become *APIError.
func (c *Client) do(ctx context.Context, method, target string, body any, cache bool) (http.Header, []byte, error) {
	full := target
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		full = c.base() + target
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, full, rdr)
	if err != nil {
		return nil, nil, fmt.Errorf("github %s: bad request", method)
	}
	safePath := req.URL.Path
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	useCache := cache && method == http.MethodGet
	if useCache {
		c.mu.Lock()
		if e, ok := c.cache[full]; ok {
			req.Header.Set("If-None-Match", e.etag)
		}
		c.mu.Unlock()
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		// The transport error may embed the URL; keep method and path only.
		return nil, nil, fmt.Errorf("github %s %s: request failed", method, safePath)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtifactZip+1))
	if err != nil {
		return nil, nil, fmt.Errorf("github %s %s: read failed", method, safePath)
	}
	if resp.StatusCode == http.StatusNotModified && useCache {
		c.mu.Lock()
		e, ok := c.cache[full]
		c.mu.Unlock()
		if ok {
			return resp.Header, e.body, nil
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{Method: method, Path: safePath, Status: resp.StatusCode}
		if resp.StatusCode == http.StatusNotFound {
			ae.kind = ErrNotFound
		}
		return nil, nil, ae
	}
	if useCache {
		if et := resp.Header.Get("ETag"); et != "" && len(data) <= maxCacheBody {
			c.mu.Lock()
			if c.cache == nil {
				c.cache = map[string]cachedBody{}
			}
			c.cache[full] = cachedBody{etag: et, body: data}
			c.mu.Unlock()
		}
	}
	return resp.Header, data, nil
}

func (c *Client) getJSON(ctx context.Context, p string, out any) error {
	_, data, err := c.do(ctx, http.MethodGet, p, nil, true)
	if err != nil {
		return err
	}
	if len(data) > maxJSONBody {
		return fmt.Errorf("github GET %s: response too large", p)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("github GET %s: decode failed", pathOnly(p))
	}
	return nil
}

func pathOnly(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		return p[:i]
	}
	return p
}

// conflictOn turns an APIError with one of the statuses into ErrConflict.
func conflictOn(err error, statuses ...int) error {
	var ae *APIError
	if errors.As(err, &ae) {
		for _, s := range statuses {
			if ae.Status == s {
				ae.kind = ErrConflict
			}
		}
	}
	return err
}

var linkNextRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

// pages follows Link rel="next" from first, decoding each page with decode and
// feeding items to each until it returns false, limit items were seen, or no
// next page remains. Next links outside APIBase are not followed.
func pages[T any](ctx context.Context, c *Client, first string, limit int, decode func([]byte) ([]T, error), each func(T) bool) error {
	next := first
	seen := 0
	for i := 0; next != "" && i < maxPages; i++ {
		h, data, err := c.do(ctx, http.MethodGet, next, nil, true)
		if err != nil {
			return err
		}
		items, err := decode(data)
		if err != nil {
			return fmt.Errorf("github GET %s: decode failed", pathOnly(next))
		}
		for _, it := range items {
			if limit > 0 && seen >= limit {
				return nil
			}
			seen++
			if !each(it) {
				return nil
			}
		}
		next = ""
		if m := linkNextRe.FindStringSubmatch(h.Get("Link")); m != nil && strings.HasPrefix(m[1], c.base()+"/") {
			next = m[1]
		}
	}
	return nil
}

func decodeArray[T any](data []byte) ([]T, error) {
	var out []T
	err := json.Unmarshal(data, &out)
	return out, err
}

func decodeWrapped[T any](key string) func([]byte) ([]T, error) {
	return func(data []byte) ([]T, error) {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		var out []T
		if raw, ok := m[key]; ok {
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
}

func collect[T any](ctx context.Context, c *Client, first string, limit int, decode func([]byte) ([]T, error)) ([]T, error) {
	var out []T
	err := pages(ctx, c, first, limit, decode, func(t T) bool { out = append(out, t); return true })
	return out, err
}

// ---- pulls ----

type ghPull struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	HTMLURL   string     `json:"html_url"`
	State     string     `json:"state"`
	MergedAt  *time.Time `json:"merged_at"`
	MergeSHA  string     `json:"merge_commit_sha"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Head struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
}

func (g ghPull) pull() Pull {
	p := Pull{
		Number: g.Number, Title: g.Title, HTMLURL: g.HTMLURL, State: g.State,
		HeadRef: g.Head.Ref, HeadSHA: g.Head.SHA, HeadRepo: g.Head.Repo.FullName,
		BaseRepo: g.Base.Repo.FullName, MergeSHA: g.MergeSHA, CreatedAt: g.CreatedAt,
	}
	if g.MergedAt != nil {
		t := g.MergedAt.UTC()
		p.MergedAt = &t
	}
	for _, l := range g.Labels {
		p.Labels = append(p.Labels, l.Name)
	}
	return p
}

func (c *Client) OpenPulls(ctx context.Context) ([]Pull, error) {
	raw, err := collect(ctx, c, c.repoPath("pulls?state=open&per_page=100"), 1000, decodeArray[ghPull])
	if err != nil {
		return nil, err
	}
	out := make([]Pull, 0, len(raw))
	for _, g := range raw {
		out = append(out, g.pull())
	}
	return out, nil
}

// MergedPulls lists merged PRs updated at or after since, newest first.
func (c *Client) MergedPulls(ctx context.Context, since time.Time) ([]Pull, error) {
	var out []Pull
	err := pages(ctx, c, c.repoPath("pulls?state=closed&sort=updated&direction=desc&per_page=100"), 0,
		decodeArray[ghPull], func(g ghPull) bool {
			if g.UpdatedAt.Before(since) {
				return false
			}
			if g.MergedAt != nil {
				out = append(out, g.pull())
			}
			return true
		})
	return out, err
}

func (c *Client) PullFiles(ctx context.Context, number int) ([]string, error) {
	type f struct {
		Filename string `json:"filename"`
	}
	fs, err := collect(ctx, c, c.repoPath("pulls/"+strconv.Itoa(number)+"/files?per_page=100"), 3000, decodeArray[f])
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(fs))
	for _, x := range fs {
		out = append(out, x.Filename)
	}
	return out, nil
}

func (c *Client) CreatePull(ctx context.Context, head, title, body string) (Pull, error) {
	_, data, err := c.do(ctx, http.MethodPost, c.repoPath("pulls"),
		map[string]string{"head": head, "base": "main", "title": title, "body": body}, false)
	if err != nil {
		return Pull{}, err
	}
	var g ghPull
	if err := json.Unmarshal(data, &g); err != nil {
		return Pull{}, errors.New("github POST pulls: decode failed")
	}
	return g.pull(), nil
}

func (c *Client) ClosePull(ctx context.Context, number int) error {
	_, _, err := c.do(ctx, http.MethodPatch, c.repoPath("pulls/"+strconv.Itoa(number)), map[string]string{"state": "closed"}, false)
	return err
}

func (c *Client) MergePull(ctx context.Context, number int, headSHA string) error {
	_, _, err := c.do(ctx, http.MethodPut, c.repoPath("pulls/"+strconv.Itoa(number)+"/merge"),
		map[string]string{"merge_method": "rebase", "sha": headSHA}, false)
	return conflictOn(err, 405, 409, 422)
}

func (c *Client) AddLabel(ctx context.Context, number int, label string) error {
	_, _, err := c.do(ctx, http.MethodPost, c.repoPath("issues/"+strconv.Itoa(number)+"/labels"),
		map[string][]string{"labels": {label}}, false)
	return err
}

func (c *Client) RemoveLabel(ctx context.Context, number int, label string) error {
	_, _, err := c.do(ctx, http.MethodDelete, c.repoPath("issues/"+strconv.Itoa(number)+"/labels/"+url.PathEscape(label)), nil, false)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ---- branches, files ----

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func (c *Client) Branches(ctx context.Context, prefix string) ([]Ref, error) {
	type r struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	rs, err := collect(ctx, c, c.repoPath("git/matching-refs/heads/"+escapePath(prefix)+"?per_page=100"), 1000, decodeArray[r])
	if err != nil {
		return nil, err
	}
	out := make([]Ref, 0, len(rs))
	for _, x := range rs {
		out = append(out, Ref{Name: strings.TrimPrefix(x.Ref, "refs/heads/"), SHA: x.Object.SHA})
	}
	return out, nil
}

func (c *Client) DeleteBranch(ctx context.Context, branch string) error {
	_, _, err := c.do(ctx, http.MethodDelete, c.repoPath("git/refs/heads/"+escapePath(branch)), nil, false)
	var ae *APIError
	if errors.As(err, &ae) && (ae.Status == 404 || ae.Status == 422) {
		return nil
	}
	return err
}

func (c *Client) File(ctx context.Context, p, ref string) ([]byte, error) {
	var f struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := c.getJSON(ctx, c.repoPath("contents/"+escapePath(p)+"?ref="+url.QueryEscape(ref)), &f); err != nil {
		return nil, err
	}
	if f.Encoding != "base64" {
		return nil, fmt.Errorf("github contents %s: unsupported encoding", p)
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("github contents %s: bad content", p)
	}
	return b, nil
}

type ghPerson struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

// AmendFile replaces one file in the head commit of branch: a new commit with
// the same message, author and parents but a changed tree, then a force update
// of the ref. It writes nothing unless the branch head is still expectHead.
func (c *Client) AmendFile(ctx context.Context, branch, expectHead, p string, content []byte) (string, error) {
	refPath := "git/refs/heads/" + escapePath(branch)
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.getJSON(ctx, c.repoPath("git/ref/heads/"+escapePath(branch)), &ref); err != nil {
		return "", err
	}
	if ref.Object.SHA != expectHead {
		return "", &APIError{Method: http.MethodGet, Path: c.repoPath("git/ref/heads/" + branch), Status: http.StatusConflict, kind: ErrConflict}
	}
	var cm struct {
		Message string `json:"message"`
		Tree    struct {
			SHA string `json:"sha"`
		} `json:"tree"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
		Author ghPerson `json:"author"`
	}
	if err := c.getJSON(ctx, c.repoPath("git/commits/"+expectHead), &cm); err != nil {
		return "", err
	}
	var sha struct {
		SHA string `json:"sha"`
	}
	post := func(rest string, body any) (string, error) {
		_, data, err := c.do(ctx, http.MethodPost, c.repoPath(rest), body, false)
		if err != nil {
			return "", err
		}
		sha.SHA = ""
		if err := json.Unmarshal(data, &sha); err != nil || sha.SHA == "" {
			return "", fmt.Errorf("github POST %s: decode failed", c.repoPath(rest))
		}
		return sha.SHA, nil
	}
	blob, err := post("git/blobs", map[string]string{"content": base64.StdEncoding.EncodeToString(content), "encoding": "base64"})
	if err != nil {
		return "", err
	}
	tree, err := post("git/trees", map[string]any{
		"base_tree": cm.Tree.SHA,
		"tree":      []map[string]string{{"path": p, "mode": "100644", "type": "blob", "sha": blob}},
	})
	if err != nil {
		return "", err
	}
	parents := make([]string, 0, len(cm.Parents))
	for _, pr := range cm.Parents {
		parents = append(parents, pr.SHA)
	}
	commit, err := post("git/commits", map[string]any{
		"message": cm.Message, "tree": tree, "parents": parents, "author": cm.Author,
	})
	if err != nil {
		return "", err
	}
	_, _, err = c.do(ctx, http.MethodPatch, c.repoPath(refPath), map[string]any{"sha": commit, "force": true}, false)
	if err != nil {
		return "", conflictOn(err, 409, 422)
	}
	return commit, nil
}

// ---- workflows ----

func (c *Client) Dispatch(ctx context.Context, workflow string, inputs map[string]string) error {
	body := map[string]any{"ref": "main"}
	if len(inputs) > 0 {
		body["inputs"] = inputs
	}
	_, _, err := c.do(ctx, http.MethodPost, c.repoPath("actions/workflows/"+url.PathEscape(workflow)+"/dispatches"), body, false)
	return err
}

type ghRun struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	DisplayTitle string    `json:"display_title"`
	Path         string    `json:"path"`
	Event        string    `json:"event"`
	HeadSHA      string    `json:"head_sha"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	HTMLURL      string    `json:"html_url"`
	CreatedAt    time.Time `json:"created_at"`
}

func (g ghRun) run() Run {
	name := g.DisplayTitle
	if name == "" {
		name = g.Name
	}
	return Run{
		ID: g.ID, Name: name, WorkflowFile: path.Base(g.Path), Event: g.Event, HeadSHA: g.HeadSHA,
		Status: g.Status, Conclusion: g.Conclusion, HTMLURL: g.HTMLURL, CreatedAt: g.CreatedAt,
	}
}

func (c *Client) runs(ctx context.Context, first string, limit int) ([]Run, error) {
	raw, err := collect(ctx, c, first, limit, decodeWrapped[ghRun]("workflow_runs"))
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(raw))
	for _, g := range raw {
		out = append(out, g.run())
	}
	return out, nil
}

func (c *Client) WorkflowRuns(ctx context.Context, workflow string, limit int) ([]Run, error) {
	per := limit
	if per <= 0 || per > 100 {
		per = 100
	}
	return c.runs(ctx, c.repoPath("actions/workflows/"+url.PathEscape(workflow)+"/runs?per_page="+strconv.Itoa(per)), limit)
}

func (c *Client) RunsForSHA(ctx context.Context, sha string) ([]Run, error) {
	return c.runs(ctx, c.repoPath("actions/runs?per_page=100&head_sha="+url.QueryEscape(sha)), 500)
}

func (c *Client) Jobs(ctx context.Context, runID int64) ([]Job, error) {
	type j struct {
		ID          int64      `json:"id"`
		Name        string     `json:"name"`
		Status      string     `json:"status"`
		Conclusion  string     `json:"conclusion"`
		HTMLURL     string     `json:"html_url"`
		StartedAt   *time.Time `json:"started_at"`
		CompletedAt *time.Time `json:"completed_at"`
	}
	raw, err := collect(ctx, c, c.repoPath("actions/runs/"+strconv.FormatInt(runID, 10)+"/jobs?per_page=100"), 1000, decodeWrapped[j]("jobs"))
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(raw))
	for _, x := range raw {
		out = append(out, Job{ID: x.ID, Name: x.Name, Status: x.Status, Conclusion: x.Conclusion,
			HTMLURL: x.HTMLURL, StartedAt: x.StartedAt, CompletedAt: x.CompletedAt})
	}
	return out, nil
}

func (c *Client) Annotations(ctx context.Context, jobID int64) ([]Annotation, error) {
	type a struct {
		Level   string `json:"annotation_level"`
		Title   string `json:"title"`
		Message string `json:"message"`
	}
	raw, err := collect(ctx, c, c.repoPath("check-runs/"+strconv.FormatInt(jobID, 10)+"/annotations?per_page=100"), 500, decodeArray[a])
	if err != nil {
		return nil, err
	}
	out := make([]Annotation, 0, len(raw))
	for _, x := range raw {
		out = append(out, Annotation{Level: x.Level, Title: x.Title, Message: x.Message})
	}
	return out, nil
}

// Artifact returns the first file inside the named artifact of a run.
func (c *Client) Artifact(ctx context.Context, runID int64, name string) ([]byte, error) {
	var list struct {
		Artifacts []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"artifacts"`
	}
	if err := c.getJSON(ctx, c.repoPath("actions/runs/"+strconv.FormatInt(runID, 10)+"/artifacts?name="+url.QueryEscape(name)), &list); err != nil {
		return nil, err
	}
	var id int64
	for _, a := range list.Artifacts {
		if a.Name == name {
			id = a.ID
			break
		}
	}
	if id == 0 {
		return nil, &APIError{Method: http.MethodGet, Path: c.repoPath("actions/runs/" + strconv.FormatInt(runID, 10) + "/artifacts"), Status: http.StatusNotFound, kind: ErrNotFound}
	}
	_, data, err := c.do(ctx, http.MethodGet, c.repoPath("actions/artifacts/"+strconv.FormatInt(id, 10)+"/zip"), nil, false)
	if err != nil {
		return nil, err
	}
	if len(data) > maxArtifactZip {
		return nil, errors.New("github artifact: archive too large")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("github artifact: not a zip archive")
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, errors.New("github artifact: cannot open file")
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, maxArtifactFile+1))
		if err != nil {
			return nil, errors.New("github artifact: cannot read file")
		}
		if len(b) > maxArtifactFile {
			return nil, errors.New("github artifact: file too large")
		}
		return b, nil
	}
	return nil, errors.New("github artifact: archive is empty")
}

// ---- releases, tags, commits ----

func (c *Client) Releases(ctx context.Context, page, perPage int) ([]GHRelease, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 30
	}
	var raw []struct {
		Tag         string     `json:"tag_name"`
		Body        string     `json:"body"`
		HTMLURL     string     `json:"html_url"`
		PublishedAt *time.Time `json:"published_at"`
	}
	if err := c.getJSON(ctx, c.repoPath(fmt.Sprintf("releases?per_page=%d&page=%d", perPage, page)), &raw); err != nil {
		return nil, err
	}
	out := make([]GHRelease, 0, len(raw))
	for _, r := range raw {
		g := GHRelease{Tag: r.Tag, Body: r.Body, HTMLURL: r.HTMLURL}
		if r.PublishedAt != nil {
			g.PublishedAt = r.PublishedAt.UTC()
		}
		out = append(out, g)
	}
	return out, nil
}

// TagSHA resolves tag to a commit sha, peeling annotated tags.
func (c *Client) TagSHA(ctx context.Context, tag string) (string, error) {
	var ref struct {
		Object struct{ Type, SHA string } `json:"object"`
	}
	if err := c.getJSON(ctx, c.repoPath("git/ref/tags/"+escapePath(tag)), &ref); err != nil {
		return "", err
	}
	obj := ref.Object
	for i := 0; obj.Type == "tag" && i < 5; i++ {
		var t struct {
			Object struct{ Type, SHA string } `json:"object"`
		}
		if err := c.getJSON(ctx, c.repoPath("git/tags/"+url.PathEscape(obj.SHA)), &t); err != nil {
			return "", err
		}
		obj = t.Object
	}
	return obj.SHA, nil
}

// PullForCommit returns the PR number and URL that contains sha, or 0 when none.
func (c *Client) PullForCommit(ctx context.Context, sha string) (int, string, error) {
	var ps []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := c.getJSON(ctx, c.repoPath("commits/"+url.PathEscape(sha)+"/pulls"), &ps); err != nil {
		return 0, "", err
	}
	if len(ps) == 0 {
		return 0, "", nil
	}
	return ps[0].Number, ps[0].HTMLURL, nil
}
