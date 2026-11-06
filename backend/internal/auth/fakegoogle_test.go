package auth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

const (
	testIssuer   = "https://accounts.google.com"
	testClientID = "client-123"
)

// fakeGoogle is a token endpoint that returns an ID token signed with key and
// carrying whatever claims the test set last.
type fakeGoogle struct {
	t       *testing.T
	srv     *httptest.Server
	key     *rsa.PrivateKey
	claims  map[string]any
	noIDTok bool
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGoogle{t: t, key: key}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		body := map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600}
		if !f.noIDTok {
			body["id_token"] = f.sign(f.key, f.claims)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) sign(key *rsa.PrivateKey, claims map[string]any) string {
	f.t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		f.t.Fatal(err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	s, err := obj.CompactSerialize()
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

func (f *fakeGoogle) provider() *Provider {
	verifier := oidc.NewVerifier(testIssuer,
		&oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&f.key.PublicKey}},
		&oidc.Config{ClientID: testClientID})
	return NewProvider(oauth2.Config{
		ClientID:     testClientID,
		ClientSecret: "secret",
		RedirectURL:  "http://dash.test/auth/callback",
		Endpoint: oauth2.Endpoint{
			AuthURL:   f.srv.URL + "/auth",
			TokenURL:  f.srv.URL + "/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
		Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
	}, verifier)
}

// validClaims are the claims of a verified Percona Google account.
func validClaims(email, nonce string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": testIssuer, "aud": testClientID, "sub": "1",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"email": email, "email_verified": true, "hd": "percona.com", "nonce": nonce,
	}
}
