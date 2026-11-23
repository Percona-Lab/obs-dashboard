package release

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Authentication modes of the Release tab.
const (
	ModeApp   = "app"
	ModeToken = "token"
	ModeOff   = "off"
)

type permRequirement struct{ name, level string }

// requiredPermissions is the App permission set the Release tab needs, in the
// order the Settings card lists it.
var requiredPermissions = []permRequirement{
	{"contents", "write"},
	{"pull_requests", "write"},
	{"actions", "write"},
	{"checks", "read"},
	{"metadata", "read"},
}

func permRank(level string) int {
	switch level {
	case "read":
		return 1
	case "write":
		return 2
	case "admin":
		return 3
	}
	return 0
}

// Connection decides how the Release tab authenticates to GitHub, supplies the
// client's token source and reports the state of the connection.
type Connection struct {
	APIBase string           // passed on to the AppSource and used for the token-mode call
	HTTP    *http.Client     // default: 30 s timeout
	Now     func() time.Time // default time.Now

	mode string
	repo string
	app  *AppSource
	pat  string

	mu sync.Mutex // guards copying the settings onto app
}

// NewConnection picks the mode: app when appID or keyB64 is non-empty (even if
// only one is, or the key is unusable), else token when pat is non-empty, else
// off. Inputs are trimmed.
func NewConnection(repo, appID, keyB64, pat string) *Connection {
	appID, keyB64, pat = strings.TrimSpace(appID), strings.TrimSpace(keyB64), strings.TrimSpace(pat)
	c := &Connection{repo: repo, pat: pat}
	switch {
	case appID != "" || keyB64 != "":
		c.mode = ModeApp
		c.app = NewAppSource(repo, appID, keyB64)
	case pat != "":
		c.mode = ModeToken
	default:
		c.mode = ModeOff
	}
	return c
}

func (c *Connection) Mode() string { return c.mode }

// ConfigErr is the AppSource's configuration fault in app mode, else nil.
func (c *Connection) ConfigErr() error {
	if c.app == nil {
		return nil
	}
	return c.app.ConfigErr()
}

// syncApp passes the settings on to the AppSource. It writes only on a change,
// so concurrent callers do not race with a running source.
func (c *Connection) syncApp() {
	if c.app == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.app.APIBase != c.APIBase {
		c.app.APIBase = c.APIBase
	}
	if c.app.HTTP != c.HTTP {
		c.app.HTTP = c.HTTP
	}
	// Func values are not comparable; the source's clock is set once.
	if c.app.Now == nil && c.Now != nil {
		c.app.Now = c.Now
	}
}

// TokenSource is the *AppSource in app mode, StaticToken in token mode, nil
// when off.
func (c *Connection) TokenSource() TokenSource {
	switch c.mode {
	case ModeApp:
		c.syncApp()
		return c.app
	case ModeToken:
		return StaticToken(c.pat)
	}
	return nil
}

func (c *Connection) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Connection) base() string {
	if c.APIBase != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	return "https://api.github.com"
}

func (c *Connection) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// ConnStatus is what the Settings card shows. It holds no secret.
type ConnStatus struct {
	Mode           string           `json:"mode"`
	Repo           string           `json:"repo"`
	OK             bool             `json:"ok"`
	Error          string           `json:"error"`
	FailedStep     string           `json:"failed_step"`
	App            *ConnApp         `json:"app,omitempty"`
	InstallationID int64            `json:"installation_id,omitempty"`
	Permissions    []ConnPermission `json:"permissions,omitempty"`
	Token          *ConnToken       `json:"token,omitempty"`
	CheckedAt      time.Time        `json:"checked_at"`
}

type ConnApp struct {
	ID   string `json:"id"` // GitHub's numeric ID, as a string
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type ConnPermission struct {
	Name     string `json:"name"`
	Required string `json:"required"`
	Granted  string `json:"granted"` // "none" when GitHub does not list it
	OK       bool   `json:"ok"`
}

type ConnToken struct {
	MintedAt    *time.Time `json:"minted_at"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastError   string     `json:"last_error"`
	LastErrorAt *time.Time `json:"last_error_at"`
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

// Check probes the connection live, stopping at the first failing step.
func (c *Connection) Check(ctx context.Context) ConnStatus {
	st := ConnStatus{Mode: c.mode, Repo: c.repo, CheckedAt: c.now().UTC()}
	switch c.mode {
	case ModeApp:
		c.checkApp(ctx, &st)
	case ModeToken:
		c.checkToken(ctx, &st)
	}
	return st
}

func (st *ConnStatus) fail(step string, err error) {
	st.OK, st.FailedStep, st.Error = false, step, err.Error()
}

func (c *Connection) checkApp(ctx context.Context, st *ConnStatus) {
	if err := c.app.ConfigErr(); err != nil {
		st.fail("config", err)
		return
	}
	c.syncApp()
	// The token section is filled whatever happens below.
	defer func() {
		ts := c.app.TokenState()
		st.Token = &ConnToken{
			MintedAt:    timePtr(ts.MintedAt),
			ExpiresAt:   timePtr(ts.ExpiresAt),
			LastError:   ts.LastError,
			LastErrorAt: timePtr(ts.LastErrorAt),
		}
	}()
	info, err := c.app.App(ctx)
	if err != nil {
		st.fail("app", err)
		return
	}
	st.App = &ConnApp{ID: fmt.Sprint(info.ID), Slug: info.Slug, Name: info.Name}
	inst, err := c.app.Installation(ctx)
	if err != nil {
		st.fail("installation", err)
		return
	}
	st.InstallationID = inst.ID
	for _, req := range requiredPermissions {
		granted, ok := inst.Permissions[req.name]
		if !ok || granted == "" {
			granted = "none"
		}
		st.Permissions = append(st.Permissions, ConnPermission{
			Name: req.name, Required: req.level, Granted: granted,
			OK: permRank(granted) >= permRank(req.level),
		})
	}
	if _, err := c.app.FreshToken(ctx); err != nil {
		st.fail("token", err)
		return
	}
	st.OK = true
}

func (c *Connection) checkToken(ctx context.Context, st *ConnStatus) {
	p := "/repos/" + c.repo
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+p, nil)
	if err != nil {
		st.fail("repository", fmt.Errorf("github GET %s: bad request", p))
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.pat)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		// The transport error may embed the URL; keep method and path only.
		st.fail("repository", fmt.Errorf("github GET %s: request failed", p))
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxAppReplySize))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		ae := &APIError{Method: http.MethodGet, Path: p, Status: resp.StatusCode}
		if resp.StatusCode == http.StatusNotFound {
			ae.kind = ErrNotFound
		}
		st.fail("repository", ae)
		return
	}
	st.OK = true
}
