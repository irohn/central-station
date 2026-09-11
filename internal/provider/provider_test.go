package provider

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
)

type fake struct {
	id    string
	hosts []string
	last  string
}

func (f *fake) ID() string   { return f.id }
func (f *fake) Name() string { return strings.ToUpper(f.id) }
func (f *fake) MatchURL(u *url.URL) bool {
	for _, h := range f.hosts {
		if u.Hostname() == h {
			return true
		}
	}
	return false
}
func (f *fake) ResolveURL(_ context.Context, u *url.URL) (*Track, error) {
	f.last = "url:" + u.String()
	return &Track{Provider: f.id, ID: "u", URL: u.String(), Title: "from url"}, nil
}
func (f *fake) Search(_ context.Context, q string) (*Track, error) {
	f.last = "search:" + q
	return &Track{Provider: f.id, ID: "s", Title: "from search"}, nil
}

func newReg(t *testing.T) (*Registry, *fake, *fake) {
	t.Helper()
	yt := &fake{id: "youtube", hosts: []string{"www.youtube.com", "youtu.be"}}
	sp := &fake{id: "spotify", hosts: []string{"open.spotify.com"}}
	r, err := NewRegistry("youtube", yt, sp)
	if err != nil {
		t.Fatal(err)
	}
	return r, yt, sp
}

func TestParseInputURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"https://www.youtube.com/watch?v=6TWJaFD6R2s", "https://www.youtube.com/watch?v=6TWJaFD6R2s", true},
		{"http://youtu.be/abc", "http://youtu.be/abc", true},
		{"youtu.be/abc", "https://youtu.be/abc", true},
		{"www.youtube.com/watch?v=x", "https://www.youtube.com/watch?v=x", true},
		{"spotify://track/123", "spotify://track/123", true},
		{"daft punk around the world", "", false},
		{"radiohead", "", false},
		{"", "", false},
		{"https://", "", false},
	}
	for _, c := range cases {
		u, ok := ParseInputURL(c.in)
		if ok != c.ok {
			t.Errorf("%q: ok=%v want %v", c.in, ok, c.ok)
			continue
		}
		if ok && u.String() != c.want {
			t.Errorf("%q: got %s want %s", c.in, u, c.want)
		}
	}
}

func TestResolveURLDetectsProvider(t *testing.T) {
	r, yt, sp := newReg(t)
	res, err := r.Resolve(context.Background(), "https://open.spotify.com/track/1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != InputURL || res.Track.Provider != "spotify" || sp.last == "" || yt.last != "" {
		t.Fatalf("unexpected resolution: %+v yt=%q sp=%q", res, yt.last, sp.last)
	}
}

func TestResolveUnsupportedProvider(t *testing.T) {
	r, _, _ := newReg(t)
	_, err := r.Resolve(context.Background(), "https://vimeo.com/12345?x=1", "")
	var uerr *UnsupportedProviderError
	if !errors.As(err, &uerr) {
		t.Fatalf("want UnsupportedProviderError, got %v", err)
	}
	if got := uerr.Error(); got != "Unsupported provider: https://vimeo.com" {
		t.Fatalf("message = %q", got)
	}
}

func TestResolveSearchDefaultsAndOverrides(t *testing.T) {
	r, yt, sp := newReg(t)
	res, err := r.Resolve(context.Background(), "  never gonna give you up ", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != InputSearch || res.Track.Provider != "youtube" || yt.last != "search:never gonna give you up" {
		t.Fatalf("default search wrong: %+v (%q)", res, yt.last)
	}
	res, err = r.Resolve(context.Background(), "never gonna give you up", "SPOTIFY")
	if err != nil {
		t.Fatal(err)
	}
	if res.Track.Provider != "spotify" || sp.last != "search:never gonna give you up" {
		t.Fatalf("override wrong: %+v (%q)", res, sp.last)
	}
}

func TestResolveErrors(t *testing.T) {
	r, _, _ := newReg(t)
	ctx := context.Background()

	if _, err := r.Resolve(ctx, "   ", ""); !errors.Is(err, ErrEmptyInput) {
		t.Errorf("empty input: %v", err)
	}
	var unknown *UnknownProviderError
	if _, err := r.Resolve(ctx, "song", "deezer"); !errors.As(err, &unknown) || unknown.ID != "deezer" {
		t.Errorf("unknown provider: %v", err)
	}
	var mismatch *ProviderMismatchError
	if _, err := r.Resolve(ctx, "https://youtu.be/abc", "spotify"); !errors.As(err, &mismatch) {
		t.Errorf("mismatch: %v", err)
	}
	if _, err := NewRegistry("nope", &fake{id: "youtube"}); err == nil {
		t.Error("expected error for unknown default provider")
	}
}

func TestList(t *testing.T) {
	r, _, _ := newReg(t)
	got := r.List()
	if len(got) != 2 || got[0].ID != "spotify" || got[1].ID != "youtube" || !got[1].Default || got[0].Default {
		t.Fatalf("list = %+v", got)
	}
}
