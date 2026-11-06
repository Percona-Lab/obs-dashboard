package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// AllowedDomain is the only Google Workspace domain that may sign in.
const AllowedDomain = "percona.com"

const googleIssuer = "https://accounts.google.com"

// Identity is what a verified Google ID token says about the user.
type Identity struct {
	Email         string
	EmailVerified bool
	HostedDomain  string
	Nonce         string
}

// Provider runs the OIDC authorization-code flow against Google.
type Provider struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewProvider wires an OAuth2 config and ID token verifier. Tests use it with
// a fake token endpoint and a static key set.
func NewProvider(cfg oauth2.Config, verifier *oidc.IDTokenVerifier) *Provider {
	return &Provider{oauth: cfg, verifier: verifier}
}

// NewGoogleProvider discovers Google's endpoints and keys.
func NewGoogleProvider(ctx context.Context, clientID, clientSecret, redirectURL string) (*Provider, error) {
	p, err := oidc.NewProvider(ctx, googleIssuer)
	if err != nil {
		return nil, fmt.Errorf("google oidc discovery: %w", err)
	}
	return NewProvider(oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     p.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}, p.Verifier(&oidc.Config{ClientID: clientID})), nil
}

// AuthCodeURL is where the browser is sent to sign in. hd is only a hint to
// Google's account chooser; the callback enforces the domain.
func (p *Provider) AuthCodeURL(state, nonce string) string {
	return p.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("hd", AllowedDomain))
}

// Exchange trades the callback code for a verified identity.
func (p *Provider) Exchange(ctx context.Context, code string) (Identity, error) {
	tok, err := p.oauth.Exchange(ctx, code)
	if err != nil {
		return Identity{}, fmt.Errorf("code exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return Identity{}, errors.New("token response has no id_token")
	}
	idt, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, fmt.Errorf("verify id_token: %w", err)
	}
	var c struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		HD            string `json:"hd"`
	}
	if err := idt.Claims(&c); err != nil {
		return Identity{}, fmt.Errorf("id_token claims: %w", err)
	}
	return Identity{
		Email:         strings.ToLower(strings.TrimSpace(c.Email)),
		EmailVerified: c.EmailVerified,
		HostedDomain:  c.HD,
		Nonce:         idt.Nonce,
	}, nil
}
