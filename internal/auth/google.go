package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const googleIssuer = "https://accounts.google.com"

// Identity is the verified profile returned by Google.
type Identity struct {
	Sub           string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
	HostedDomain  string // Google Workspace domain, if any
}

// Google performs the OAuth 2.0 authorization-code flow with OpenID Connect.
type Google struct {
	oauth oauth2.Config

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

// NewGoogle configures the Google client. Discovery of Google's OIDC metadata
// happens lazily on first use so the API can start without network access.
func NewGoogle(clientID, clientSecret, redirectURL string) *Google {
	return &Google{oauth: oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     google.Endpoint,
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}}
}

// AuthURL builds the URL to send the browser to.
func (g *Google) AuthURL(state, nonce string) string {
	return g.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("prompt", "select_account"))
}

func (g *Google) getVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.verifier != nil {
		return g.verifier, nil
	}
	p, err := oidc.NewProvider(ctx, googleIssuer)
	if err != nil {
		return nil, fmt.Errorf("google oidc discovery: %w", err)
	}
	g.verifier = p.Verifier(&oidc.Config{ClientID: g.oauth.ClientID})
	return g.verifier, nil
}

// Exchange trades the authorization code for tokens and verifies the ID token.
func (g *Google) Exchange(ctx context.Context, code, nonce string) (*Identity, error) {
	verifier, err := g.getVerifier(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := g.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("google token exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return nil, errors.New("google response has no id_token")
	}
	idt, err := verifier.Verify(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("verify id_token: %w", err)
	}
	if idt.Nonce != nonce {
		return nil, errors.New("id_token nonce mismatch")
	}
	var c struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
		HD            string `json:"hd"`
	}
	if err := idt.Claims(&c); err != nil {
		return nil, fmt.Errorf("parse id_token claims: %w", err)
	}
	return &Identity{Sub: idt.Subject, Email: c.Email, EmailVerified: c.EmailVerified, Name: c.Name, Picture: c.Picture, HostedDomain: c.HD}, nil
}
