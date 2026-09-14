package v1

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"central-station/internal/auth"
	"central-station/internal/httpx"
	"central-station/internal/rbac"
	"central-station/internal/store"
)

const verifyTokenTTL = 24 * time.Hour

// verificationMode is "none" in demo mode (accounts are verified on
// registration), "email" when a mail server is configured, else "admin".
func (a *api) verificationMode() string {
	if a.Config.AuthDemoMode {
		return "none"
	}
	if a.Mail.Enabled() {
		return "email"
	}
	return "admin"
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

func normalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s || !strings.Contains(s, "@") {
		return "", httpx.BadRequest("invalid_email", "enter a valid email address")
	}
	return s, nil
}

// issueSession sets the cookie and writes the standard login response.
func (a *api) issueSession(w http.ResponseWriter, u *store.User, extra map[string]any) error {
	token, exp, err := a.Sessions.Issue(u.ID)
	if err != nil {
		return err
	}
	a.Sessions.SetCookie(w, token, exp)
	out := map[string]any{"token": token, "expiresAt": exp, "user": toUserDTO(u)}
	for k, v := range extra {
		out[k] = v
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// POST /auth/password/register  {"email","name","password"}
func (a *api) passwordRegister(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	email, err := normalizeEmail(body.Email)
	if err != nil {
		return err
	}
	if !a.emailAllowed(email) {
		return httpx.Forbidden("email %s is not on the sign-in allow-list", email)
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		return httpx.BadRequest("weak_password", "%s", err.Error())
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		body.Name = strings.SplitN(email, "@", 2)[0]
	}
	if len(body.Name) > 80 {
		body.Name = body.Name[:80]
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		return err
	}
	role, _ := rbac.ParseRole(a.Config.AuthUnverifiedRole)
	u, err := a.Users.Register(r.Context(), &store.User{Email: email, Name: body.Name, PasswordHash: hash, Role: role})
	if errors.Is(err, store.ErrExists) {
		return httpx.Conflict("email_taken", "an account with this email already exists; sign in instead")
	}
	if err != nil {
		return err
	}
	sent := false
	if a.Config.AuthDemoMode {
		// No verification step: the account is usable (and, for ADMIN_EMAILS,
		// an admin) straight away.
		if u, err = a.markVerified(r.Context(), u); err != nil {
			return err
		}
	} else {
		sent = a.sendVerification(r.Context(), u)
	}
	a.logger().Info("user registered", "email", email, "verification", a.verificationMode())
	return a.issueSession(w, u, map[string]any{"verification": a.verificationMode(), "verificationSent": sent})
}

// dummyHash keeps timing similar for unknown accounts (never matches).
var dummyHash = func() string { h, _ := auth.HashPassword("dummy-password-for-timing-only"); return h }()

// POST /auth/password/login  {"email","password"}
func (a *api) passwordLogin(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	key := email + "|" + clientIP(r)
	if a.loginLimiter.Blocked(key) {
		return httpx.NewError(http.StatusTooManyRequests, "too_many_attempts", "too many failed sign-in attempts; try again in a few minutes")
	}
	bad := func() error {
		a.loginLimiter.Fail(key)
		return httpx.Unauthorized("invalid email or password")
	}
	u, err := a.Users.GetByEmail(r.Context(), email)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !u.HasPassword()) {
		// Same cost and answer whether the account is unknown or Google-only.
		_, _ = auth.VerifyPassword(dummyHash, body.Password)
		return bad()
	}
	if err != nil {
		return err
	}
	ok, err := auth.VerifyPassword(u.PasswordHash, body.Password)
	if err != nil {
		return err
	}
	if !ok {
		return bad()
	}
	a.loginLimiter.Reset(key)
	_ = a.Users.TouchLogin(r.Context(), u.ID)
	if a.Config.AuthDemoMode && !u.EmailVerified {
		// Accounts created before demo mode was switched on catch up here.
		if u, err = a.markVerified(r.Context(), u); err != nil {
			return err
		}
	}
	return a.issueSession(w, u, map[string]any{"verification": a.verificationMode()})
}

// POST /auth/password/change  {"currentPassword","newPassword"}
func (a *api) passwordChange(w http.ResponseWriter, r *http.Request) error {
	u := auth.MustUser(r)
	var body struct {
		Current string `json:"currentPassword"`
		New     string `json:"newPassword"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	if u.HasPassword() {
		ok, err := auth.VerifyPassword(u.PasswordHash, body.Current)
		if err != nil {
			return err
		}
		if !ok {
			return httpx.Forbidden("current password is incorrect")
		}
	}
	if err := auth.ValidatePassword(body.New); err != nil {
		return httpx.BadRequest("weak_password", "%s", err.Error())
	}
	hash, err := auth.HashPassword(body.New)
	if err != nil {
		return err
	}
	if err := a.Users.SetPassword(r.Context(), u.ID, hash); err != nil {
		return err
	}
	httpx.NoContent(w)
	return nil
}

// sendVerification issues a token and emails the link when mail is
// configured. Returns whether an email went out.
func (a *api) sendVerification(ctx context.Context, u *store.User) bool {
	if !a.Mail.Enabled() || u.EmailVerified {
		return false
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return false
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if err := a.Users.SetVerifyToken(ctx, u.ID, token, time.Now().Add(verifyTokenTTL)); err != nil {
		a.logger().Error("store verify token", "err", err)
		return false
	}
	link := a.Config.BaseURL + "/api/v1/auth/verify?token=" + url.QueryEscape(token)
	body := fmt.Sprintf("Hi %s,\n\nConfirm your email address for Central Station by opening this link:\n\n%s\n\nThe link is valid for 24 hours. If you did not create an account, ignore this message.\n", u.Name, link)
	go func() {
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := a.Mail.Send(sctx, u.Email, "Confirm your email for Central Station", body); err != nil {
			a.logger().Error("send verification email", "to", u.Email, "err", err)
		}
	}()
	return true
}

// POST /auth/verify/resend
func (a *api) verifyResend(w http.ResponseWriter, r *http.Request) error {
	u := auth.MustUser(r)
	if u.EmailVerified {
		return httpx.Conflict("already_verified", "this email is already verified")
	}
	if !a.Mail.Enabled() {
		return httpx.Conflict("verification_by_admin", "this server has no mail service; ask an admin to verify your account")
	}
	a.sendVerification(r.Context(), u)
	httpx.NoContent(w)
	return nil
}

// GET /auth/verify?token=...  (link from the email) — marks the user verified,
// promotes the role and redirects to the app.
func (a *api) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	token := r.URL.Query().Get("token")
	u, err := a.Users.GetByVerifyToken(r.Context(), token)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !u.VerifyExpires.IsZero() && time.Now().After(u.VerifyExpires)) {
		return a.loginFailed(w, r, "", "verification_link_invalid")
	}
	if err != nil {
		return err
	}
	if _, err := a.markVerified(r.Context(), u); err != nil {
		return err
	}
	http.Redirect(w, r, a.frontendURL("/", url.Values{"verified": {"1"}}), http.StatusFound)
	return nil
}

// markVerified flips the flag and applies the verified-role rules: promotion
// to AUTH_VERIFIED_ROLE (never a demotion) and ADMIN_EMAILS.
func (a *api) markVerified(ctx context.Context, u *store.User) (*store.User, error) {
	v, err := a.Users.SetVerified(ctx, u.ID, true)
	if err != nil {
		return nil, err
	}
	want := v.Role
	if verified, err := rbac.ParseRole(a.Config.AuthVerifiedRole); err == nil && verified.Level() > want.Level() {
		want = verified
	}
	if a.isAdminEmail(v.Email) {
		want = rbac.RoleAdmin
	}
	if want != v.Role {
		if v, err = a.Users.SetRole(ctx, v.ID, want); err != nil {
			return nil, err
		}
	}
	a.logger().Info("email verified", "email", v.Email, "role", v.Role)
	return v, nil
}
