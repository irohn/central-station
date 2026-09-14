// Package server assembles the top-level HTTP handler: health endpoints, the
// API version index, cross-cutting middleware and the mounted API versions.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"central-station/internal/httpx"
)

// Options configure the server handler.
type Options struct {
	Logger         *slog.Logger
	AllowedOrigins []string // CORS origins allowed to call the API with credentials; "*" allows every origin
	Versions       map[string]http.Handler
	// Ready reports whether the service can serve traffic (for /readyz).
	Ready func() bool
}

// New builds the root handler.
func New(o Options) http.Handler {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if o.Ready != nil && !o.Ready() {
			httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	type versionInfo struct {
		Version string `json:"version"`
		Status  string `json:"status"`
		URL     string `json:"url"`
	}
	var versions []versionInfo
	for v, h := range o.Versions {
		versions = append(versions, versionInfo{Version: v, Status: "current", URL: "/api/" + v})
		mux.Handle("/api/"+v+"/", http.StripPrefix("/api/"+v, h))
	}
	mux.HandleFunc("GET /api", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]any{"versions": versions})
	})
	mux.HandleFunc("GET /api/{$}", func(w http.ResponseWriter, _ *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]any{"versions": versions})
	})
	mux.Handle("/", httpx.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) error {
		return httpx.NotFound("not_found", "no such endpoint: %s %s", r.Method, r.URL.Path)
	}))

	var h http.Handler = mux
	h = cors(o.AllowedOrigins)(h)
	h = securityHeaders(h)
	h = logging(o.Logger)(h)
	h = recoverer(o.Logger)(h)
	h = requestID(h)
	return h
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					log.Error("panic", "err", rec, "stack", string(debug.Stack()), "path", r.URL.Path)
					httpx.WriteError(w, r, httpx.NewError(http.StatusInternalServerError, "internal_error", "internal server error"))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			if sw.status == 0 {
				sw.status = http.StatusOK
			}
			log.Info("request",
				"method", r.Method, "path", r.URL.Path, "status", sw.status,
				"bytes", sw.bytes, "duration", time.Since(start).Round(time.Microsecond).String(),
				"requestId", w.Header().Get("X-Request-ID"), "remote", r.RemoteAddr,
			)
		})
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func cors(allowed []string) func(http.Handler) http.Handler {
	set := map[string]bool{}
	for _, o := range allowed {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			set[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && (set["*"] || set[origin]) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Add("Vary", "Origin")
				if r.Method == http.MethodOptions {
					h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
					h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
					h.Set("Access-Control-Expose-Headers", "API-Version, X-Request-ID")
					h.Set("Access-Control-Max-Age", "600")
					w.WriteHeader(http.StatusNoContent)
					return
				}
				h.Set("Access-Control-Expose-Headers", "API-Version, X-Request-ID")
			}
			next.ServeHTTP(w, r)
		})
	}
}
