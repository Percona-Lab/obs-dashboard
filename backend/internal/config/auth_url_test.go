package config

import (
	"strings"
	"testing"
)

func TestAuthPublicURLCanonical(t *testing.T) {
	t.Setenv("OBS_USERNAME", "u")
	t.Setenv("GOOGLE_CLIENT_ID", "id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "secret")
	for _, bad := range []string{
		"https://x.example.com/dash",
		"https://x.example.com/?a=b",
		"https://x.example.com/#f",
		"https://u@x.example.com",
		"ftp://x",
	} {
		t.Setenv("AUTH_PUBLIC_URL", bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "auth.public_url") {
			t.Errorf("%q: expected auth.public_url error, got %v", bad, err)
		}
	}
	for in, want := range map[string]string{
		"HTTPS://Dashboard.Example.com:443/": "https://dashboard.example.com",
		"http://Localhost:80":                "http://localhost",
		"http://localhost:5173/":             "http://localhost:5173",
	} {
		t.Setenv("AUTH_PUBLIC_URL", in)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if cfg.Auth.PublicURL != want {
			t.Errorf("%q: got %q want %q", in, cfg.Auth.PublicURL, want)
		}
	}
}
