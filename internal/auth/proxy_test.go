package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"central-station/internal/store"
)

func TestParsePrefixes(t *testing.T) {
	got, err := ParsePrefixes([]string{" 10.42.0.2/31", "", "10.42.0.7/29", "192.168.1.9", "::ffff:10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.42.0.2/31", "10.42.0.0/29", "192.168.1.9/32", "10.0.0.1/32"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, p := range got {
		if p.String() != want[i] {
			t.Errorf("%d: %s, want %s", i, p, want[i])
		}
	}
	if _, err := ParsePrefixes([]string{"10.42.0.0/24", "nope"}); err == nil {
		t.Fatal("garbage accepted")
	}
}

func newTestHeaderAuth(t *testing.T, cidrs ...string) *HeaderAuth {
	t.Helper()
	peers, err := ParsePrefixes(cidrs)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHeaderAuth(HeaderConfig{
		Header: "Tailscale-User-Login", NameHeader: "Tailscale-User-Name", PictureHeader: "Tailscale-User-Profile-Pic",
		TrustedPeers: peers,
		Allowed:      func(email string) bool { return strings.HasSuffix(email, "@example.com") },
		SignIn: func(_ context.Context, id Identity) (*store.User, error) {
			return &store.User{ID: "u-" + id.Email, Email: id.Email, Name: id.Name, PictureURL: id.Picture}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHeaderAuthIdentify(t *testing.T) {
	h := newTestHeaderAuth(t, "10.42.0.2/31", "10.42.0.4/30")
	mk := func(remote string, hdr map[string]string) Identity {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/me", nil)
		r.RemoteAddr = remote
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		id, err := h.Identify(r)
		if err != nil {
			t.Fatalf("%s %v: %v", remote, hdr, err)
		}
		return id
	}
	refused := func(remote string, hdr map[string]string) string {
		t.Helper()
		r := httptest.NewRequest("GET", "/api/v1/me", nil)
		r.RemoteAddr = remote
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		_, err := h.Identify(r)
		var ref *Refusal
		if !errors.As(err, &ref) {
			t.Fatalf("%s %v: err = %v, want a Refusal", remote, hdr, err)
		}
		return ref.Code
	}

	id := mk("10.42.0.5:4321", map[string]string{"Tailscale-User-Login": " Ann@Example.com ", "Tailscale-User-Name": "Ann A", "Tailscale-User-Profile-Pic": "https://pics.example.com/ann.png"})
	if id.Email != "ann@example.com" || id.Name != "Ann A" || id.Picture != "https://pics.example.com/ann.png" || !id.EmailVerified {
		t.Fatalf("identity = %+v", id)
	}
	// IPv4-mapped peers are the same peers; a missing name falls back to the local part; a non-https picture is dropped.
	id = mk("[::ffff:10.42.0.2]:1", map[string]string{"Tailscale-User-Login": "bob@example.com", "Tailscale-User-Profile-Pic": "http://plain.example.com/x"})
	if id.Name != "bob" || id.Picture != "" {
		t.Fatalf("identity = %+v", id)
	}

	// The node (10.42.0.1), a pod outside the list, and the loopback are not the proxy.
	for _, remote := range []string{"10.42.0.1:9", "10.42.0.9:9", "127.0.0.1:9", "garbage"} {
		if code := refused(remote, map[string]string{"Tailscale-User-Login": "ann@example.com"}); code != RefusalUntrustedPeer {
			t.Errorf("%s: %s, want %s", remote, code, RefusalUntrustedPeer)
		}
	}
	if code := refused("10.42.0.3:9", nil); code != RefusalNoIdentity {
		t.Errorf("no header: %s", code)
	}
	if code := refused("10.42.0.3:9", map[string]string{"Tailscale-User-Login": "not-an-address"}); code != RefusalInvalidIdentity {
		t.Errorf("bad header: %s", code)
	}
	if code := refused("10.42.0.3:9", map[string]string{"Tailscale-User-Login": "eve@evil.example"}); code != RefusalEmailNotAllowed {
		t.Errorf("wrong domain: %s", code)
	}
}

func TestNewHeaderAuthRefusesHalfConfig(t *testing.T) {
	peers := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	ok := func(email string) bool { return true }
	signIn := func(context.Context, Identity) (*store.User, error) { return nil, nil }
	for name, cfg := range map[string]HeaderConfig{
		"no header": {TrustedPeers: peers, Allowed: ok, SignIn: signIn},
		"no peers":  {Header: "X-Login", Allowed: ok, SignIn: signIn},
		"no signin": {Header: "X-Login", TrustedPeers: peers, Allowed: ok},
	} {
		if _, err := NewHeaderAuth(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
