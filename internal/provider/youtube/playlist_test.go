package youtube

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"central-station/internal/provider"
)

func TestPlaylistIDFromURL(t *testing.T) {
	p := New("")
	cases := map[string]string{
		"https://www.youtube.com/playlist?list=PLFgquLnL59alCl_2TQvOiD5Vgm1hCaGSI": "PLFgquLnL59alCl_2TQvOiD5Vgm1hCaGSI",
		"https://www.youtube.com/watch?v=6TWJaFD6R2s&list=PL123abc":                "PL123abc",
		"https://music.youtube.com/playlist?list=OLAK5uy_abc":                      "OLAK5uy_abc",
		"https://music.youtube.com/browse/VLPL1FA6BD1610817B72":                    "PL1FA6BD1610817B72",
		"https://music.youtube.com/browse/UC_channel":                              "",
		"https://www.youtube.com/watch?v=6TWJaFD6R2s&list=RDabc":                   "", // auto mix
		"https://www.youtube.com/watch?v=6TWJaFD6R2s":                              "",
		"https://www.youtube.com/playlist?list=":                                   "",
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		got, ok := p.PlaylistIDFromURL(u)
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q ok=%v want %q", raw, got, ok, want)
		}
	}
}

const playlistPage = `<html><head><meta property="og:title" content="Office &amp; Chill"></head><body>
<script>var ytInitialData = {"contents":{"twoColumnBrowseResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[{"itemSectionRenderer":{"contents":[{"playlistVideoListRenderer":{"contents":[
{"playlistVideoRenderer":{"videoId":"aaaaaaaaaaa","thumbnail":{"thumbnails":[{"url":"https://i.ytimg.com/a-small.jpg"},{"url":"https://i.ytimg.com/a.jpg"}]},"title":{"runs":[{"text":"First "},{"text":"Song"}]},"shortBylineText":{"runs":[{"text":"Artist A"}]},"lengthSeconds":"215","isPlayable":true}},
{"playlistVideoRenderer":{"videoId":"bbbbbbbbbbb","title":{"runs":[{"text":"[Private video]"}]},"isPlayable":false}},
{"playlistVideoRenderer":{"videoId":"ccccccccccc","title":{"simpleText":"Third"},"shortBylineText":{"runs":[{"text":"Artist C"}]},"lengthSeconds":"60","isPlayable":true}},
{"playlistVideoRenderer":{"videoId":"aaaaaaaaaaa","title":{"runs":[{"text":"dup"}]},"isPlayable":true}},
{"lockupViewModel":{"contentId":"ddddddddddd","contentType":"LOCKUP_CONTENT_TYPE_VIDEO",
  "contentImage":{"thumbnailViewModel":{"image":{"sources":[{"url":"https://i.ytimg.com/d-small.jpg"},{"url":"https://i.ytimg.com/d.jpg"}]},
    "overlays":[{"thumbnailBottomOverlayViewModel":{"badges":[{"thumbnailBadgeViewModel":{"text":"1:02:03"}}]}}]}},
  "metadata":{"lockupMetadataViewModel":{"title":{"content":"Fourth (lockup)"},
    "metadata":{"contentMetadataViewModel":{"metadataRows":[{"metadataParts":[{"text":{"content":"Artist D"}}]},{"metadataParts":[{"text":{"content":"1M views"}}]}]}}}}}},
{"lockupViewModel":{"contentId":"PLnotavideo","contentType":"LOCKUP_CONTENT_TYPE_PLAYLIST"}},
{"continuationItemRenderer":{}}
]}}]}}]}}}}]}},"metadata":{"playlistMetadataRenderer":{"title":"Office & Chill"}}};</script>
</body></html>`

