// Package provider defines the music/video provider abstraction and the
// registry that maps user input (a URL or a free-text search) to a provider.
package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Track is provider-agnostic metadata for a playable item.
type Track struct {
	Provider        string `json:"provider"`
	ID              string `json:"id"`
	URL             string `json:"url"`
	Title           string `json:"title"`
	Artist          string `json:"artist,omitempty"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
	ThumbnailURL    string `json:"thumbnailUrl,omitempty"`
}

// Provider resolves URLs and searches for a single source (YouTube, Spotify, ...).
type Provider interface {
	// ID is the stable identifier used in the API, e.g. "youtube".
	ID() string
	// Name is a human-friendly name, e.g. "YouTube".
	Name() string
	// MatchURL reports whether the URL belongs to this provider.
	MatchURL(u *url.URL) bool
	// ResolveURL fetches metadata for a URL that MatchURL accepted.
	ResolveURL(ctx context.Context, u *url.URL) (*Track, error)
	// Search returns the first result for a free-text query.
	Search(ctx context.Context, query string) (*Track, error)
}

// Sentinel errors returned by providers. The API layer maps them to HTTP statuses.
var (
	// ErrNotFound: the URL points at nothing playable, or the search had no results.
	ErrNotFound = errors.New("not found")
	// ErrInvalidURL: the URL belongs to the provider but is not a playable item
	// (e.g. a channel page). Wrap it with more context.
	ErrInvalidURL = errors.New("invalid url")
	// ErrUnavailable: the provider could not be reached or returned garbage.
	ErrUnavailable = errors.New("provider unavailable")
)

// UnsupportedProviderError is returned when a URL's host matches no registered provider.
type UnsupportedProviderError struct {
	BaseURL string // scheme://host of the offending URL
}

func (e *UnsupportedProviderError) Error() string {
	return "Unsupported provider: " + e.BaseURL
}

// UnknownProviderError is returned when a caller asks for a provider ID that is not registered.
type UnknownProviderError struct {
	ID        string
	Available []string
}

func (e *UnknownProviderError) Error() string {
	return fmt.Sprintf("Unknown provider %q (available: %s)", e.ID, strings.Join(e.Available, ", "))
}

// ProviderMismatchError is returned when the caller forces a provider that
// differs from the one the URL belongs to.
type ProviderMismatchError struct {
	Requested string
	Detected  string
}

func (e *ProviderMismatchError) Error() string {
	return fmt.Sprintf("URL belongs to provider %q but provider %q was requested", e.Detected, e.Requested)
}

// EmptyInputError is returned when the input is blank.
var ErrEmptyInput = errors.New("input must be a URL or a search string")

// Info describes a registered provider for API consumers.
type Info struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

// InputKind tells how the registry interpreted the input.
type InputKind string

const (
	InputURL    InputKind = "url"
	InputSearch InputKind = "search"
)

// Resolution is the outcome of Registry.Resolve.
type Resolution struct {
	Track *Track    `json:"track"`
	Kind  InputKind `json:"inputKind"`
	Query string    `json:"query"` // normalised input that was resolved
}

// Registry holds the configured providers and the default one used for searches.
type Registry struct {
	providers map[string]Provider
	order     []string
	defaultID string
}

// NewRegistry creates a registry. defaultID must be one of the given providers.
func NewRegistry(defaultID string, providers ...Provider) (*Registry, error) {
	r := &Registry{providers: map[string]Provider{}}
	for _, p := range providers {
		id := strings.ToLower(p.ID())
		if _, dup := r.providers[id]; dup {
			return nil, fmt.Errorf("provider %q registered twice", id)
		}
		r.providers[id] = p
		r.order = append(r.order, id)
	}
	sort.Strings(r.order)
	defaultID = strings.ToLower(defaultID)
	if _, ok := r.providers[defaultID]; !ok {
		return nil, &UnknownProviderError{ID: defaultID, Available: r.order}
	}
	r.defaultID = defaultID
	return r, nil
}

// DefaultID returns the provider used for searches when none is specified.
func (r *Registry) DefaultID() string { return r.defaultID }

// List returns the registered providers in a stable order.
func (r *Registry) List() []Info {
	out := make([]Info, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, Info{ID: id, Name: r.providers[id].Name(), Default: id == r.defaultID})
	}
	return out
}

// Get returns a provider by ID.
func (r *Registry) Get(id string) (Provider, error) {
	p, ok := r.providers[strings.ToLower(id)]
	if !ok {
		return nil, &UnknownProviderError{ID: id, Available: r.order}
	}
	return p, nil
}

// Resolve interprets input as either a URL or a search string.
//
//   - URL: the provider is detected from the host. An unknown host yields
//     *UnsupportedProviderError. If providerID is non-empty and differs from the
//     detected provider, *ProviderMismatchError is returned.
//   - Search: providerID (or the default provider) performs the search and the
//     first result is returned.
func (r *Registry) Resolve(ctx context.Context, input, providerID string) (*Resolution, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, ErrEmptyInput
	}
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	if providerID != "" {
		if _, err := r.Get(providerID); err != nil {
			return nil, err
		}
	}

	if u, ok := ParseInputURL(input); ok {
		p := r.match(u)
		if p == nil {
			return nil, &UnsupportedProviderError{BaseURL: u.Scheme + "://" + u.Host}
		}
		if providerID != "" && providerID != p.ID() {
			return nil, &ProviderMismatchError{Requested: providerID, Detected: p.ID()}
		}
		t, err := p.ResolveURL(ctx, u)
		if err != nil {
			return nil, err
		}
		return &Resolution{Track: t, Kind: InputURL, Query: u.String()}, nil
	}

	if providerID == "" {
		providerID = r.defaultID
	}
	p := r.providers[providerID]
	t, err := p.Search(ctx, input)
	if err != nil {
		return nil, err
	}
	return &Resolution{Track: t, Kind: InputSearch, Query: input}, nil
}

func (r *Registry) match(u *url.URL) Provider {
	for _, id := range r.order {
		if r.providers[id].MatchURL(u) {
			return r.providers[id]
		}
	}
	return nil
}

// bareHostRe matches inputs like "youtu.be/abc" or "www.youtube.com/watch?v=x"
// that lack a scheme but are clearly URLs rather than search phrases.
var bareHostRe = regexp.MustCompile(`^(?i)[a-z0-9-]+(\.[a-z0-9-]+)+(:\d+)?(/\S*)?$`)

// ParseInputURL decides whether input is a URL. Schemeless inputs that look like
// a hostname with a path ("youtube.com/watch?v=...") are upgraded to https.
func ParseInputURL(input string) (*url.URL, bool) {
	input = strings.TrimSpace(input)
	if strings.ContainsAny(input, " \t\r\n") {
		return nil, false
	}
	lower := strings.ToLower(input)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		u, err := url.Parse(input)
		if err != nil || u.Host == "" {
			return nil, false
		}
		return u, true
	}
	if strings.Contains(input, "://") {
		// Some other scheme (ftp, spotify:, ...) - treat as URL so the caller
		// can report an unsupported provider rather than searching for it.
		u, err := url.Parse(input)
		if err != nil || u.Host == "" {
			return nil, false
		}
		return u, true
	}
	if bareHostRe.MatchString(input) {
		u, err := url.Parse("https://" + input)
		if err != nil || u.Host == "" {
			return nil, false
		}
		return u, true
	}
	return nil, false
}

// ---- Playlists ----

// Playlist is a resolved collection of tracks (e.g. a YouTube playlist).
type Playlist struct {
	Provider string   `json:"provider"`
	ID       string   `json:"id"`
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	Tracks   []*Track `json:"tracks"`
}

// PlaylistProvider is implemented by providers that can expand playlist URLs.
type PlaylistProvider interface {
	Provider
	// PlaylistIDFromURL extracts the playlist ID from a URL that MatchURL
	// accepted. ok is false when the URL does not reference a playlist.
	PlaylistIDFromURL(u *url.URL) (id string, ok bool)
	// ResolvePlaylist fetches the playlist title and its tracks.
	ResolvePlaylist(ctx context.Context, id string) (*Playlist, error)
}

// ErrNotPlaylist: the URL belongs to the provider but does not reference a playlist.
var ErrNotPlaylist = errors.New("not a playlist url")

// PlaylistsUnsupportedError is returned when the URL's provider cannot expand playlists.
type PlaylistsUnsupportedError struct{ ProviderID string }

func (e *PlaylistsUnsupportedError) Error() string {
	return fmt.Sprintf("provider %q does not support playlists", e.ProviderID)
}

// ResolvePlaylist expands a playlist URL into its tracks. The provider is
// detected from the host like in Resolve; free-text input is rejected.
func (r *Registry) ResolvePlaylist(ctx context.Context, input string) (*Playlist, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil, ErrEmptyInput
	}
	u, ok := ParseInputURL(input)
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a URL", ErrInvalidURL, input)
	}
	p := r.match(u)
	if p == nil {
		return nil, &UnsupportedProviderError{BaseURL: u.Scheme + "://" + u.Host}
	}
	pp, ok := p.(PlaylistProvider)
	if !ok {
		return nil, &PlaylistsUnsupportedError{ProviderID: p.ID()}
	}
	id, ok := pp.PlaylistIDFromURL(u)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotPlaylist, u.String())
	}
	return pp.ResolvePlaylist(ctx, id)
}
