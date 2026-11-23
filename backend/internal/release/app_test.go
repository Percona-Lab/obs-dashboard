package release

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// requiredAppPermissions is the full permission set a fake installation grants
// when fakeAppAPI.permissions is nil.
var requiredAppPermissions = map[string]string{
	"contents":      "write",
	"pull_requests": "write",
	"actions":       "write",
	"metadata":      "read",
}

// fakeAppAPI serves the three App endpoints. Zero value: App installed, all
// calls succeed, tokens "ghs_1", "ghs_2", … expiring one hour after now().
type fakeAppAPI struct {
	mu             sync.Mutex
	key            *rsa.PrivateKey // JWTs are verified against this
	now            func() time.Time
	appStatus      int               // non-zero: GET /app answers this status
	instStatus     int               // non-zero: installation lookup answers this status
	mintStatus     int               // non-zero: mint answers this status
	expiresAt      string            // non-empty: literal expires_at in the mint response
	permissions    map[string]string // nil: the full required set
	mintDelay      time.Duration
	instID         int64  // zero: 7654321; a mint for any other ID answers 404
	lastMintPath   string // path of the latest mint request
	lookups, mints int
	requests       int // every request, whatever the outcome
	issued         int // tokens handed out
	lastClaims     jwt.Claims
	lastMintBody   string
}

func (f *fakeAppAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests++
	f.mu.Unlock()

	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	tok, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.RS256})
	var cl jwt.Claims
	if err != nil || tok.Claims(&f.key.PublicKey, &cl) != nil {
		http.Error(w, `{"message":"bad jwt"}`, http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	f.lastClaims = cl
	f.mu.Unlock()

	reply := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	fail := func(status int) { reply(status, map[string]string{"message": "ghs_leak eyJ BEGIN"}) }

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/app":
		f.mu.Lock()
		st := f.appStatus
		f.mu.Unlock()
		if st != 0 {
			fail(st)
			return
		}
		reply(200, map[string]any{"id": 123456, "slug": "percona-obs-release", "name": "Percona OBS Release"})
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/installation"):
		f.mu.Lock()
		f.lookups++
		st := f.instStatus
		perms := f.permissions
		f.mu.Unlock()
		if st != 0 {
			fail(st)
			return
		}
		if perms == nil {
			perms = requiredAppPermissions
		}
		reply(200, map[string]any{"id": f.installationID(), "permissions": perms})
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/app/installations/") && r.URL.Path != fmt.Sprintf("/app/installations/%d/access_tokens", f.installationID()):
		f.mu.Lock()
		f.mints++
		f.lastMintPath = r.URL.Path
		f.mu.Unlock()
		fail(404)
	case r.Method == http.MethodPost && r.URL.Path == fmt.Sprintf("/app/installations/%d/access_tokens", f.installationID()):
		var body strings.Builder
		buf := make([]byte, 512)
		for {
			n, err := r.Body.Read(buf)
			body.Write(buf[:n])
			if err != nil {
				break
			}
		}
		f.mu.Lock()
		f.mints++
		f.lastMintBody = body.String()
		f.lastMintPath = r.URL.Path
		st, delay, exp := f.mintStatus, f.mintDelay, f.expiresAt
		now := f.now
		f.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		if st != 0 {
			fail(st)
			return
		}
		f.mu.Lock()
		f.issued++
		n := f.issued
		f.mu.Unlock()
		resp := map[string]any{"token": fmt.Sprintf("ghs_%d", n)}
		if exp != "" {
			if exp != "-" {
				resp["expires_at"] = exp
			}
		} else {
			resp["expires_at"] = now().Add(time.Hour).UTC().Format(time.RFC3339)
		}
		reply(201, resp)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeAppAPI) installationID() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.instID != 0 {
		return f.instID
	}
	return 7654321
}

func (f *fakeAppAPI) counts() (lookups, mints int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookups, f.mints
}

var (
	testKeyOnce sync.Once
	testKey     *rsa.PrivateKey
)

func sharedTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	testKeyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

func testKeyB64(t *testing.T, key *rsa.PrivateKey, pkcs8 bool) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(testKeyPEM(t, key, pkcs8))
}

