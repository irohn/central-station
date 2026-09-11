// Package auth implements Google sign-in, signed session tokens and the
// request middleware that resolves the current user and enforces permissions.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// SessionCookie carries the session token for browser clients.
	SessionCookie = "cs_session"
	// stateCookie carries the signed OAuth state between login and callback.
	stateCookie = "cs_oauth_state"

	purposeSession = "session"
	purposeState   = "oauth_state"
	stateTTL       = 10 * time.Minute
)

// Sessions issues and verifies HMAC-signed session tokens (JWT, HS256).
// Tokens carry only the user ID; the role is looked up per request so that
// role changes take effect immediately.
type Sessions struct {
	secret []byte
	ttl    time.Duration
	secure bool
	now    func() time.Time
}

// NewSessions creates a session signer.
func NewSessions(secret string, ttl time.Duration, secureCookies bool) *Sessions {
	return &Sessions{secret: []byte(secret), ttl: ttl, secure: secureCookies, now: time.Now}
}

type claims struct {
	Purpose  string `json:"purpose"`
	Nonce    string `json:"nonce,omitempty"`
	Redirect string `json:"redirect,omitempty"`
	jwt.RegisteredClaims
}

// Issue creates a session token for the user.
func (s *Sessions) Issue(userID string) (token string, expires time.Time, err error) {
	now := s.now()
	expires = now.Add(s.ttl)
	c := claims{Purpose: purposeSession, RegisteredClaims: jwt.RegisteredClaims{
		Subject: userID, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(expires), ID: randomString(16),
	}}
	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
	return token, expires, err
}

// Parse validates a session token and returns the user ID.
func (s *Sessions) Parse(token string) (string, error) {
	c, err := s.parse(token, purposeSession)
	if err != nil {
		return "", err
	}
	if c.Subject == "" {
		return "", errors.New("session token has no subject")
	}
	return c.Subject, nil
}

func (s *Sessions) parse(token, purpose string) (*claims, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return s.secret, nil
	}, jwt.WithTimeFunc(s.now), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if c.Purpose != purpose {
		return nil, fmt.Errorf("token purpose %q, want %q", c.Purpose, purpose)
	}
	return &c, nil
}

// SetCookie writes the session cookie.
func (s *Sessions) SetCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie deletes the session cookie.
func (s *Sessions) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
}

// OAuthState is the data carried across the Google redirect.
type OAuthState struct {
	State    string
	Nonce    string
	Redirect string
}

// NewOAuthState generates fresh random state and nonce values.
func NewOAuthState(redirect string) OAuthState {
	return OAuthState{State: randomString(24), Nonce: randomString(24), Redirect: redirect}
}

// SetStateCookie stores the OAuth state in a short-lived signed cookie.
func (s *Sessions) SetStateCookie(w http.ResponseWriter, st OAuthState) error {
	now := s.now()
	c := claims{Purpose: purposeState, Nonce: st.Nonce, Redirect: st.Redirect, RegisteredClaims: jwt.RegisteredClaims{
		Subject: st.State, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(stateTTL)),
	}}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.secret)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: token, Path: "/", MaxAge: int(stateTTL.Seconds()),
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// ReadStateCookie verifies the state cookie and checks it against the state
// parameter returned by Google.
func (s *Sessions) ReadStateCookie(r *http.Request, state string) (*OAuthState, error) {
	ck, err := r.Cookie(stateCookie)
	if err != nil {
		return nil, errors.New("missing OAuth state cookie (login flow expired or started elsewhere)")
	}
	c, err := s.parse(ck.Value, purposeState)
	if err != nil {
		return nil, fmt.Errorf("invalid OAuth state cookie: %w", err)
	}
	if state == "" || c.Subject != state {
		return nil, errors.New("OAuth state mismatch")
	}
	return &OAuthState{State: c.Subject, Nonce: c.Nonce, Redirect: c.Redirect}, nil
}

// ClearStateCookie removes the OAuth state cookie.
func (s *Sessions) ClearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
