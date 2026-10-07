package provider

import (
	"fmt"
	"sort"
	"strings"
)

// Factory builds a Provider from connection settings.
type Factory func(Config) (Provider, error)

// Registry maps a provider type (the `type` key in a config profile) to its
// Factory. Registration is explicit, in one place in the CLI, rather than via
// init() side effects, so tests can build registries with fakes.
type Registry struct {
	factories map[string]Factory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Register adds a factory under one or more type names, e.g. "openai" and
// "openai-compatible". It panics on an empty or duplicate name, since both
// are programming errors.
func (r *Registry) Register(f Factory, types ...string) {
	if f == nil {
		panic("provider: nil factory")
	}
	for _, t := range types {
		if t == "" {
			panic("provider: empty type name")
		}
		if _, dup := r.factories[t]; dup {
			panic("provider: duplicate type " + t)
		}
		r.factories[t] = f
	}
}

// New builds the provider registered under typ.
func (r *Registry) New(typ string, cfg Config) (Provider, error) {
	f, ok := r.factories[typ]
	if !ok {
		return nil, fmt.Errorf("provider %q: unknown type %q (supported: %s)",
			cfg.Name, typ, strings.Join(r.Types(), ", "))
	}
	p, err := f(cfg)
	if err != nil {
		return nil, fmt.Errorf("provider %q: %w", cfg.Name, err)
	}
	return p, nil
}

// Types lists registered type names in sorted order.
func (r *Registry) Types() []string {
	types := make([]string, 0, len(r.factories))
	for t := range r.factories {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}