func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

func testKeyPEM(t *testing.T, key *rsa.PrivateKey, pkcs8 bool) []byte {
	t.Helper()
	if pkcs8 {
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return pemBlock("PRIVATE KEY", der)
	}
	return pemBlock("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
}

// newTestApp returns a source wired to a fake API, with a fixed clock the
// test can move through *clock.
func newTestApp(t *testing.T) (src *AppSource, api *fakeAppAPI, clock *time.Time) {
	t.Helper()
	key := sharedTestKey(t)
	c := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	clock = &c
	api = &fakeAppAPI{key: key, now: func() time.Time { return *clock }}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	src = NewAppSource("percona/obs-packaging", "123456", testKeyB64(t, key, false))
	src.APIBase = srv.URL
	src.Now = func() time.Time { return *clock }
	return src, api, clock
}

func TestAppSourceJWTClaims(t *testing.T) {
	src, api, clock := newTestApp(t)
	if _, err := src.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	cl := api.lastClaims
	if cl.Issuer != "123456" {
		t.Errorf("iss = %q", cl.Issuer)
	}
	if got, want := cl.IssuedAt.Time().Unix(), clock.Add(-60*time.Second).Unix(); got != want {
		t.Errorf("iat = %d, want %d", got, want)
	}
	if got, want := cl.Expiry.Time().Unix(), clock.Add(9*time.Minute).Unix(); got != want {
		t.Errorf("exp = %d, want %d", got, want)
	}
}

func TestAppSourceMintReuseRefresh(t *testing.T) {
	src, api, clock := newTestApp(t)
	ctx := context.Background()
	tok := func(want string) {
		t.Helper()
		got, err := src.Token(ctx)
		if err != nil || got != want {
			t.Fatalf("Token = %q, %v; want %q", got, err, want)
		}
	}
	tok("ghs_1")
	if l, m := api.counts(); l != 1 || m != 1 {
		t.Fatalf("lookups=%d mints=%d, want 1 1", l, m)
	}
	*clock = clock.Add(54 * time.Minute)
	tok("ghs_1")
	if _, m := api.counts(); m != 1 {
		t.Fatalf("mints=%d after 54m, want 1", m)
	}
	*clock = clock.Add(2 * time.Minute)
	tok("ghs_2")
	if l, m := api.counts(); l != 1 || m != 2 {
		t.Fatalf("lookups=%d mints=%d, want 1 2", l, m)
	}
	if api.lastMintBody != `{"repositories":["obs-packaging"]}` {
		t.Errorf("mint body = %s", api.lastMintBody)
	}
}

func TestAppSourceInvalidate(t *testing.T) {
	src, _, _ := newTestApp(t)
	ctx := context.Background()
	tok := func(want string) {
		t.Helper()
		got, err := src.Token(ctx)
		if err != nil || got != want {
			t.Fatalf("Token = %q, %v; want %q", got, err, want)
		}
	}
	tok("ghs_1")
	src.Invalidate("other")
	tok("ghs_1")
	src.Invalidate("ghs_1")
	tok("ghs_2")
}

func TestAppSourceNotInstalled(t *testing.T) {
	src, api, clock := newTestApp(t)
	api.instStatus = 404
	_, err := src.Token(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Method != "GET" || ae.Path != "/repos/percona/obs-packaging/installation" || ae.Status != 404 {
		t.Fatalf("err = %#v", err)
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err does not wrap ErrNotFound")
	}
	api.instStatus = 0
	*clock = clock.Add(31 * time.Second)
	if got, err := src.Token(context.Background()); err != nil || got != "ghs_1" {
		t.Fatalf("Token = %q, %v", got, err)
	}
}

func TestAppSourceFailureMemory(t *testing.T) {
	src, api, clock := newTestApp(t)
	ctx := context.Background()
	api.mintStatus = 500
	_, err1 := src.Token(ctx)
	if err1 == nil {
		t.Fatal("want error")
	}
	if _, m := api.counts(); m != 1 {
		t.Fatalf("mints=%d, want 1", m)
	}
	_, err2 := src.Token(ctx)
	if err2 == nil || err2.Error() != err1.Error() {
		t.Fatalf("second err = %v, want %v", err2, err1)
	}
	if _, m := api.counts(); m != 1 {
		t.Fatalf("mints=%d after remembered failure, want 1", m)
	}
	if _, err := src.FreshToken(ctx); err == nil {
		t.Fatal("FreshToken: want error")
	}
	if _, m := api.counts(); m != 2 {
		t.Fatalf("mints=%d after FreshToken, want 2", m)
	}
	*clock = clock.Add(31 * time.Second)
	api.mintStatus = 0
	if got, err := src.Token(ctx); err != nil || got != "ghs_1" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	st := src.TokenState()
	if st.LastError != "" {
		t.Errorf("LastError = %q after success", st.LastError)
	}

	// The error text while failing, from the state.
	api.mintStatus = 500
	src.Invalidate("ghs_1")
	if _, err := src.Token(ctx); err == nil {
		t.Fatal("want error")
	}
	want := "github POST /app/installations/7654321/access_tokens: HTTP 500"
	if got := src.TokenState().LastError; got != want {
		t.Errorf("LastError = %q, want %q", got, want)
	}
}

func TestAppSourceInstallationIgnoresFailureMemory(t *testing.T) {
	src, api, _ := newTestApp(t)
	ctx := context.Background()
	api.instStatus = 500
	if _, err := src.Token(ctx); err == nil {
		t.Fatal("want error")
	}
	api.instStatus = 0
	inst, err := src.Installation(ctx)
	if err != nil || inst.ID != 7654321 || inst.Permissions["contents"] != "write" {
		t.Fatalf("Installation = %+v, %v", inst, err)
	}
	if l, _ := api.counts(); l != 2 {
		t.Errorf("lookups=%d, want 2", l)
	}
}

func TestAppSourceApp(t *testing.T) {
	src, _, _ := newTestApp(t)
	info, err := src.App(context.Background())
	if err != nil || info.ID != 123456 || info.Slug != "percona-obs-release" || info.Name != "Percona OBS Release" {
		t.Fatalf("App = %+v, %v", info, err)
	}
}

func TestAppSourceMintBadExpiry(t *testing.T) {
	for name, exp := range map[string]string{"garbage": "not-a-time", "missing": "-"} {
		t.Run(name, func(t *testing.T) {
			src, api, _ := newTestApp(t)
			api.expiresAt = exp
			if _, err := src.Token(context.Background()); err == nil {
				t.Fatal("want error")
			}
			if !src.TokenState().ExpiresAt.IsZero() {
				t.Errorf("ExpiresAt = %v, want zero", src.TokenState().ExpiresAt)
			}
		})
	}
}

func TestAppSourceConcurrentMint(t *testing.T) {
	src, api, _ := newTestApp(t)
	api.mintDelay = 50 * time.Millisecond
	var wg sync.WaitGroup
	got := make([]string, 20)
	errs := make([]error, 20)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = src.Token(context.Background())
		}()
	}
	wg.Wait()
	for i := range got {
		if errs[i] != nil || got[i] != "ghs_1" {
			t.Fatalf("call %d = %q, %v", i, got[i], errs[i])
		}
	}
	if l, m := api.counts(); l != 1 || m != 1 {
		t.Fatalf("lookups=%d mints=%d, want 1 1", l, m)
	}
}

