package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/url"
	"testing"
	"time"
)

func TestProviderAuthCodeURL(t *testing.T) {
	f := newFakeGoogle(t)
	u, err := url.Parse(f.provider().AuthCodeURL("st", "nn"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"state": "st", "nonce": "nn", "hd": "percona.com",
		"scope": "openid email profile", "redirect_uri": "http://dash.test/auth/callback",
		"client_id": testClientID, "response_type": "code",
	} {
		if got := q.Get(k); got != want {
			t.Errorf("%s: got %q want %q", k, got, want)
		}
	}
}

func TestProviderExchange(t *testing.T) {
	f := newFakeGoogle(t)
	f.claims = validClaims("  Alice@Percona.com ", "nn")
	id, err := f.provider().Exchange(context.Background(), "code")
	if err != nil {
		t.Fatal(err)
	}
	if id.Email != "alice@percona.com" || !id.EmailVerified || id.HostedDomain != "percona.com" || id.Nonce != "nn" {
		t.Fatalf("identity: %+v", id)
	}
}

func TestProviderExchangeRejects(t *testing.T) {
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(f *fakeGoogle){
		"wrong key": func(f *fakeGoogle) {
			// Replace the served token with one signed by an untrusted key.
			trusted := f.key
			f.key = other
			t.Cleanup(func() { f.key = trusted })
		},
		"wrong audience": func(f *fakeGoogle) { f.claims["aud"] = "someone-else" },
		"expired": func(f *fakeGoogle) {
			f.claims["exp"] = time.Now().Add(-time.Hour).Unix()
		},
		"no id_token": func(f *fakeGoogle) { f.noIDTok = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFakeGoogle(t)
			p := f.provider() // verifier trusts the original key
			f.claims = validClaims("alice@percona.com", "nn")
			mutate(f)
			if _, err := p.Exchange(context.Background(), "code"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
