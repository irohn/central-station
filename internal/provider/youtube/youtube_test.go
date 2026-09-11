package youtube

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"central-station/internal/provider"
)

func TestVideoIDFromURL(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=6TWJaFD6R2s":                "6TWJaFD6R2s",
		"https://www.youtube.com/watch?v=6TWJaFD6R2s&list=PL1&t=10s": "6TWJaFD6R2s",
		"https://youtu.be/6TWJaFD6R2s?si=abc":                        "6TWJaFD6R2s",
		"https://m.youtube.com/watch?v=6TWJaFD6R2s":                  "6TWJaFD6R2s",
		"https://music.youtube.com/watch?v=6TWJaFD6R2s":              "6TWJaFD6R2s",
		"https://www.youtube.com/shorts/6TWJaFD6R2s":                 "6TWJaFD6R2s",
		"https://www.youtube.com/embed/6TWJaFD6R2s":                  "6TWJaFD6R2s",
		"https://www.youtube.com/live/6TWJaFD6R2s":                   "6TWJaFD6R2s",
		"https://www.youtube.com/?v=6TWJaFD6R2s":                     "6TWJaFD6R2s",
		"https://www.youtube.com/channel/UCabc":                      "",
		"https://www.youtube.com/watch?v=short":                      "",
		"https://www.youtube.com/playlist?list=PL123":                "",
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		got, ok := VideoIDFromURL(u)
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q ok=%v, want %q", raw, got, ok, want)
		}
	}
}

func TestMatchURL(t *testing.T) {
	p := New("")
	for raw, want := range map[string]bool{
		"https://www.youtube.com/watch?v=x": true,
		"https://YOUTU.BE/x":                true,
		"https://vimeo.com/x":               false,
		"https://notyoutube.com/x":          false,
	} {
		u, _ := url.Parse(raw)
		if p.MatchURL(u) != want {
			t.Errorf("%s: want %v", raw, want)
		}
	}
}

// fakeYouTube serves the search results page, oEmbed and Data API endpoints.
func fakeYouTube(t *testing.T) (*Provider, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/results", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search_query") == "nothing" {
			w.Write([]byte("<html>no results</html>"))
			return
		}
		if r.URL.Query().Get("sp") != "EgIQAQ==" {
			t.Errorf("missing video filter sp param: %s", r.URL.RawQuery)
		}
		w.Write([]byte(`<html><script>var ytInitialData = {"contents":[{"channelRenderer":{"channelId":"UC1"}},` +
			`{"videoRenderer":{"videoId":"dQw4w9WgXcQ","title":{"runs":[{"text":"Rick"}]}}},` +
			`{"videoRenderer":{"videoId":"AAAAAAAAAAA"}}]};</script></html>`))
	})
	mux.HandleFunc("/oembed", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("url") == CanonicalURL("gonegonegon") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"title":"Never Gonna Give You Up","author_name":"Rick Astley","thumbnail_url":"https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg"}`))
	})
	mux.HandleFunc("/v3/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "KEY" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Write([]byte(`{"items":[{"id":{"kind":"youtube#video","videoId":"dQw4w9WgXcQ"}}]}`))
	})
	mux.HandleFunc("/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") == "gonegonegon" {
			w.Write([]byte(`{"items":[]}`))
			return
		}
		w.Write([]byte(`{"items":[{"id":"dQw4w9WgXcQ","snippet":{"title":"Never Gonna Give You Up","channelTitle":"Rick Astley",` +
			`"thumbnails":{"default":{"url":"d.jpg"},"medium":{"url":"m.jpg"}}},"contentDetails":{"duration":"PT3M33S"}}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p := New("")
	p.HTTP = srv.Client()
	p.SearchPageURL = srv.URL + "/results"
	p.OEmbedURL = srv.URL + "/oembed"
	p.APIBaseURL = srv.URL + "/v3"
	return p, srv
}

func TestSearchKeyless(t *testing.T) {
	p, _ := fakeYouTube(t)
	tr, err := p.Search(context.Background(), "rick astley")
	if err != nil {
		t.Fatal(err)
	}
	if tr.ID != "dQw4w9WgXcQ" || tr.Title != "Never Gonna Give You Up" || tr.Artist != "Rick Astley" || tr.URL != CanonicalURL("dQw4w9WgXcQ") {
		t.Fatalf("track = %+v", tr)
	}
	if _, err := p.Search(context.Background(), "nothing"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestResolveURLKeyless(t *testing.T) {
	p, _ := fakeYouTube(t)
	u, _ := url.Parse("https://youtu.be/dQw4w9WgXcQ")
	tr, err := p.ResolveURL(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Provider != "youtube" || tr.Title != "Never Gonna Give You Up" || tr.ThumbnailURL == "" {
		t.Fatalf("track = %+v", tr)
	}

	u, _ = url.Parse("https://www.youtube.com/watch?v=gonegonegon")
	if _, err := p.ResolveURL(context.Background(), u); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("private video: want ErrNotFound, got %v", err)
	}
	u, _ = url.Parse("https://www.youtube.com/channel/UCabc")
	if _, err := p.ResolveURL(context.Background(), u); !errors.Is(err, provider.ErrInvalidURL) {
		t.Fatalf("channel: want ErrInvalidURL, got %v", err)
	}
}

func TestDataAPI(t *testing.T) {
	p, _ := fakeYouTube(t)
	p.APIKey = "KEY"
	tr, err := p.Search(context.Background(), "rick astley")
	if err != nil {
		t.Fatal(err)
	}
	if tr.DurationSeconds != 213 || tr.ThumbnailURL != "m.jpg" || tr.Artist != "Rick Astley" {
		t.Fatalf("track = %+v", tr)
	}
	u, _ := url.Parse("https://youtu.be/gonegonegon")
	if _, err := p.ResolveURL(context.Background(), u); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestProviderDown(t *testing.T) {
	p, srv := fakeYouTube(t)
	srv.Close()
	if _, err := p.Search(context.Background(), "x"); !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestParseISODuration(t *testing.T) {
	for in, want := range map[string]int{"PT3M33S": 213, "PT1H": 3600, "PT45S": 45, "P1DT1S": 86401, "garbage": 0, "": 0} {
		if got := parseISODuration(in); got != want {
			t.Errorf("%s: got %d want %d", in, got, want)
		}
	}
}
