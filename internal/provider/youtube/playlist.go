package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"central-station/internal/provider"
)

// MaxPlaylistTracks caps how many tracks a playlist expands to.
const MaxPlaylistTracks = 300

var playlistIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,}$`)

// PlaylistIDFromURL extracts the playlist ID from playlist and watch URLs
// (`list=`) and from YouTube Music browse pages (`/browse/VL<id>`), on any
// YouTube host including music.youtube.com. Auto-generated mixes (RD...) are
// session-specific on YouTube and rejected.
func (p *Provider) PlaylistIDFromURL(u *url.URL) (string, bool) {
	id := u.Query().Get("list")
	if id == "" {
		if segs := strings.Split(strings.Trim(u.Path, "/"), "/"); len(segs) == 2 && segs[0] == "browse" && strings.HasPrefix(segs[1], "VL") {
			id = strings.TrimPrefix(segs[1], "VL")
		}
	}
	if id == "" || !playlistIDRe.MatchString(id) || strings.HasPrefix(id, "RD") {
		return "", false
	}
	return id, true
}

// PlaylistURL returns the canonical URL for a playlist ID.
func PlaylistURL(id string) string { return "https://www.youtube.com/playlist?list=" + id }

// ResolvePlaylist expands a playlist: Data API when a key is configured,
// otherwise the public playlist page is parsed.
func (p *Provider) ResolvePlaylist(ctx context.Context, id string) (*provider.Playlist, error) {
	if p.APIKey != "" {
		return p.playlistAPI(ctx, id)
	}
	return p.scrapePlaylist(ctx, id)
}

// ---- Data API v3 backend ----

type apiPlaylistList struct {
	Items []struct {
		Snippet struct {
			Title string `json:"title"`
		} `json:"snippet"`
	} `json:"items"`
}

type apiPlaylistItems struct {
	NextPageToken string `json:"nextPageToken"`
	Items         []struct {
		Snippet struct {
			Title                  string `json:"title"`
			VideoOwnerChannelTitle string `json:"videoOwnerChannelTitle"`
			Thumbnails             map[string]struct {
				URL string `json:"url"`
			} `json:"thumbnails"`
			ResourceID struct {
				VideoID string `json:"videoId"`
			} `json:"resourceId"`
		} `json:"snippet"`
		Status struct {
			PrivacyStatus string `json:"privacyStatus"`
		} `json:"status"`
	} `json:"items"`
}

func (p *Provider) playlistAPI(ctx context.Context, id string) (*provider.Playlist, error) {
	var meta apiPlaylistList
	if err := p.apiGet(ctx, "/playlists", url.Values{"id": {id}, "part": {"snippet"}}, &meta); err != nil {
		return nil, err
	}
	if len(meta.Items) == 0 {
		return nil, fmt.Errorf("%w: YouTube playlist %s is unavailable (private or deleted?)", provider.ErrNotFound, id)
	}
	pl := &provider.Playlist{Provider: ProviderID, ID: id, URL: PlaylistURL(id), Title: meta.Items[0].Snippet.Title}

	pageToken := ""
	for len(pl.Tracks) < MaxPlaylistTracks {
		var page apiPlaylistItems
		params := url.Values{"playlistId": {id}, "part": {"snippet,status"}, "maxResults": {"50"}}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		if err := p.apiGet(ctx, "/playlistItems", params, &page); err != nil {
			return nil, err
		}
		var ids []string
		batch := map[string]*provider.Track{}
		for _, it := range page.Items {
			vid := it.Snippet.ResourceID.VideoID
			if vid == "" || it.Status.PrivacyStatus == "private" || it.Snippet.Title == "Deleted video" || it.Snippet.Title == "Private video" {
				continue
			}
			thumb := ""
			for _, k := range []string{"medium", "high", "default", "standard", "maxres"} {
				if t, ok := it.Snippet.Thumbnails[k]; ok && t.URL != "" {
					thumb = t.URL
					break
				}
			}
			t := &provider.Track{Provider: ProviderID, ID: vid, URL: CanonicalURL(vid), Title: it.Snippet.Title, Artist: it.Snippet.VideoOwnerChannelTitle, ThumbnailURL: thumb}
			batch[vid] = t
			ids = append(ids, vid)
			pl.Tracks = append(pl.Tracks, t)
		}
		// Durations live on the videos resource; one extra call per page.
		if len(ids) > 0 {
			var vids apiVideoList
			if err := p.apiGet(ctx, "/videos", url.Values{"id": {strings.Join(ids, ",")}, "part": {"contentDetails"}}, &vids); err == nil {
				for _, v := range vids.Items {
					if t, ok := batch[v.ID]; ok {
						t.DurationSeconds = parseISODuration(v.ContentDetails.Duration)
					}
				}
			}
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	if len(pl.Tracks) > MaxPlaylistTracks {
		pl.Tracks = pl.Tracks[:MaxPlaylistTracks]
	}
	if len(pl.Tracks) == 0 {
		return nil, fmt.Errorf("%w: YouTube playlist %s has no playable videos", provider.ErrNotFound, id)
	}
	return pl, nil
}

// ---- keyless backend: parse ytInitialData from the playlist page ----

var (
	initialDataRe = regexp.MustCompile(`(?s)ytInitialData\s*=\s*(\{.*?\});\s*</script>`)
	ogTitleRe     = regexp.MustCompile(`<meta property="og:title" content="([^"]*)"`)
)

