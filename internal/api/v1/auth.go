package v1

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"central-station/internal/auth"
	"central-station/internal/httpx"
	"central-station/internal/rbac"
	"central-station/internal/store"
)

// userDTO is the public representation of a user.
type userDTO struct {
	*store.User
	Permissions []rbac.Permission `json:"permissions"`
	HasPassword bool              `json:"hasPassword"`
	// PermissionsOverridden is true while an admin has replaced this user's
	// permissions for the running API instance.
	PermissionsOverridden bool `json:"permissionsOverridden"`
}

func toUserDTO(u *store.User) userDTO {
	return userDTO{User: u, Permissions: u.Permissions(), HasPassword: u.HasPassword(), PermissionsOverridden: u.PermissionsOverridden()}
}

// GET /auth/google/login?redirect=/somewhere
func (a *api) googleLogin(w http.ResponseWriter, r *http.Request) error {
	if a.Google == nil {
		return httpx.NewError(http.StatusNotImplemented, "sso_not_configured", "Google SSO is not configured on this server")
	}
	st := auth.NewOAuthState(a.safeRedirect(r.URL.Query().Get("redirect")))
	if err := a.Sessions.SetStateCookie(w, st); err != nil {
		return err
	}
	http.Redirect(w, r, a.Google.AuthURL(st.State, st.Nonce), http.StatusFound)
	return nil
}

// GET /auth/google/callback?code=...&state=...
func (a *api) googleCallback(w http.ResponseWriter, r *http.Request) error {
	if a.Google == nil {
		return httpx.NewError(http.StatusNotImplemented, "sso_not_configured", "Google SSO is not configured on this server")
	}
	q := r.URL.Query()
	a.Sessions.ClearStateCookie(w)

	if e := q.Get("error"); e != "" {
		return a.loginFailed(w, r, "", "google_"+e)
	}
	st, err := a.Sessions.ReadStateCookie(r, q.Get("state"))
	if err != nil {
		return httpx.BadRequest("invalid_state", "%s", err.Error())
	}
	id, err := a.Google.Exchange(r.Context(), q.Get("code"), st.Nonce)
	if err != nil {
		return httpx.NewError(http.StatusBadGateway, "google_exchange_failed", "could not complete Google sign-in: %v", err)
	}
	if !id.EmailVerified {
		return a.loginFailed(w, r, st.Redirect, "email_not_verified")
	}
	if !a.emailAllowed(id.Email) {
		return a.loginFailed(w, r, st.Redirect, "email_not_allowed")
	}
	u, err := a.signIn(r.Context(), &store.User{Email: id.Email, Name: id.Name, PictureURL: id.Picture, GoogleSub: id.Sub}, "")
	if err != nil {
		return err
	}
	token, exp, err := a.Sessions.Issue(u.ID)
	if err != nil {
		return err
	}
	a.Sessions.SetCookie(w, token, exp)
	http.Redirect(w, r, a.frontendURL(st.Redirect, nil), http.StatusFound)
	return nil
}

// POST /auth/dev/login  {"email": "...", "name": "...", "role": "manager"}
// Only mounted when AUTH_DEV_MODE=true.
func (a *api) devLogin(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if err := httpx.Decode(r, &body, false); err != nil {
		return err
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	if body.Email == "" || !strings.Contains(body.Email, "@") {
		return httpx.BadRequest("invalid_email", "email is required")
	}
	if body.Name == "" {
		body.Name = strings.SplitN(body.Email, "@", 2)[0]
	}
	if !a.emailAllowed(body.Email) {
		return httpx.Forbidden("email %s is not on the sign-in allow-list", body.Email)
	}
	var role rbac.Role
	if body.Role != "" {
		var err error
		if role, err = rbac.ParseRole(body.Role); err != nil {
			return httpx.BadRequest("invalid_role", "%s", err.Error())
		}
	}
	u, err := a.signIn(r.Context(), &store.User{Email: body.Email, Name: body.Name}, role)
	if err != nil {
		return err
	}
	token, exp, err := a.Sessions.Issue(u.ID)
	if err != nil {
		return err
	}
	a.Sessions.SetCookie(w, token, exp)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"token":     token,
		"expiresAt": exp,
		"user":      toUserDTO(u),
	})
	return nil
}

// isAdminEmail reports whether email is listed in ADMIN_EMAILS.
func (a *api) isAdminEmail(email string) bool {
	email = strings.ToLower(email)
	for _, e := range a.Config.AdminEmails {
		if e == email {
			return true
		}
	}
	return false
}

