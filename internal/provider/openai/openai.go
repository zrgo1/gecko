// Package openai implements the Chat Completions API spoken by OpenAI and by
// OpenAI-compatible servers (OpenRouter, Groq, LM Studio, vLLM, Ollama's /v1, ...).
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zrgo/gecko/internal/provider"
)

// DefaultBaseURL is used by the "openai" type when no base_url is configured.
const DefaultBaseURL = "https://api.openai.com/v1"

const (
	defaultTimeout = 60 * time.Second
	maxBodyBytes   = 1 << 20 // 1 MiB is far more than any single-command reply
)

// Client calls POST {baseURL}/chat/completions.
type Client struct {
	name     string
	model    string
	baseURL  string
	apiKey   string
	jsonMode bool // send response_format={"type":"json_object"}
	http     *http.Client
}

// NewOpenAI builds a client for api.openai.com (or a base_url override).
// An API key is required. JSON mode is enabled.
func NewOpenAI(cfg provider.Config) (provider.Provider, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("no API key: export the variable named by api_key_env (default OPENAI_API_KEY)")
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	return newClient(cfg, base, true)
}

// NewCompatible builds a client for any OpenAI-compatible server.
// base_url is required; the API key is optional (local servers rarely need one).
// JSON mode is disabled because several servers reject response_format
// "json_object" (LM Studio, for one); the reply parser tolerates prose instead.
func NewCompatible(cfg provider.Config) (provider.Provider, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("base_url is required for openai-compatible providers")
	}
	return newClient(cfg, cfg.BaseURL, false)
}

func newClient(cfg provider.Config, base string, jsonMode bool) (*Client, error) {
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid base_url %q: want http(s)://host[/path]", base)
	}
	if cfg.Model == "" {
		return nil, errors.New("model is required")
	}
	return &Client{
		name:     cfg.Name,
		model:    cfg.Model,
		baseURL:  strings.TrimRight(base, "/"),
		apiKey:   cfg.APIKey,
		jsonMode: jsonMode,
		http:     &http.Client{Timeout: defaultTimeout},
	}, nil
}

// Suggest sends the prompt and parses the reply into a Suggestion.
func (c *Client) Suggest(ctx context.Context, req provider.Request) (provider.Suggestion, error) {
	content, err := c.complete(ctx, req)
	if err != nil {
		return provider.Suggestion{}, fmt.Errorf("%s: %w", c.name, err)
	}
	return provider.ParseSuggestion(content)
}

// --- wire types (only the fields gecko uses) ---

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []message       `json:"messages"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	// temperature is intentionally omitted: reasoning models (o-series, gpt-5)
	// reject any value other than the default.
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func (c *Client) complete(ctx context.Context, req provider.Request) (string, error) {
	body := chatRequest{
		Model: c.model,
		Messages: []message{
			{Role: "system", Content: req.System},
			{Role: "user", Content: req.User},
		},
	}
	if c.jsonMode {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", newAPIError(resp.StatusCode, data)
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return "", errors.New("response has no choices")
	}
	ch := cr.Choices[0]
	switch {
	case ch.Message.Refusal != "":
		return "", &provider.NoCommandError{Reason: ch.Message.Refusal}
	case ch.FinishReason == "length":
		return "", errors.New("reply was cut off (max tokens reached)")
	case strings.TrimSpace(ch.Message.Content) == "":
		return "", errors.New("response has empty content")
	}
	return ch.Message.Content, nil
}

// APIError is a non-2xx reply from the server.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	s := fmt.Sprintf("API error %d: %s", e.StatusCode, msg)
	if e.StatusCode == http.StatusUnauthorized {
		s += " (check the API key)"
	}
	return s
}

// newAPIError extracts a message from the common error shapes:
// {"error":{"message":"..."}} (OpenAI), {"error":"..."} (some proxies),
// or a raw text body.
func newAPIError(status int, body []byte) *APIError {
	var env struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &env) == nil && len(env.Error) > 0 {
		var obj struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(env.Error, &obj) == nil && obj.Message != "" {
			return &APIError{StatusCode: status, Message: obj.Message}
		}
		var str string
		if json.Unmarshal(env.Error, &str) == nil && str != "" {
			return &APIError{StatusCode: status, Message: str}
		}
	}
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return &APIError{StatusCode: status, Message: text}
}
