package provider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseSuggestionJSONStrings(t *testing.T) {
	got, err := ParseSuggestion("```json\n" + `{"command":"  printf '%s' '{\"key\":\"value\"}'\n","explanation":"  prints {JSON}  ","risk":"unrated"}` + "\n```")
	want := Suggestion{Command: `printf '%s' '{"key":"value"}'`, Explanation: "prints {JSON}", Risk: RiskUnknown}
	if err != nil || got != want {
		t.Fatalf("ParseSuggestion = %+v, %v; want %+v", got, err, want)
	}
}

func TestParseSuggestionJSONFieldTypes(t *testing.T) {
	for _, raw := range []string{
		`{"command":42}`,
		`{"command":"ls","explanation":[]}`,
		`{"command":"ls","risk":true}`,
	} {
		_, err := ParseSuggestion(raw)
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) || !strings.Contains(err.Error(), "decode model reply") {
			t.Fatalf("ParseSuggestion(%s) = %v, want wrapped JSON type error", raw, err)
		}
	}
}

func TestParseSuggestionMissingCommand(t *testing.T) {
	for _, raw := range []string{`{}`, `{"command":null}`, `{"command":" \t\n "}`} {
		got, err := ParseSuggestion(raw)
		var refusal *NoCommandError
		if got != (Suggestion{}) || !errors.As(err, &refusal) || refusal.Error() != "model returned no command" {
			t.Fatalf("ParseSuggestion(%s) = %+v, %v", raw, got, err)
		}
	}
}

func TestParseSuggestionMissingOptionalFields(t *testing.T) {
	got, err := ParseSuggestion(`{"command":"ls"}`)
	if err != nil || got != (Suggestion{Command: "ls", Risk: RiskUnknown}) {
		t.Fatalf("ParseSuggestion = %+v, %v", got, err)
	}
}