func (p *Provider) scrapePlaylist(ctx context.Context, id string) (*provider.Playlist, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.PlaylistPageURL+"?"+url.Values{"list": {id}, "hl": {"en"}}.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.AddCookie(&http.Cookie{Name: "CONSENT", Value: "YES+cb"})
	req.AddCookie(&http.Cookie{Name: "SOCS", Value: "CAI"})

	res, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: playlist page: %v", provider.ErrUnavailable, err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: YouTube playlist %s is unavailable", provider.ErrNotFound, id)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: playlist page returned HTTP %d", provider.ErrUnavailable, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: playlist page: %v", provider.ErrUnavailable, err)
	}
	pl, err := parsePlaylistPage(id, body)
	if err != nil {
		return nil, err
	}
	return pl, nil
}

// parsePlaylistPage extracts the playlist title and its videos from the HTML.
func parsePlaylistPage(id string, body []byte) (*provider.Playlist, error) {
	pl := &provider.Playlist{Provider: ProviderID, ID: id, URL: PlaylistURL(id)}
	if m := ogTitleRe.FindSubmatch(body); m != nil {
		pl.Title = html.UnescapeString(string(m[1]))
	}
	m := initialDataRe.FindSubmatch(body)
	if m == nil {
		if bytes.Contains(body, []byte("This playlist does not exist")) || bytes.Contains(body, []byte("playlist is private")) {
			return nil, fmt.Errorf("%w: YouTube playlist %s is unavailable (private or deleted?)", provider.ErrNotFound, id)
		}
		return nil, fmt.Errorf("%w: could not find playlist data on the page", provider.ErrUnavailable)
	}
	var data any
	if err := json.Unmarshal(m[1], &data); err != nil {
		return nil, fmt.Errorf("%w: playlist data: %v", provider.ErrUnavailable, err)
	}
	if pl.Title == "" {
		if t, ok := dig(data, "metadata", "playlistMetadataRenderer", "title").(string); ok {
			pl.Title = t
		}
	}
	seen := map[string]bool{}
	add := func(t *provider.Track) {
		if t == nil || t.ID == "" || seen[t.ID] || len(pl.Tracks) >= MaxPlaylistTracks {
			return
		}
		seen[t.ID] = true
		pl.Tracks = append(pl.Tracks, t)
	}
	// Current markup (2025+): one lockupViewModel per video.
	walk(data, "lockupViewModel", func(v map[string]any) { add(trackFromLockup(v)) })
	// Legacy markup: playlistVideoRenderer.
	walk(data, "playlistVideoRenderer", func(v map[string]any) { add(trackFromLegacyRenderer(v)) })
	if len(pl.Tracks) == 0 {
		return nil, fmt.Errorf("%w: YouTube playlist %s has no playable videos", provider.ErrNotFound, id)
	}
	if pl.Title == "" {
		pl.Title = "YouTube playlist"
	}
	return pl, nil
}

