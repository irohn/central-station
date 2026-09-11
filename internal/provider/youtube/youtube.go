// Package youtube implements provider.Provider for YouTube.
//
// Two backends are supported:
//   - YouTube Data API v3 when an API key is configured (accurate metadata incl. duration).
//   - Keyless fallback: the public results page is scraped for the first video ID
//     and the oEmbed endpoint supplies title/author.
package youtube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"central-station/internal/provider"
)

const ProviderID = "youtube"

// Provider is the YouTube implementation. Zero-value fields are filled by New.
type Provider struct {
	APIKey string
	HTTP   *http.Client

	// Endpoints are overridable for tests.
	APIBaseURL    string // https://www.googleapis.com/youtube/v3
	OEmbedURL     string // https://www.youtube.com/oembed
	SearchPageURL string // https://www.youtube.com/results
}

// New returns a YouTube provider. apiKey may be empty to use the keyless fallback.
func New(apiKey string) *Provider {
	return &Provider{
		APIKey:        apiKey,
		HTTP:          &http.Client{Timeout: 10 * time.Second},
		APIBaseURL:    "https://www.googleapis.com/youtube/v3",
		OEmbedURL:     "https://www.youtube.com/oembed",
		SearchPageURL: "https://www.youtube.com/results",
	}
}

func (p *Provider) ID() string   { return ProviderID }
func (p *Provider) Name() string { return "YouTube" }

var hosts = map[string]bool{
	"youtube.com":              true,
	"www.youtube.com":          true,
	"m.youtube.com":            true,
	"music.youtube.com":        true,
	"youtu.be":                 true,
	"www.youtu.be":             true,
	"youtube-nocookie.com":     true,
	"www.youtube-nocookie.com": true,
}

// MatchURL reports whether u is hosted on a YouTube domain.
func (p *Provider) MatchURL(u *url.URL) bool {
	return hosts[strings.ToLower(u.Hostname())]
}

var videoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// VideoIDFromURL extracts the 11-character video ID from the many YouTube URL shapes.
func VideoIDFromURL(u *url.URL) (string, bool) {
	host := strings.ToLower(u.Hostname())
	path := strings.Trim(u.Path, "/")
	segs := strings.Split(path, "/")

	candidate := ""
	switch {
	case host == "youtu.be" || host == "www.youtu.be":
		candidate = segs[0]
	case path == "watch" || path == "":
		candidate = u.Query().Get("v")
	case len(segs) >= 2 && (segs[0] == "shorts" || segs[0] == "embed" || segs[0] == "live" || segs[0] == "v"):
		candidate = segs[1]
	case path == "attribution_link":
		// /attribution_link?u=/watch?v=ID
		if inner, err := url.Parse(u.Query().Get("u")); err == nil {
			candidate = inner.Query().Get("v")
		}
	}
	if candidate == "" {
		candidate = u.Query().Get("v")
	}
	if !videoIDRe.MatchString(candidate) {
		return "", false
	}
	return candidate, true
}

// CanonicalURL returns the canonical watch URL for a video ID.
func CanonicalURL(id string) string { return "https://www.youtube.com/watch?v=" + id }

// ResolveURL fetches metadata for a YouTube video URL.
func (p *Provider) ResolveURL(ctx context.Context, u *url.URL) (*provider.Track, error) {
	id, ok := VideoIDFromURL(u)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a YouTube video URL", provider.ErrInvalidURL, u.String())
	}
	return p.lookup(ctx, id)
}

// Search returns the first video for a query.
func (p *Provider) Search(ctx context.Context, query string) (*provider.Track, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, provider.ErrEmptyInput
	}
	if p.APIKey != "" {
		return p.searchAPI(ctx, query)
	}
	id, err := p.scrapeFirstVideoID(ctx, query)
	if err != nil {
		return nil, err
	}
	return p.lookup(ctx, id)
}

func (p *Provider) lookup(ctx context.Context, id string) (*provider.Track, error) {
	if p.APIKey != "" {
		return p.videoAPI(ctx, id)
	}
	return p.oembed(ctx, id)
}

// ---- keyless backend ----

type oembedResponse struct {
	Title        string `json:"title"`
	AuthorName   string `json:"author_name"`
	ThumbnailURL string `json:"thumbnail_url"`
}

func (p *Provider) oembed(ctx context.Context, id string) (*provider.Track, error) {
	q := url.Values{"url": {CanonicalURL(id)}, "format": {"json"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.OEmbedURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	res, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: oembed: %v", provider.ErrUnavailable, err)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound, res.StatusCode == http.StatusUnauthorized, res.StatusCode == http.StatusForbidden:
		// 401/403 is what YouTube returns for private or removed videos.
		return nil, fmt.Errorf("%w: YouTube video %s is unavailable", provider.ErrNotFound, id)
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: oembed returned HTTP %d", provider.ErrUnavailable, res.StatusCode)
	}
	var body oembedResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("%w: oembed: bad response: %v", provider.ErrUnavailable, err)
	}
	return &provider.Track{
		Provider:     ProviderID,
		ID:           id,
		URL:          CanonicalURL(id),
		Title:        body.Title,
		Artist:       body.AuthorName,
		ThumbnailURL: body.ThumbnailURL,
	}, nil
}

