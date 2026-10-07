// Package provider defines the interface every LLM backend implements, the
// types passed across it, and a registry that maps a config "type" to a
// constructor.
//
// Providers are transport only: the prompt package builds the messages, the
// provider sends them and parses the reply into a Suggestion.
package provider

import (
	"context"
	"fmt"
	"strings"
)

// Config is what a provider needs to connect. It is deliberately separate
// from config.Resolved so provider packages do not depend on config loading.
type Config struct {
	Name    string // profile name, e.g. "groq"; used in error messages
	Model   string
	BaseURL string // empty means the provider's default endpoint
	APIKey  string // may be empty for local servers
}

// Request is a single completion request.
type Request struct {
	System string // system prompt: rules, environment, output format
	User   string // the user's request, including any tool hint
}

// Suggestion is the model's proposed command.
type Suggestion struct {
	Command     string `json:"command"`
	Explanation string `json:"explanation"`
	Risk        Risk   `json:"risk"`
}

// Provider turns a Request into a Suggestion.
type Provider interface {
	Suggest(ctx context.Context, req Request) (Suggestion, error)
}

// Risk is the model's own assessment of how dangerous a command is.
// It is informational only; gecko never blocks on it.
type Risk string

const (
	RiskLow     Risk = "low"     // read-only: ls, grep, find without -delete
	RiskMedium  Risk = "medium"  // modifies files or state in a recoverable way
	RiskHigh    Risk = "high"    // destructive, privileged, or hard to undo
	RiskUnknown Risk = "unknown" // missing or unrecognized
)

// ParseRisk normalizes a risk string, mapping anything unrecognized to RiskUnknown.
func ParseRisk(s string) Risk {
	switch r := Risk(strings.ToLower(strings.TrimSpace(s))); r {
	case RiskLow, RiskMedium, RiskHigh:
		return r
	default:
		return RiskUnknown
	}
}

// NoCommandError means the model declined or could not produce a command.
// Reason carries the model's explanation, if any.
type NoCommandError struct {
	Reason string
}

func (e *NoCommandError) Error() string {
	if e.Reason == "" {
		return "model returned no command"
	}
	return fmt.Sprintf("model returned no command: %s", e.Reason)
}
