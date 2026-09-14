package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORS(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	preflight := func(h http.Handler, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodOptions, "/api/v1/queue", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	listed := cors([]string{"http://localhost:3000"})(ok)
	if rec := preflight(listed, "http://localhost:3000"); rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("listed origin: %d %v", rec.Code, rec.Header())
	}
	if rec := preflight(listed, "http://192.168.1.20:3000"); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unlisted origin must get no CORS headers: %v", rec.Header())
	}

	// "*" reflects whatever origin asks (credentials need the exact origin, not a literal *).
	any := cors([]string{"*"})(ok)
	for _, o := range []string{"http://192.168.1.20:3000", "http://office-pc.local:3000"} {
		rec := preflight(any, o)
		if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != o || rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Errorf("any origin %s: %d %v", o, rec.Code, rec.Header())
		}
	}
}
