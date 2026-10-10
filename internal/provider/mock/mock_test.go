package mock

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/zrgo/gecko/internal/provider"
)

func TestSuggest(t *testing.T) {
	want := provider.Suggestion{Command: "printf test", Explanation: "fixed", Risk: provider.RiskLow}
	failure := errors.New("provider failure")
	for _, resultErr := range []error{nil, failure} {
		p := New(want, resultErr)
		req := provider.Request{System: "rules", User: "query"}
		got, err := p.Suggest(context.Background(), req)
		if got != want || !errors.Is(err, resultErr) {
			t.Fatalf("Suggest = %+v, %v; want %+v, %v", got, err, want, resultErr)
		}
		requests := p.Requests()
		if len(requests) != 1 || requests[0] != req {
			t.Fatalf("requests = %+v", requests)
		}
		requests[0].User = "mutated"
		if p.Requests()[0] != req {
			t.Fatal("Requests exposed internal storage")
		}
	}
}

func TestSuggestCanceled(t *testing.T) {
	p := New(provider.Suggestion{Command: "printf test"}, errors.New("fixed error"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := p.Suggest(ctx, provider.Request{User: "query"})
	if got != (provider.Suggestion{}) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Suggest = %+v, %v", got, err)
	}
	if len(p.Requests()) != 1 {
		t.Fatal("canceled request not captured")
	}
}

func TestConcurrentRequests(t *testing.T) {
	p := New(provider.Suggestion{}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Suggest(context.Background(), provider.Request{User: "query"})
			p.Requests()
		}()
	}
	wg.Wait()
	if got := len(p.Requests()); got != 20 {
		t.Fatalf("captured %d requests, want 20", got)
	}
}