// trackFromLockup decodes the lockupViewModel shape used by playlist pages.
func trackFromLockup(v map[string]any) *provider.Track {
	vid, _ := v["contentId"].(string)
	if ct, _ := v["contentType"].(string); ct != "LOCKUP_CONTENT_TYPE_VIDEO" || !videoIDRe.MatchString(vid) {
		return nil
	}
	t := &provider.Track{Provider: ProviderID, ID: vid, URL: CanonicalURL(vid)}
	meta := dig(v, "metadata", "lockupMetadataViewModel")
	t.Title, _ = dig(meta, "title", "content").(string)
	if rows, ok := dig(meta, "metadata", "contentMetadataViewModel", "metadataRows").([]any); ok && len(rows) > 0 {
		if parts, ok := dig(rows[0], "metadataParts").([]any); ok && len(parts) > 0 {
			t.Artist, _ = dig(parts[0], "text", "content").(string)
		}
	}
	thumb := dig(v, "contentImage", "thumbnailViewModel")
	if sources, ok := dig(thumb, "image", "sources").([]any); ok && len(sources) > 0 {
		t.ThumbnailURL, _ = dig(sources[len(sources)-1], "url").(string)
	}
	if overlays, ok := dig(thumb, "overlays").([]any); ok {
		for _, o := range overlays {
			badges, _ := dig(o, "thumbnailBottomOverlayViewModel", "badges").([]any)
			for _, b := range badges {
				if txt, ok := dig(b, "thumbnailBadgeViewModel", "text").(string); ok {
					if d := parseClock(txt); d > 0 {
						t.DurationSeconds = d
					}
				}
			}
		}
	}
	return t
}

// trackFromLegacyRenderer decodes the older playlistVideoRenderer shape.
func trackFromLegacyRenderer(v map[string]any) *provider.Track {
	vid, _ := v["videoId"].(string)
	if !videoIDRe.MatchString(vid) {
		return nil
	}
	if playable, ok := v["isPlayable"].(bool); ok && !playable {
		return nil
	}
	t := &provider.Track{Provider: ProviderID, ID: vid, URL: CanonicalURL(vid)}
	t.Title = runsText(dig(v, "title"))
	t.Artist = runsText(dig(v, "shortBylineText"))
	if s, ok := v["lengthSeconds"].(string); ok {
		t.DurationSeconds, _ = strconv.Atoi(s)
	}
	if thumbs, ok := dig(v, "thumbnail", "thumbnails").([]any); ok && len(thumbs) > 0 {
		t.ThumbnailURL, _ = dig(thumbs[len(thumbs)-1], "url").(string)
	}
	return t
}

// parseClock converts "3:55" or "1:02:03" to seconds (0 if not a clock string).
func parseClock(s string) int {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	total := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0
		}
		total = total*60 + n
	}
	return total
}

// dig follows a path of map keys, returning nil when any step is missing.
func dig(v any, path ...string) any {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// runsText flattens a YouTube text object ({"runs":[{"text":..}]} or {"simpleText":..}).
func runsText(v any) string {
	if s, ok := dig(v, "simpleText").(string); ok {
		return s
	}
	runs, _ := dig(v, "runs").([]any)
	var b strings.Builder
	for _, r := range runs {
		if s, ok := dig(r, "text").(string); ok {
			b.WriteString(s)
		}
	}
	return b.String()
}

// walk calls fn for every object found under a key named `key`. Arrays are
// visited in order, which is what preserves the playlist ordering: the video
// renderers all live in one `contents` array.
func walk(v any, key string, fn func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if k == key {
				if m, ok := child.(map[string]any); ok {
					fn(m)
					continue
				}
			}
			walk(child, key, fn)
		}
	case []any:
		for _, child := range x {
			walk(child, key, fn)
		}
	}
}