func TestParsePlaylistPage(t *testing.T) {
	pl, err := parsePlaylistPage("PL1", []byte(playlistPage))
	if err != nil {
		t.Fatal(err)
	}
	if pl.Title != "Office & Chill" || pl.URL != "https://www.youtube.com/playlist?list=PL1" {
		t.Fatalf("meta = %+v", pl)
	}
	if len(pl.Tracks) != 3 {
		t.Fatalf("tracks = %d, want 3 (unplayable, duplicate and non-video lockup skipped): %+v", len(pl.Tracks), pl.Tracks)
	}
	d := pl.Tracks[0] // lockups are parsed first (current markup)
	if d.ID != "ddddddddddd" || d.Title != "Fourth (lockup)" || d.Artist != "Artist D" || d.DurationSeconds != 3723 || d.ThumbnailURL != "https://i.ytimg.com/d.jpg" {
		t.Fatalf("lockup track = %+v", d)
	}
	a := pl.Tracks[1]
	if a.ID != "aaaaaaaaaaa" || a.Title != "First Song" || a.Artist != "Artist A" || a.DurationSeconds != 215 || a.ThumbnailURL != "https://i.ytimg.com/a.jpg" || a.URL != CanonicalURL("aaaaaaaaaaa") {
		t.Fatalf("track a = %+v", a)
	}
	if pl.Tracks[2].ID != "ccccccccccc" || pl.Tracks[2].Title != "Third" {
		t.Fatalf("track c = %+v", pl.Tracks[2])
	}
}

func TestParsePlaylistPageErrors(t *testing.T) {
	if _, err := parsePlaylistPage("PL1", []byte("<html>This playlist does not exist</html>")); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := parsePlaylistPage("PL1", []byte("<html>nothing</html>")); !errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestResolvePlaylistScrape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/playlist" || r.URL.Query().Get("list") != "PL1" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(playlistPage))
	}))
	defer srv.Close()
	p := New("")
	p.PlaylistPageURL = srv.URL + "/playlist"
	pl, err := p.ResolvePlaylist(context.Background(), "PL1")
	if err != nil || len(pl.Tracks) != 3 {
		t.Fatalf("resolve: %v %+v", err, pl)
	}
	if _, err := p.ResolvePlaylist(context.Background(), "nope"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
}

func TestResolvePlaylistAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/playlists", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "PL1" {
			w.Write([]byte(`{"items":[]}`))
			return
		}
		w.Write([]byte(`{"items":[{"snippet":{"title":"API list"}}]}`))
	})
	mux.HandleFunc("/playlistItems", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") == "" {
			w.Write([]byte(`{"nextPageToken":"p2","items":[
			 {"snippet":{"title":"One","videoOwnerChannelTitle":"Ch1","resourceId":{"videoId":"11111111111"},"thumbnails":{"medium":{"url":"http://t/1"}}},"status":{"privacyStatus":"public"}},
			 {"snippet":{"title":"Private video","resourceId":{"videoId":"22222222222"}},"status":{"privacyStatus":"private"}}]}`))
			return
		}
		w.Write([]byte(`{"items":[{"snippet":{"title":"Three","resourceId":{"videoId":"33333333333"}},"status":{"privacyStatus":"public"}}]}`))
	})
	mux.HandleFunc("/videos", func(w http.ResponseWriter, r *http.Request) {
		ids := r.URL.Query().Get("id")
		var items []string
		for _, id := range strings.Split(ids, ",") {
			items = append(items, `{"id":"`+id+`","contentDetails":{"duration":"PT3M"}}`)
		}
		w.Write([]byte(`{"items":[` + strings.Join(items, ",") + `]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	p := New("key")
	p.APIBaseURL = srv.URL
	pl, err := p.ResolvePlaylist(context.Background(), "PL1")
	if err != nil {
		t.Fatal(err)
	}
	if pl.Title != "API list" || len(pl.Tracks) != 2 || pl.Tracks[0].ID != "11111111111" || pl.Tracks[1].ID != "33333333333" {
		t.Fatalf("playlist = %+v", pl)
	}
	if pl.Tracks[0].DurationSeconds != 180 || pl.Tracks[0].Artist != "Ch1" || pl.Tracks[0].ThumbnailURL != "http://t/1" {
		t.Fatalf("track = %+v", pl.Tracks[0])
	}
	if _, err := p.ResolvePlaylist(context.Background(), "gone"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("gone: %v", err)
	}
}