var (
	videoRendererRe = regexp.MustCompile(`"videoRenderer":\{"videoId":"([A-Za-z0-9_-]{11})"`)
	anyVideoIDRe    = regexp.MustCompile(`"videoId":"([A-Za-z0-9_-]{11})"`)
)

func (p *Provider) scrapeFirstVideoID(ctx context.Context, query string) (string, error) {
	// sp=EgIQAQ%3D%3D restricts results to videos (no channels/playlists).
	q := url.Values{"search_query": {query}, "sp": {"EgIQAQ=="}, "hl": {"en"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.SearchPageURL+"?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// Skip the EU consent interstitial.
	req.AddCookie(&http.Cookie{Name: "CONSENT", Value: "YES+cb"})
	req.AddCookie(&http.Cookie{Name: "SOCS", Value: "CAI"})

	res, err := p.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: search: %v", provider.ErrUnavailable, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: search returned HTTP %d", provider.ErrUnavailable, res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("%w: search: %v", provider.ErrUnavailable, err)
	}
	if m := videoRendererRe.FindSubmatch(body); m != nil {
		return string(m[1]), nil
	}
	if m := anyVideoIDRe.FindSubmatch(body); m != nil {
		return string(m[1]), nil
	}
	return "", fmt.Errorf("%w: no YouTube results for %q", provider.ErrNotFound, query)
}

// ---- Data API v3 backend ----

type apiVideoList struct {
	Items []struct {
		ID      string `json:"id"`
		Snippet struct {
			Title        string `json:"title"`
			ChannelTitle string `json:"channelTitle"`
			Thumbnails   map[string]struct {
				URL string `json:"url"`
			} `json:"thumbnails"`
		} `json:"snippet"`
		ContentDetails struct {
			Duration string `json:"duration"`
		} `json:"contentDetails"`
	} `json:"items"`
}

type apiSearchList struct {
	Items []struct {
		ID struct {
			VideoID string `json:"videoId"`
		} `json:"id"`
	} `json:"items"`
}

func (p *Provider) apiGet(ctx context.Context, path string, params url.Values, out any) error {
	params.Set("key", p.APIKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.APIBaseURL+path+"?"+params.Encode(), nil)
	if err != nil {
		return err
	}
	res, err := p.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: data api: %v", provider.ErrUnavailable, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("%w: data api returned HTTP %d: %s", provider.ErrUnavailable, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out); err != nil {
		return fmt.Errorf("%w: data api: bad response: %v", provider.ErrUnavailable, err)
	}
	return nil
}

func (p *Provider) videoAPI(ctx context.Context, id string) (*provider.Track, error) {
	var list apiVideoList
	err := p.apiGet(ctx, "/videos", url.Values{"id": {id}, "part": {"snippet,contentDetails"}}, &list)
	if err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, fmt.Errorf("%w: YouTube video %s is unavailable", provider.ErrNotFound, id)
	}
	it := list.Items[0]
	thumb := ""
	for _, k := range []string{"medium", "high", "default", "standard", "maxres"} {
		if t, ok := it.Snippet.Thumbnails[k]; ok && t.URL != "" {
			thumb = t.URL
			break
		}
	}
	return &provider.Track{
		Provider:        ProviderID,
		ID:              it.ID,
		URL:             CanonicalURL(it.ID),
		Title:           it.Snippet.Title,
		Artist:          it.Snippet.ChannelTitle,
		DurationSeconds: parseISODuration(it.ContentDetails.Duration),
		ThumbnailURL:    thumb,
	}, nil
}

func (p *Provider) searchAPI(ctx context.Context, query string) (*provider.Track, error) {
	var list apiSearchList
	params := url.Values{"q": {query}, "part": {"id"}, "type": {"video"}, "maxResults": {"1"}}
	if err := p.apiGet(ctx, "/search", params, &list); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 || list.Items[0].ID.VideoID == "" {
		return nil, fmt.Errorf("%w: no YouTube results for %q", provider.ErrNotFound, query)
	}
	return p.videoAPI(ctx, list.Items[0].ID.VideoID)
}

var isoDurationRe = regexp.MustCompile(`^P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// parseISODuration converts an ISO-8601 duration like "PT4M13S" to seconds.
func parseISODuration(s string) int {
	m := isoDurationRe.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
	return n(1)*86400 + n(2)*3600 + n(3)*60 + n(4)
}
