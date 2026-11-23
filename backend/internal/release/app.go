package release

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// ErrAppConfig marks a GitHub App configuration fault (missing or unusable ID
// or key). Its messages never contain the key.
var ErrAppConfig = errors.New("github app: bad configuration")

type appConfigError struct{ msg string }

func (e *appConfigError) Error() string { return e.msg }
func (e *appConfigError) Unwrap() error { return ErrAppConfig }

const (
	appJWTBackdate  = 60 * time.Second // iat: tolerate clock skew
	appJWTLifetime  = 9 * time.Minute  // exp: GitHub allows at most 10
	appTokenMargin  = 5 * time.Minute  // renew this long before expires_at
	appFailureTTL   = 30 * time.Second // a failed lookup or mint is remembered
	maxAppReplySize = 1 << 20
)

// AppInfo is the App as GitHub reports it for GET /app.
type AppInfo struct {
	ID         int64
	Slug, Name string
}

// Installation is the App's installation on the repository.
type Installation struct {
	ID          int64
	Permissions map[string]string // GitHub's "permissions" object
}

// TokenState describes the cached installation token. It holds no secret.
type TokenState struct {
	MintedAt, ExpiresAt time.Time // zero when no token is cached
	LastError           string    // sanitised; "" when the last mint succeeded
	LastErrorAt         time.Time
}

// AppSource is a TokenSource that mints installation tokens for a GitHub App
// on one repository and caches them.
type AppSource struct {
	APIBase string           // default https://api.github.com
	HTTP    *http.Client     // default: 30 s timeout
	Now     func() time.Time // default time.Now

	repo   string
	appID  string
	key    *rsa.PrivateKey
	cfgErr error

	mu        sync.Mutex // held across lookup and mint: one mint for concurrent callers
	installID int64
	token     string
	mintedAt  time.Time
	expiresAt time.Time
	lastErr   error
	lastErrAt time.Time
	failUntil time.Time
}

var _ TokenSource = (*AppSource)(nil)

// NewAppSource never fails: a configuration fault is kept and returned by
// every call. appID and keyB64 are trimmed of surrounding whitespace.
func NewAppSource(repo, appID, keyB64 string) *AppSource {
	a := &AppSource{repo: repo, appID: strings.TrimSpace(appID)}
	keyB64 = strings.TrimSpace(keyB64)
	switch {
	case a.appID == "":
		a.cfgErr = &appConfigError{"RELEASE_GITHUB_APP_ID is not set"}
	case keyB64 == "":
		a.cfgErr = &appConfigError{"RELEASE_GITHUB_APP_PRIVATE_KEY is not set"}
	default:
		raw, err := base64.StdEncoding.DecodeString(keyB64)
		if err != nil {
			a.cfgErr = &appConfigError{"RELEASE_GITHUB_APP_PRIVATE_KEY is not valid base64"}
			break
		}
		if a.key = parseRSAKey(raw); a.key == nil {
			a.cfgErr = &appConfigError{"RELEASE_GITHUB_APP_PRIVATE_KEY is not an RSA private key in PEM form"}
		}
	}
	return a
}

// parseRSAKey reads a PKCS#1 or PKCS#8 RSA private key in PEM form, or nil.
func parseRSAKey(pemData []byte) *rsa.PrivateKey {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk
		}
	}
	return nil
}

// ConfigErr is nil when the App ID and key are usable.
func (a *AppSource) ConfigErr() error { return a.cfgErr }

func (a *AppSource) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *AppSource) base() string {
	if a.APIBase == "" {
		return "https://api.github.com"
	}
	return strings.TrimRight(a.APIBase, "/")
}

func (a *AppSource) httpClient() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// signJWT returns a short-lived App JWT.
func (a *AppSource) signJWT() (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: a.key},
		(&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		return "", errors.New("github app: cannot sign token")
	}
	now := a.now()
	s, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer:   a.appID,
		IssuedAt: jwt.NewNumericDate(now.Add(-appJWTBackdate)),
		Expiry:   jwt.NewNumericDate(now.Add(appJWTLifetime)),
	}).Serialize()
	if err != nil {
		return "", errors.New("github app: cannot sign token")
	}
	return s, nil
}

// call performs one App-JWT request and decodes a 2xx JSON reply into out.
// Errors carry method, path and status only.
func (a *AppSource) call(ctx context.Context, method, p string, body, out any) error {
	if a.cfgErr != nil {
		return a.cfgErr
	}
	jwtStr, err := a.signJWT()
	if err != nil {
		return err
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github %s %s: bad request", method, p)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base()+p, rdr)
	if err != nil {
		return fmt.Errorf("github %s %s: bad request", method, p)
	}
	req.Header.Set("Authorization", "Bearer "+jwtStr)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.httpClient().Do(req)
	if err != nil {
		// The transport error may embed the URL; keep method and path only.
		return fmt.Errorf("github %s %s: request failed", method, p)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAppReplySize))
	if err != nil {
		return fmt.Errorf("github %s %s: read failed", method, p)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// Deliberately no ErrNotFound kind: release code reads ErrNotFound as
		// "the requested object is absent", which a 404 from an App endpoint
		// (App not installed) is not.
		return &APIError{Method: method, Path: p, Status: resp.StatusCode}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("github %s %s: bad response", method, p)
	}
	return nil
}

