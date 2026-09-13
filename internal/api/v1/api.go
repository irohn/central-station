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
	"log/slog"
	"net/http"
	"strings"
	"time"

	"central-station/internal/alerts"
	"central-station/internal/auth"
	"central-station/internal/clips"
	"central-station/internal/config"
	"central-station/internal/httpx"
	"central-station/internal/mail"
	"central-station/internal/provider"
	"central-station/internal/rbac"
	"central-station/internal/settings"
	"central-station/internal/store"
	"central-station/internal/tts"
	"central-station/internal/zones"
)

// Version is the value of the API-Version response header.
const Version = "1"

// Deps are the collaborators the handlers need.
type Deps struct {
	Config    *config.Config
	Users     store.UserStore
	Providers *provider.Registry
	Sessions  *auth.Sessions
	Google    *auth.Google // nil when Google SSO is not configured
	// Zones owns one queue, player hub and default playlist per zone.
	Zones    *zones.Manager
	Clips    clips.Store    // recordings, TTS output and chimes
	TTS      *tts.Client    // nil or unconfigured disables typed announcements
	Settings settings.Store // small admin settings (default chime)
	Alerts   alerts.Store   // scheduled announcements (nil disables)
	Mail     *mail.Client   // verification email; nil or unconfigured = admins verify
}

type api struct {
	Deps
	loginLimiter *auth.Limiter
}