func TestAppSourceKeyForms(t *testing.T) {
	key := sharedTestKey(t)
	pkcs1 := testKeyB64(t, key, false)
	pkcs8 := testKeyB64(t, key, true)
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	ecB64 := base64.StdEncoding.EncodeToString(pemBlock("PRIVATE KEY", ecDER))
	notPEM := "is not an RSA private key in PEM form"

	cases := []struct {
		name, id, key, wantErr string
	}{
		{"pkcs1", "123456", pkcs1, ""},
		{"pkcs8", "123456", pkcs8, ""},
		{"whitespace", "  123456 ", "  " + pkcs1 + "\n", ""},
		{"no id", "", pkcs1, "RELEASE_GITHUB_APP_ID is not set"},
		{"blank id", "   ", pkcs1, "RELEASE_GITHUB_APP_ID is not set"},
		{"no key", "123456", "", "RELEASE_GITHUB_APP_PRIVATE_KEY is not set"},
		{"bad base64", "123456", "!!!", "RELEASE_GITHUB_APP_PRIVATE_KEY is not valid base64"},
		{"not pem", "123456", base64.StdEncoding.EncodeToString([]byte("hello")), "RELEASE_GITHUB_APP_PRIVATE_KEY " + notPEM},
		{"ec key", "123456", ecB64, "RELEASE_GITHUB_APP_PRIVATE_KEY " + notPEM},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			api := &fakeAppAPI{key: key, now: time.Now}
			srv := httptest.NewServer(api)
			defer srv.Close()
			src := NewAppSource("percona/obs-packaging", c.id, c.key)
			src.APIBase = srv.URL
			err := src.ConfigErr()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("ConfigErr = %v", err)
				}
				return
			}
			if err == nil || err.Error() != c.wantErr || !errors.Is(err, ErrAppConfig) {
				t.Fatalf("ConfigErr = %v, want %q wrapping ErrAppConfig", err, c.wantErr)
			}
			ctx := context.Background()
			_, e1 := src.Token(ctx)
			_, e2 := src.App(ctx)
			_, e3 := src.Installation(ctx)
			_, e4 := src.FreshToken(ctx)
			for i, e := range []error{e1, e2, e3, e4} {
				if e == nil || e.Error() != c.wantErr || !errors.Is(e, ErrAppConfig) {
					t.Errorf("call %d err = %v, want %q", i, e, c.wantErr)
				}
			}
			if api.requests != 0 {
				t.Errorf("fake API saw %d requests, want 0", api.requests)
			}
		})
	}
}

