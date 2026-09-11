// Package v1 is the first major version of the HTTP API, mounted at /api/v1.
//
// Versioning rules for this codebase:
//   - The major version lives in the URL path and in the Go package name.
//   - Request/response DTOs are defined in this package and never shared with
//     a future v2, so each version can evolve independently.
//   - Within a version only additive, backwards-compatible changes are made
//     (new fields, new endpoints). Breaking changes go to a new package.
//   - Every response carries an "API-Version" header.
package v1

import (
	"errors"
	"net/http"

	"central-station/internal/auth"
	"central-station/internal/config"
	"central-station/internal/httpx"
	"central-station/internal/player"
	"central-station/internal/provider"
	"central-station/internal/queue"
	"central-station/internal/rbac"
	"central-station/internal/store"
)

// Version is the value of the API-Version response header.
const Version = "1"

// Deps are the collaborators the handlers need.
type Deps struct {
	Config    *config.Config
	Users     store.UserStore
	Queue     *queue.Service
	Providers *provider.Registry
	Sessions  *auth.Sessions
	Google    *auth.Google // nil when Google SSO is not configured
	Hub       *player.Hub
}

type api struct{ Deps }

// Handler builds the v1 router. Mount it with http.StripPrefix("/api/v1", ...).
func Handler(d Deps) http.Handler {
	a := &api{d}
	mux := http.NewServeMux()

	// Auth
	mux.Handle("GET /auth/methods", httpx.HandlerFunc(a.authMethods))
	mux.Handle("GET /auth/google/login", httpx.HandlerFunc(a.googleLogin))
	mux.Handle("GET /auth/google/callback", httpx.HandlerFunc(a.googleCallback))
	mux.Handle("POST /auth/logout", httpx.HandlerFunc(a.logout))
	if d.Config.AuthDevMode {
		mux.Handle("POST /auth/dev/login", httpx.HandlerFunc(a.devLogin))
	}
	mux.Handle("GET /me", auth.RequireAuth(httpx.HandlerFunc(a.me)))

	// Providers & tracks
	mux.Handle("GET /providers", auth.RequireAuth(httpx.HandlerFunc(a.listProviders)))
	mux.Handle("POST /tracks/resolve", a.perm(rbac.TrackResolve, a.resolveTrack))

	// Queue
	mux.Handle("GET /queue", a.perm(rbac.QueueRead, a.getQueue))
	mux.Handle("POST /queue", a.perm(rbac.QueueAdd, a.addToQueue))
	mux.Handle("DELETE /queue/{id}", auth.RequireAuth(httpx.HandlerFunc(a.removeFromQueue)))
	mux.Handle("PATCH /queue/{id}", a.perm(rbac.QueueReorder, a.moveInQueue))
	mux.Handle("GET /history", a.perm(rbac.HistoryRead, a.getHistory))

	// Playback
	mux.Handle("GET /playback", a.perm(rbac.QueueRead, a.getPlayback))
	mux.Handle("POST /playback/skip", a.perm(rbac.PlaybackControl, a.skip))
	mux.Handle("POST /playback/pause", a.perm(rbac.PlaybackControl, a.pause))
	mux.Handle("POST /playback/resume", a.perm(rbac.PlaybackControl, a.resume))
	mux.Handle("POST /playback/complete", a.perm(rbac.PlaybackControl, a.complete))

	// Player + live events
	mux.Handle("GET /player", auth.RequireAuth(httpx.HandlerFunc(a.getPlayer)))
	mux.Handle("GET /events/ws", auth.RequireAuth(httpx.HandlerFunc(a.eventsWS)))

	// Users (admin)
	mux.Handle("GET /users", a.perm(rbac.UsersManage, a.listUsers))
	mux.Handle("PATCH /users/{id}", a.perm(rbac.UsersManage, a.updateUser))
	mux.Handle("GET /roles", auth.RequireAuth(httpx.HandlerFunc(a.listRoles)))

	mux.Handle("/", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return httpx.NotFound("not_found", "no such endpoint: %s %s", r.Method, r.URL.Path)
	}))

	authn := &auth.Authenticator{
		Sessions:       d.Sessions,
		Users:          d.Users,
		TrustedOrigins: []string{d.Config.FrontendURL, d.Config.BaseURL},
	}
	// The player socket authenticates with PLAYER_TOKEN, not a user session,
	// so it is mounted outside the session middleware.
	outer := http.NewServeMux()
	outer.Handle("GET /player/ws", httpx.HandlerFunc(a.playerWS))
	outer.Handle("/", authn.Middleware(mux))
	return versionHeader(outer)
}

func (a *api) perm(p rbac.Permission, fn httpx.HandlerFunc) http.Handler {
	return auth.RequirePermission(p)(fn)
}

func versionHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", Version)
		next.ServeHTTP(w, r)
	})
}

// providerError maps provider package errors to API errors.
func providerError(err error) error {
	var (
		unsupported *provider.UnsupportedProviderError
		unknown     *provider.UnknownProviderError
		mismatch    *provider.ProviderMismatchError
	)
	switch {
	case errors.As(err, &unsupported):
		return httpx.BadRequest("unsupported_provider", "%s", unsupported.Error()).WithDetails(map[string]string{"baseUrl": unsupported.BaseURL})
	case errors.As(err, &unknown):
		return httpx.BadRequest("unknown_provider", "%s", unknown.Error()).WithDetails(map[string]any{"provider": unknown.ID, "available": unknown.Available})
	case errors.As(err, &mismatch):
		return httpx.BadRequest("provider_mismatch", "%s", mismatch.Error())
	case errors.Is(err, provider.ErrEmptyInput):
		return httpx.BadRequest("invalid_input", "%s", err.Error())
	case errors.Is(err, provider.ErrInvalidURL):
		return httpx.BadRequest("invalid_url", "%s", err.Error())
	case errors.Is(err, provider.ErrNotFound):
		return httpx.NotFound("track_not_found", "%s", err.Error())
	case errors.Is(err, provider.ErrUnavailable):
		return httpx.BadGateway("provider_unavailable", "%s", err.Error())
	}
	return err
}