// signIn upserts the user (identity-provider path: Google, the proxy header
// or dev login) and applies role rules: emails in ADMIN_EMAILS are always
// admins, new users get DEFAULT_ROLE, and forceRole (dev mode only)
// overrides the stored role.
func (a *api) signIn(ctx context.Context, in *store.User, forceRole rbac.Role) (*store.User, error) {
	email := strings.ToLower(in.Email)
	isAdmin := a.isAdminEmail(email)
	if _, err := a.Users.GetByEmail(ctx, email); errors.Is(err, store.ErrNotFound) {
		def, perr := rbac.ParseRole(a.Config.DefaultRole)
		if perr != nil {
			def = rbac.RoleListener
		}
		in.Role = def
	} else if err != nil {
		return nil, err
	}
	u, err := a.Users.Upsert(ctx, in)
	if err != nil {
		return nil, err
	}
	want := u.Role
	if isAdmin {
		want = rbac.RoleAdmin
	}
	if forceRole != "" {
		want = forceRole
	}
	if want != u.Role {
		if u, err = a.Users.SetRole(ctx, u.ID, want); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// proxyTouchInterval bounds how often proxy mode rewrites a user's row just
// to move LastLoginAt: there is no login event, so "last seen" is kept
// loosely instead of on every request.
const proxyTouchInterval = time.Hour

// proxySignIn resolves a proxy identity to a user on every request. The
// first request creates the user as Google sign-in would; later ones write
// only when the profile the proxy reports has changed, when ADMIN_EMAILS
// says the role is wrong, or when LastLoginAt is older than
// proxyTouchInterval.
func (a *api) proxySignIn(ctx context.Context, id auth.Identity) (*store.User, error) {
	u, err := a.Users.GetByEmail(ctx, id.Email)
	if errors.Is(err, store.ErrNotFound) {
		return a.signIn(ctx, &store.User{Email: id.Email, Name: id.Name, PictureURL: id.Picture}, "")
	}
	if err != nil {
		return nil, err
	}
	// The stored Name is the chosen display name when there is one, so the
	// profile name can only be compared while none is set.
	nameChanged := u.DisplayName == "" && u.Name != id.Name
	if nameChanged || u.PictureURL != id.Picture || time.Since(u.LastLoginAt) > proxyTouchInterval {
		return a.signIn(ctx, &store.User{Email: id.Email, Name: id.Name, PictureURL: id.Picture}, "")
	}
	if a.isAdminEmail(u.Email) && u.Role != rbac.RoleAdmin {
		return a.Users.SetRole(ctx, u.ID, rbac.RoleAdmin)
	}
	return u, nil
}

// GET /auth/whoami  (public) says who the caller is, and in proxy mode why
// they are not signed in when they are not: the proxy sent no identity, the
// request did not come from the proxy, or the address is not allowed.
func (a *api) whoami(w http.ResponseWriter, r *http.Request) error {
	mode := a.Config.AuthMode
	if u, ok := auth.UserFrom(r.Context()); ok {
		httpx.JSON(w, http.StatusOK, map[string]any{"mode": mode, "signedIn": true, "user": toUserDTO(u)})
		return nil
	}
	out := map[string]any{"mode": mode, "signedIn": false}
	if ref, ok := auth.RefusalFrom(r.Context()); ok {
		out["reason"] = ref.Code
		out["message"] = ref.Message
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

// POST /auth/logout
func (a *api) logout(w http.ResponseWriter, _ *http.Request) error {
	a.Sessions.ClearCookie(w)
	httpx.NoContent(w)
	return nil
}

// GET /me
func (a *api) me(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, toUserDTO(auth.MustUser(r)))
	return nil
}

// emailAllowed applies the sign-in allow-list (see config.AccessAllowlist).
// Admins are always allowed so the list can never lock everyone out.
func (a *api) emailAllowed(email string) bool {
	if !a.Config.AccessAllowlist {
		return true
	}
	email = strings.ToLower(strings.TrimSpace(email))
	for _, e := range a.Config.AdminEmails {
		if e == email {
			return true
		}
	}
	for _, e := range a.Config.AllowedEmails {
		if e == email {
			return true
		}
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return false
	}
	domain := email[at+1:]
	for _, d := range a.Config.AllowedEmailDomains {
		if d == domain {
			return true
		}
	}
	return false
}

// safeRedirect only accepts relative paths or URLs under the frontend origin
// (prevents open redirects).
func (a *api) safeRedirect(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return raw
	}
	if raw == a.Config.FrontendURL || strings.HasPrefix(raw, a.Config.FrontendURL+"/") {
		return raw
	}
	return ""
}

// frontendURL builds the post-login destination.
func (a *api) frontendURL(redirect string, params url.Values) string {
	target := a.Config.FrontendURL
	if redirect != "" {
		if strings.HasPrefix(redirect, "/") {
			target += redirect
		} else {
			target = redirect
		}
	}
	if len(params) > 0 {
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + params.Encode()
	}
	return target
}

func (a *api) loginFailed(w http.ResponseWriter, r *http.Request, redirect, reason string) error {
	http.Redirect(w, r, a.frontendURL(redirect, url.Values{"error": {reason}}), http.StatusFound)
	return nil
}
