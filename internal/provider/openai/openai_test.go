package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zrgo/gecko/internal/provider"
)

// server returns a test server that records the last request and replies
// with the given status and body.
func server(t *testing.T, status int, body string) (*httptest.Server, *http.Request, *chatRequest) {
	t.Helper()
	var gotReq http.Request
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = *r.Clone(context.Background())
		data, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(data, &gotBody); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &gotReq, &gotBody
}

func reply(content string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
	})
	return string(b)
}

var req = provider.Request{System: "sys prompt, reply in JSON", User: "list files"}

func TestSuggestOpenAI(t *testing.T) {
	srv, gotReq, gotBody := server(t, 200, reply(`{"command":"ls","explanation":"lists","risk":"low"}`))

	p, err := NewOpenAI(provider.Config{Name: "openai", Model: "gpt-x", BaseURL: srv.URL + "/v1/", APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.Suggest(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if s != (provider.Suggestion{Command: "ls", Explanation: "lists", Risk: provider.RiskLow}) {
		t.Fatalf("suggestion = %+v", s)
	}

	if gotReq.Method != http.MethodPost || gotReq.URL.Path != "/v1/chat/completions" {
		t.Errorf("got %s %s", gotReq.Method, gotReq.URL.Path)
	}
	if h := gotReq.Header.Get("Authorization"); h != "Bearer sk-test" {
		t.Errorf("Authorization = %q", h)
	}
	if gotBody.Model != "gpt-x" || len(gotBody.Messages) != 2 ||
		gotBody.Messages[0] != (message{"system", req.System}) ||
		gotBody.Messages[1] != (message{"user", req.User}) {
		t.Errorf("body = %+v", gotBody)
	}
	if gotBody.ResponseFormat == nil || gotBody.ResponseFormat.Type != "json_object" {
		t.Errorf("openai type should request JSON mode, got %+v", gotBody.ResponseFormat)
	}
}

func TestSuggestCompatibleNoKeyNoJSONMode(t *testing.T) {
	srv, gotReq, gotBody := server(t, 200, reply("Here you go:\n```json\n{\"command\":\"ls\",\"risk\":\"low\"}\n```"))

	p, err := NewCompatible(provider.Config{Name: "local", Model: "qwen", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.Suggest(context.Background(), req)
	if err != nil || s.Command != "ls" {
		t.Fatalf("s=%+v err=%v", s, err)
	}
	if h := gotReq.Header.Get("Authorization"); h != "" {
		t.Errorf("no key configured, but Authorization = %q", h)
	}
	if gotBody.ResponseFormat != nil {
		t.Errorf("compatible type should not send response_format")
	}
}

func TestConstructorErrors(t *testing.T) {
	tests := []struct {
		name     string
		factory  provider.Factory
		cfg      provider.Config
		contains string
	}{
		{"openai needs key", NewOpenAI, provider.Config{Model: "m"}, "no API key"},
		{"compatible needs base_url", NewCompatible, provider.Config{Model: "m"}, "base_url is required"},
		{"bad scheme", NewCompatible, provider.Config{Model: "m", BaseURL: "localhost:1234/v1"}, "invalid base_url"},
		{"no host", NewCompatible, provider.Config{Model: "m", BaseURL: "http://"}, "invalid base_url"},
		{"no model", NewOpenAI, provider.Config{APIKey: "k"}, "model is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.factory(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("want error containing %q, got %v", tt.contains, err)
			}
		})
	}
}

func TestSuggestErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		contains string
	}{
		{"openai error shape", 401, `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error"}}`,
			`provider "p": API error 401: Incorrect API key provided (check the API key)`},
		{"string error shape", 404, `{"error":"model not found"}`, "API error 404: model not found"},
		{"plain text error", 502, "upstream down", "API error 502: upstream down"},
		{"empty error body", 500, "", "API error 500: Internal Server Error"},
		{"no choices", 200, `{"choices":[]}`, "no choices"},
		{"truncated", 200, `{"choices":[{"message":{"content":"{\"comm"},"finish_reason":"length"}]}`, "cut off"},
		{"empty content", 200, `{"choices":[{"message":{"content":"  "},"finish_reason":"stop"}]}`, "empty content"},
		{"not json", 200, `<html>`, "decode response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _, _ := server(t, tt.status, tt.body)
			p, _ := NewCompatible(provider.Config{Name: "p", Model: "m", BaseURL: srv.URL})
			_, err := p.Suggest(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("want error containing %q, got %v", tt.contains, err)
			}
		})
	}
}

func TestRefusalIsNoCommand(t *testing.T) {
	srv, _, _ := server(t, 200, `{"choices":[{"message":{"content":null,"refusal":"I can't help with that."},"finish_reason":"stop"}]}`)
	p, _ := NewCompatible(provider.Config{Name: "p", Model: "m", BaseURL: srv.URL})
	_, err := p.Suggest(context.Background(), req)
	var nc *provider.NoCommandError
	if !errors.As(err, &nc) || nc.Reason != "I can't help with that." {
		t.Fatalf("got %v", err)
	}
}

func TestTimeoutAndCancel(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(block); srv.Close() })

	p, _ := NewCompatible(provider.Config{Name: "p", Model: "m", BaseURL: srv.URL})

	t.Run("client timeout", func(t *testing.T) {
		p.(*Client).http.Timeout = 50 * time.Millisecond
		if _, err := p.Suggest(context.Background(), req); err == nil || !strings.Contains(err.Error(), "request failed") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("context cancel", func(t *testing.T) {
		p.(*Client).http.Timeout = 0
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		if _, err := p.Suggest(ctx, req); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	})
}