// Handler builds the v1 router. Mount it with http.StripPrefix("/api/v1", ...).
func Handler(d Deps) http.Handler {
	a := &api{Deps: d, loginLimiter: auth.NewLimiter(10, 15*time.Minute)}
	mux := http.NewServeMux()

	// Auth
	mux.Handle("GET /auth/methods", httpx.HandlerFunc(a.authMethods))
	mux.Handle("GET /auth/google/login", httpx.HandlerFunc(a.googleLogin))
	mux.Handle("GET /auth/google/callback", httpx.HandlerFunc(a.googleCallback))
	mux.Handle("POST /auth/logout", httpx.HandlerFunc(a.logout))
	if d.Config.AuthDevMode {
		mux.Handle("POST /auth/dev/login", httpx.HandlerFunc(a.devLogin))
	}
	if d.Config.AuthPasswordEnabled {
		mux.Handle("POST /auth/password/register", httpx.HandlerFunc(a.passwordRegister))
		mux.Handle("POST /auth/password/login", httpx.HandlerFunc(a.passwordLogin))
		mux.Handle("POST /auth/password/change", auth.RequireAuth(httpx.HandlerFunc(a.passwordChange)))
		mux.Handle("POST /auth/verify/resend", auth.RequireAuth(httpx.HandlerFunc(a.verifyResend)))
		mux.Handle("GET /auth/verify", httpx.HandlerFunc(a.verifyEmail))
	}
	mux.Handle("GET /me", auth.RequireAuth(httpx.HandlerFunc(a.me)))

	// Providers & tracks
	mux.Handle("GET /providers", auth.RequireAuth(httpx.HandlerFunc(a.listProviders)))
	mux.Handle("POST /tracks/resolve", a.perm(rbac.TrackResolve, a.resolveTrack))

	// Zones (each zone = one player node with its own queue). Zone-scoped
	// routes live under /zones/{zone}/...; the same paths without the prefix
	// address the default zone so pre-zone clients keep working.
	mux.Handle("GET /zones", auth.RequireAuth(httpx.HandlerFunc(a.listZones)))
	mux.Handle("POST /zones", a.perm(rbac.IntegrationsManage, a.createZone))
	mux.Handle("GET /zones/{zone}", auth.RequireAuth(httpx.HandlerFunc(a.getZone)))
	mux.Handle("PATCH /zones/{zone}", a.perm(rbac.IntegrationsManage, a.renameZone))
	mux.Handle("DELETE /zones/{zone}", a.perm(rbac.IntegrationsManage, a.deleteZone))
	mux.Handle("POST /zones/{zone}/token", a.perm(rbac.IntegrationsManage, a.rotateZoneToken))

	zoned := func(pattern string, h http.Handler) {
		method, path, _ := strings.Cut(pattern, " ")
		mux.Handle(method+" /zones/{zone}"+path, h)
		mux.Handle(pattern, h)
	}

	// Queue
	zoned("GET /queue", a.perm(rbac.QueueRead, a.getQueue))
	zoned("POST /queue", a.perm(rbac.QueueAdd, a.addToQueue))
	zoned("DELETE /queue/{id}", auth.RequireAuth(httpx.HandlerFunc(a.removeFromQueue)))
	zoned("PATCH /queue/{id}", a.perm(rbac.QueueReorder, a.moveInQueue))
	zoned("DELETE /queue/playlists/{id}", auth.RequireAuth(httpx.HandlerFunc(a.removePlaylist)))
	zoned("PATCH /queue/playlists/{id}", auth.RequireAuth(httpx.HandlerFunc(a.updatePlaylist)))
	zoned("GET /history", a.perm(rbac.HistoryRead, a.getHistory))

	// Playback
	zoned("GET /playback", a.perm(rbac.QueueRead, a.getPlayback))
	zoned("POST /playback/skip", a.perm(rbac.PlaybackControl, a.skip))
	zoned("POST /playback/pause", a.perm(rbac.PlaybackControl, a.pause))
	zoned("POST /playback/resume", a.perm(rbac.PlaybackControl, a.resume))
	zoned("POST /playback/complete", a.perm(rbac.PlaybackControl, a.complete))
	zoned("POST /playback/volume", a.perm(rbac.VolumeControl, a.setVolume))

	// Default playlist (plays when the queue is empty)
	zoned("GET /settings/fallback", a.perm(rbac.QueueRead, a.getFallback))
	zoned("PUT /settings/fallback", a.perm(rbac.FallbackManage, a.putFallback))
	zoned("POST /settings/fallback/refresh", a.perm(rbac.FallbackManage, a.refreshFallback))

	// Clips are global; announcements go to a zone (or every zone).
	mux.Handle("GET /clips", a.perm(rbac.BroadcastSend, a.listClips))
	mux.Handle("POST /clips", a.perm(rbac.BroadcastSend, a.uploadClip))
	mux.Handle("POST /clips/tts", a.perm(rbac.BroadcastSend, a.ttsClip))
	mux.Handle("POST /clips/tts/preview", a.perm(rbac.BroadcastSend, a.ttsPreview))
	mux.Handle("PUT /settings/tts", a.perm(rbac.SettingsManage, a.setDefaultVoice))
	mux.Handle("GET /clips/{id}/audio", auth.RequireAuth(httpx.HandlerFunc(a.clipAudio)))
	mux.Handle("PATCH /clips/{id}", a.perm(rbac.BroadcastSend, a.renameClip))
	mux.Handle("DELETE /clips/{id}", a.perm(rbac.BroadcastSend, a.deleteClip))
	zoned("GET /broadcasts", auth.RequireAuth(httpx.HandlerFunc(a.getBroadcasts)))
	zoned("POST /broadcasts", a.perm(rbac.BroadcastSend, a.sendBroadcast))
	mux.Handle("PUT /settings/chime", a.perm(rbac.SettingsManage, a.setDefaultChime))

	// Alerts (scheduled announcements; each targets a zone or all zones)
	mux.Handle("GET /alerts", a.perm(rbac.AlertsManage, a.listAlerts))
	mux.Handle("POST /alerts", a.perm(rbac.AlertsManage, a.createAlert))
	mux.Handle("PATCH /alerts/{id}", a.perm(rbac.AlertsManage, a.updateAlert))
	mux.Handle("DELETE /alerts/{id}", a.perm(rbac.AlertsManage, a.deleteAlert))
	mux.Handle("POST /alerts/{id}/test", a.perm(rbac.AlertsManage, a.testAlert))

	// Player + live events
	zoned("GET /player", auth.RequireAuth(httpx.HandlerFunc(a.getPlayer)))
	zoned("GET /events/ws", auth.RequireAuth(httpx.HandlerFunc(a.eventsWS)))

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
		TrustedOrigins: append([]string{d.Config.FrontendURL, d.Config.BaseURL}, d.Config.AllowedOrigins...),
	}
	// The player socket authenticates with a zone's player token (or the
	// legacy PLAYER_TOKEN), not a user session, so it is mounted outside the
	// session middleware.
	outer := http.NewServeMux()
	outer.Handle("GET /player/ws", httpx.HandlerFunc(a.playerWS))
	outer.Handle("GET /player/clips/{id}/audio", httpx.HandlerFunc(a.playerClipAudio))
	outer.Handle("/", authn.Middleware(mux))
	return versionHeader(outer)
}

func (a *api) logger() *slog.Logger { return slog.Default() }

// rt resolves the zone a request addresses: the {zone} path value, or the
// default zone for legacy unscoped routes.
func (a *api) rt(r *http.Request) (*zones.Runtime, error) {
	id := r.PathValue("zone")
	if id == "" {
		if rt := a.Zones.Default(); rt != nil {
			return rt, nil
		}
		return nil, httpx.NewError(http.StatusServiceUnavailable, "no_zones", "no zones are configured")
	}
	rt, ok := a.Zones.Get(id)
	if !ok {
		return nil, httpx.NotFound("zone_not_found", "zone %s not found", id)
	}
	return rt, nil
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
		noPlaylists *provider.PlaylistsUnsupportedError
	)
	switch {
	case errors.As(err, &noPlaylists):
		return httpx.BadRequest("playlists_unsupported", "%s", noPlaylists.Error())
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
	case errors.Is(err, provider.ErrNotPlaylist):
		return httpx.BadRequest("not_a_playlist", "%s", err.Error())
	case errors.Is(err, provider.ErrNotFound):
		return httpx.NotFound("track_not_found", "%s", err.Error())
	case errors.Is(err, provider.ErrUnavailable):
		return httpx.BadGateway("provider_unavailable", "%s", err.Error())
	}
	return err
}
