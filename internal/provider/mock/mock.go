// Package mock provides a deterministic in-process provider for tests.
// It is not registered as a CLI backend.
package mock

import (
	"context"
	"sync"

	"github.com/zrgo/gecko/internal/provider"
)

// Provider returns a fixed result and records requests. It is safe for concurrent use.
type Provider struct {
	suggestion provider.Suggestion
	err        error
	mu         sync.Mutex
	requests   []provider.Request
}

var _ provider.Provider = (*Provider)(nil)

// New constructs a provider returning suggestion and err unless the context is canceled.
func New(suggestion provider.Suggestion, err error) *Provider {
	return &Provider{suggestion: suggestion, err: err}
}

func (p *Provider) Suggest(ctx context.Context, req provider.Request) (provider.Suggestion, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return provider.Suggestion{}, err
	}
	return p.suggestion, p.err
}

// Requests returns a snapshot of received requests, including canceled calls.
func (p *Provider) Requests() []provider.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.Request(nil), p.requests...)
}
