package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"central-station/internal/httpx"
	"central-station/internal/rbac"
	"central-station/internal/store"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxViaCookie
)

// UserFrom returns the authenticated user stored on the request context.
func UserFrom(ctx context.Context) (*store.User, bool) {
	u, ok := ctx.Value(ctxUser).(*store.User)
	return u, ok && u != nil
}

// MustUser returns the authenticated user; call only behind RequireAuth.
func MustUser(r *http.Request) *store.User {
	u, ok := UserFrom(r.Context())
	if !ok {
		panic("auth: MustUser called without RequireAuth")
	}
	return u
}

// Authenticator resolves the current user from a Bearer token or session cookie.
type Authenticator struct {
	Sessions *Sessions
	Users    store.UserStore
	// TrustedOrigins are the origins allowed to make cookie-authenticated
	// state-changing requests (CSRF protection).
	TrustedOrigins []string
}

// Middleware attaches the user (if any) to the request context. It never
// rejects a request by itself; use RequireAuth / RequirePermission for that.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, viaCookie := tokenFromRequest(r)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		userID, err := a.Sessions.Parse(token)
		if err != nil {
			if viaCookie {
				// Stale cookie: drop it and continue anonymously.
				a.Sessions.ClearCookie(w)
				next.ServeHTTP(w, r)
				return
			}
			httpx.WriteError(w, r, httpx.Unauthorized("invalid or expired token"))
			return
		}
		u, err := a.Users.GetByID(r.Context(), userID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				if viaCookie {
					a.Sessions.ClearCookie(w)
				}
				next.ServeHTTP(w, r)
				return
			}
			httpx.WriteError(w, r, err)
			return
		}
		if viaCookie && !isSafeMethod(r.Method) && !a.originTrusted(r) {
			httpx.WriteError(w, r, httpx.Forbidden("cross-site request rejected"))
			return
		}
		ctx := context.WithValue(r.Context(), ctxUser, u)
		ctx = context.WithValue(ctx, ctxViaCookie, viaCookie)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func tokenFromRequest(r *http.Request) (token string, viaCookie bool) {
	if h := r.Header.Get("Authorization"); h != "" {
		if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
			return strings.TrimSpace(h[7:]), false
		}
		return "", false
	}
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		return c.Value, true
	}
	return "", false
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// originTrusted implements a simple CSRF check for cookie-authenticated
// mutations: the Origin (or Referer) header, when present, must be trusted,
// and Sec-Fetch-Site must not be cross-site.
func (a *Authenticator) originTrusted(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref := r.Header.Get("Referer"); ref != "" {
			origin = ref
		}
	}
	if origin == "" {
		// Non-browser client (curl) or same-origin navigation; SameSite=Lax
		// already prevents cross-site cookie sends on non-GET requests.
		return true
	}
	for _, t := range a.TrustedOrigins {
		if t != "" && (origin == t || strings.HasPrefix(origin, t+"/")) {
			return true
		}
	}
	return false
}

// RequireAuth rejects anonymous requests with 401.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			httpx.WriteError(w, r, httpx.Unauthorized("authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequirePermission rejects anonymous requests with 401 and requests whose
// user lacks the permission with 403.
func RequirePermission(p rbac.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, ok := UserFrom(r.Context())
			if !ok {
				httpx.WriteError(w, r, httpx.Unauthorized("authentication required"))
				return
			}
			if !u.Role.Has(p) {
				httpx.WriteError(w, r, httpx.Forbidden("role %q lacks permission %q", u.Role, p).WithDetails(map[string]any{"requiredPermission": p, "role": u.Role}))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
