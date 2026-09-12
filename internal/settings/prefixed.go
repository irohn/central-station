package settings

import (
	"context"
	"encoding/json"
)

// Prefixed namespaces every key of a Store, e.g. per zone.
type Prefixed struct {
	Store  Store
	Prefix string
}

// WithPrefix returns a Store whose keys are prefixed.
func WithPrefix(s Store, prefix string) *Prefixed { return &Prefixed{Store: s, Prefix: prefix} }

func (p *Prefixed) Get(ctx context.Context, key string) (json.RawMessage, error) {
	return p.Store.Get(ctx, p.Prefix+key)
}

func (p *Prefixed) Set(ctx context.Context, key string, value json.RawMessage) error {
	return p.Store.Set(ctx, p.Prefix+key, value)
}
