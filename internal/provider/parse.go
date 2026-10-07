package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ParseSuggestion decodes the model's JSON reply into a Suggestion.
//
// It is shared by all providers and tolerant of common model habits:
// surrounding ```json fences and prose before or after the object.
// An empty command yields a *NoCommandError carrying the explanation.
func ParseSuggestion(raw string) (Suggestion, error) {
	obj, err := extractObject(raw)
	if err != nil {
		return Suggestion{}, err
	}

	var wire struct {
		Command     string `json:"command"`
		Explanation string `json:"explanation"`
		Risk        string `json:"risk"`
	}
	if err := json.Unmarshal([]byte(obj), &wire); err != nil {
		return Suggestion{}, fmt.Errorf("decode model reply: %w", err)
	}

	s := Suggestion{
		Command:     strings.TrimSpace(wire.Command),
		Explanation: strings.TrimSpace(wire.Explanation),
		Risk:        ParseRisk(wire.Risk),
	}
	if s.Command == "" {
		return Suggestion{}, &NoCommandError{Reason: s.Explanation}
	}
	return s, nil
}

// extractObject returns the outermost {...} span in s.
func extractObject(s string) (string, error) {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return "", errors.New("model reply contains no JSON object: " + truncate(s, 200))
	}
	return s[start : end+1], nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