func TestAppSourceNoSecretInErrors(t *testing.T) {
	key := sharedTestKey(t)
	keyB64 := testKeyB64(t, key, false)
	ctx := context.Background()

	check := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: want error", name)
			return
		}
		s := err.Error()
		for _, bad := range []string{keyB64, "BEGIN", "eyJ", "ghs_"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s: error text contains a secret marker %q", name, bad)
			}
		}
	}

	src, api, _ := newTestApp(t)
	api.appStatus = 401
	_, err := src.App(ctx)
	check("app 401", err)

	src, api, _ = newTestApp(t)
	api.instStatus = 500
	_, err = src.Token(ctx)
	check("lookup 500", err)
	check("state after lookup 500", errors.New(src.TokenState().LastError))

	src, api, _ = newTestApp(t)
	api.mintStatus = 403
	_, err = src.Token(ctx)
	check("mint 403", err)
	check("state after mint 403", errors.New(src.TokenState().LastError))

	// A closed server: the transport error embeds the URL, which must not leak.
	src = NewAppSource("percona/obs-packaging", "123456", keyB64)
	dead := httptest.NewServer(http.NotFoundHandler())
	src.APIBase = dead.URL
	dead.Close()
	_, err = src.Token(ctx)
	check("closed server", err)
	if err != nil && strings.Contains(err.Error(), dead.URL) {
		t.Errorf("closed server: error contains the URL")
	}
}

func TestAppSourceReinstalled(t *testing.T) {
	src, api, clock := newTestApp(t)
	ctx := context.Background()
	if got, err := src.Token(ctx); err != nil || got != "ghs_1" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	api.mu.Lock()
	api.instID = 99
	api.mu.Unlock()
	src.Invalidate("ghs_1")
	_, err := src.Token(ctx)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Method != "POST" || ae.Path != "/app/installations/7654321/access_tokens" || ae.Status != 404 || !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %#v", err)
	}
	*clock = clock.Add(31 * time.Second)
	if got, err := src.Token(ctx); err != nil || got != "ghs_2" {
		t.Fatalf("Token = %q, %v", got, err)
	}
	if l, _ := api.counts(); l != 2 {
		t.Errorf("lookups=%d, want 2", l)
	}
	if api.lastMintPath != "/app/installations/99/access_tokens" {
		t.Errorf("mint path = %s", api.lastMintPath)
	}
}
