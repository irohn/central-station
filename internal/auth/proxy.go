package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"central-station/internal/httpx"
	"central-station/internal/store"
)

// HeaderConfig configures sign-in from a reverse proxy that names the person
// in a request header: the Tailscale operator's proxy, which sets
// Tailscale-User-Login to the tailnet identity of the connection after
// removing any copy the client sent.
type HeaderConfig struct {
	// Header carries the person's email address.
	Header string
	// NameHeader carries their display name; optional.
	NameHeader string
	// PictureHeader carries an https URL of their avatar; optional.
	PictureHeader string
	// TrustedPeers are the socket peers whose headers are believed: the pods
	// that forward the proxy's requests, as address ranges. A request from any
	// other address is refused whatever headers it carries, which is what
	// keeps a process on the node (which no NetworkPolicy can keep out) from
	// naming someone.
	TrustedPeers []netip.Prefix
	// Allowed applies the sign-in allow-list to an email address.
	Allowed func(email string) bool
	// SignIn resolves the identity to a user, creating or refreshing the
	// record. It runs on every request, so it should write only when needed.
	SignIn func(ctx context.Context, id Identity) (*store.User, error)
}

// Refusal says why a request carries no identity. Code is what the API
// reports; Message is for the person reading it.
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Message }

// Refusal codes.
const (
	// RefusalUntrustedPeer: the request did not come from the proxy, so its
	// identity header is not believed.
	RefusalUntrustedPeer = "untrusted_peer"
	// RefusalNoIdentity: the proxy attached no identity. A tagged tailnet
	// node, such as a CI runner, gets none.
	RefusalNoIdentity = "no_identity"
	// RefusalInvalidIdentity: the header does not hold an email address.
	RefusalInvalidIdentity = "invalid_identity"
	// RefusalEmailNotAllowed: the address is not on the sign-in allow-list.
	RefusalEmailNotAllowed = "email_not_allowed"
)

// HeaderAuth signs people in from a trusted proxy's identity header. There is
// no session and no cookie: the proxy names the person on every request, so a
// change in the tailnet policy or the allow-list applies on their next one,
// and there is nothing to sign out of.
type HeaderAuth struct {
	cfg HeaderConfig
}

// NewHeaderAuth checks the configuration once at boot.
func NewHeaderAuth(cfg HeaderConfig) (*HeaderAuth, error) {
	cfg.Header = strings.TrimSpace(cfg.Header)
	cfg.NameHeader = strings.TrimSpace(cfg.NameHeader)
	cfg.PictureHeader = strings.TrimSpace(cfg.PictureHeader)
	if cfg.Header == "" {
		return nil, errors.New("AUTH_TRUSTED_HEADER must name the identity header")
	}
	if len(cfg.TrustedPeers) == 0 {
		return nil, errors.New("AUTH_TRUSTED_PROXY_CIDRS must list the proxy's addresses")
	}
	if cfg.Allowed == nil || cfg.SignIn == nil {
		return nil, errors.New("auth: HeaderConfig needs Allowed and SignIn")
	}
	return &HeaderAuth{cfg: cfg}, nil
}

// Identify says who the proxy says is making the request, or why nobody is.
// The error is always a *Refusal.
func (h *HeaderAuth) Identify(r *http.Request) (Identity, error) {
	if !h.trusted(r.RemoteAddr) {
		return Identity{}, &Refusal{RefusalUntrustedPeer, fmt.Sprintf("identity headers are not accepted from %s", r.RemoteAddr)}
	}
	email := strings.ToLower(strings.TrimSpace(r.Header.Get(h.cfg.Header)))
	if email == "" {
		return Identity{}, &Refusal{RefusalNoIdentity, fmt.Sprintf("the request carries no %s header; open Central Station through the tailnet, from a machine signed in as a person", h.cfg.Header)}
	}
	if at := strings.LastIndex(email, "@"); at <= 0 || at == len(email)-1 {
		return Identity{}, &Refusal{RefusalInvalidIdentity, fmt.Sprintf("%q is not an email address", email)}
	}
	if !h.cfg.Allowed(email) {
		return Identity{Email: email}, &Refusal{RefusalEmailNotAllowed, fmt.Sprintf("%s is not allowed to sign in", email)}
	}
	id := Identity{Email: email, EmailVerified: true}
	if h.cfg.NameHeader != "" {
		id.Name = strings.TrimSpace(r.Header.Get(h.cfg.NameHeader))
	}
	if id.Name == "" {
		id.Name = email[:strings.LastIndex(email, "@")]
	}
	if h.cfg.PictureHeader != "" {
		if pic := strings.TrimSpace(r.Header.Get(h.cfg.PictureHeader)); pic != "" {
			if u, err := url.Parse(pic); err == nil && u.Scheme == "https" && u.Host != "" {
				id.Picture = pic
			}
		}
	}
	return id, nil
}

// Authenticate identifies the caller and resolves them to a user.
func (h *HeaderAuth) Authenticate(r *http.Request) (*store.User, error) {
	id, err := h.Identify(r)
	if err != nil {
		return nil, err
	}
	return h.cfg.SignIn(r.Context(), id)
}

// trusted reports whether remoteAddr (host:port) is inside TrustedPeers.
func (h *HeaderAuth) trusted(remoteAddr string) bool {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return false
	}
	addr := ap.Addr().Unmap()
	for _, p := range h.cfg.TrustedPeers {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ParsePrefixes parses a list of CIDRs, as AUTH_TRUSTED_PROXY_CIDRS gives
// them. A bare address is that one host.
func ParsePrefixes(cidrs []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if p, err := netip.ParsePrefix(c); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(c)
		if err != nil {
			return nil, fmt.Errorf("%q is not a CIDR or an address", c)
		}
		out = append(out, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
	}
	return out, nil
}

// RefusalFrom returns the proxy's reason for leaving a request anonymous, if
// the authenticator recorded one.
func RefusalFrom(ctx context.Context) (*Refusal, bool) {
	r, ok := ctx.Value(ctxRefusal).(*Refusal)
	return r, ok && r != nil
}

// unauthorized builds the 401 for an anonymous request: the proxy's reason
// when there is one, else the generic one.
func unauthorized(ctx context.Context) *httpx.Error {
	if ref, ok := RefusalFrom(ctx); ok {
		return httpx.NewError(http.StatusUnauthorized, ref.Code, "%s", ref.Message)
	}
	return httpx.Unauthorized("authentication required")
}