// App fetches the App's own record (GET /app).
func (a *AppSource) App(ctx context.Context) (AppInfo, error) {
	var r struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := a.call(ctx, http.MethodGet, "/app", nil, &r); err != nil {
		return AppInfo{}, err
	}
	return AppInfo{ID: r.ID, Slug: r.Slug, Name: r.Name}, nil
}

func (a *AppSource) lookup(ctx context.Context) (Installation, error) {
	var r struct {
		ID          int64             `json:"id"`
		Permissions map[string]string `json:"permissions"`
	}
	if err := a.call(ctx, http.MethodGet, "/repos/"+a.repo+"/installation", nil, &r); err != nil {
		return Installation{}, err
	}
	if r.ID == 0 {
		return Installation{}, fmt.Errorf("github GET /repos/%s/installation: bad response", a.repo)
	}
	return Installation{ID: r.ID, Permissions: r.Permissions}, nil
}

// Installation looks the installation up live, ignoring the failure memory,
// and remembers its ID.
func (a *AppSource) Installation(ctx context.Context) (Installation, error) {
	inst, err := a.lookup(ctx)
	if err != nil {
		return Installation{}, err
	}
	a.mu.Lock()
	if a.installID != inst.ID {
		a.installID = inst.ID
		a.token = "" // belonged to another installation
	}
	a.mu.Unlock()
	return inst, nil
}

// Token returns a cached installation token, minting one when none is cached
// or it expires within five minutes. A failed lookup or mint is returned again
// for 30 s without calling GitHub.
func (a *AppSource) Token(ctx context.Context) (string, error) { return a.tokenFor(ctx, false) }

// FreshToken is Token without the failure memory.
func (a *AppSource) FreshToken(ctx context.Context) (string, error) { return a.tokenFor(ctx, true) }

func (a *AppSource) tokenFor(ctx context.Context, ignoreMemory bool) (string, error) {
	if a.cfgErr != nil {
		return "", a.cfgErr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if a.token != "" && now.Before(a.expiresAt.Add(-appTokenMargin)) {
		return a.token, nil
	}
	if !ignoreMemory && a.lastErr != nil && now.Before(a.failUntil) {
		return "", a.lastErr
	}
	tok, expires, err := a.mintLocked(ctx)
	if err != nil {
		// A caller giving up is not GitHub failing: do not poison other callers.
		if ctx.Err() == nil {
			a.lastErr, a.lastErrAt = err, now
			a.failUntil = now.Add(appFailureTTL)
		}
		return "", err
	}
	a.token, a.mintedAt, a.expiresAt = tok, now, expires
	a.lastErr, a.failUntil = nil, time.Time{}
	return tok, nil
}

// mintLocked looks the installation up if needed and mints a token. a.mu must
// be held.
func (a *AppSource) mintLocked(ctx context.Context) (string, time.Time, error) {
	if a.installID == 0 {
		inst, err := a.lookup(ctx)
		if err != nil {
			return "", time.Time{}, err
		}
		a.installID = inst.ID
	}
	name := a.repo
	if _, after, ok := strings.Cut(a.repo, "/"); ok {
		name = after
	}
	p := fmt.Sprintf("/app/installations/%d/access_tokens", a.installID)
	var r struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	req := struct {
		Repositories []string `json:"repositories"`
	}{[]string{name}}
	if err := a.call(ctx, http.MethodPost, p, req, &r); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			a.installID = 0 // uninstalled or reinstalled: look it up again next time
		}
		return "", time.Time{}, err
	}
	exp, err := time.Parse(time.RFC3339, r.ExpiresAt)
	if r.Token == "" || err != nil {
		return "", time.Time{}, fmt.Errorf("github POST %s: bad response", p)
	}
	return r.Token, exp, nil
}

// Invalidate drops the cached token if it is token, so the next call mints.
func (a *AppSource) Invalidate(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if token != "" && a.token == token {
		a.token = ""
		a.mintedAt, a.expiresAt = time.Time{}, time.Time{}
	}
}

// TokenState reports the cached token's times and the last failure.
func (a *AppSource) TokenState() TokenState {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := TokenState{LastErrorAt: a.lastErrAt}
	if a.token != "" {
		st.MintedAt, st.ExpiresAt = a.mintedAt, a.expiresAt
	}
	if a.lastErr != nil {
		st.LastError = a.lastErr.Error()
	}
	return st
}
