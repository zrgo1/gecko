package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fake struct{ cfg Config }

func (f *fake) Suggest(context.Context, Request) (Suggestion, error) {
	return Suggestion{Command: "echo " + f.cfg.Model}, nil
}

func fakeFactory(cfg Config) (Provider, error) { return &fake{cfg: cfg}, nil }

func TestRegistryNew(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeFactory, "openai", "openai-compatible")

	for _, typ := range []string{"openai", "openai-compatible"} {
		p, err := r.New(typ, Config{Name: "x", Model: "m"})
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		s, _ := p.Suggest(context.Background(), Request{})
		if s.Command != "echo m" {
			t.Fatalf("%s: config not passed through, got %q", typ, s.Command)
		}
	}
}

func TestRegistryUnknownType(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeFactory, "openai-compatible", "openai")

	_, err := r.New("anthropic", Config{Name: "claude"})
	want := `provider "claude": unknown type "anthropic" (supported: openai, openai-compatible)`
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
}

func TestRegistryFactoryError(t *testing.T) {
	r := NewRegistry()
	r.Register(func(Config) (Provider, error) { return nil, errors.New("missing API key") }, "openai")

	_, err := r.New("openai", Config{Name: "work"})
	if err == nil || err.Error() != `provider "work": missing API key` {
		t.Fatalf("got %v", err)
	}
}

func TestRegistryPanics(t *testing.T) {
	cases := map[string]func(r *Registry){
		"duplicate": func(r *Registry) { r.Register(fakeFactory, "a"); r.Register(fakeFactory, "a") },
		"empty":     func(r *Registry) { r.Register(fakeFactory, "") },
		"nil":       func(r *Registry) { r.Register(nil, "a") },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			fn(NewRegistry())
		})
	}
}

func TestParseRisk(t *testing.T) {
	for in, want := range map[string]Risk{
		"low": RiskLow, " HIGH ": RiskHigh, "Medium": RiskMedium, "": RiskUnknown, "spicy": RiskUnknown,
	} {
		if got := ParseRisk(in); got != want {
			t.Errorf("ParseRisk(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSuggestion(t *testing.T) {
	want := Suggestion{Command: "ls t*", Explanation: "lists files", Risk: RiskLow}

	for name, raw := range map[string]string{
		"plain":       `{"command":"ls t*","explanation":"lists files","risk":"low"}`,
		"fenced":      "```json\n{\"command\": \"ls t*\", \"explanation\": \"lists files\", \"risk\": \"LOW\"}\n```",
		"with prose":  "Sure! Here you go:\n{\"command\":\"  ls t* \",\"explanation\":\"lists files\",\"risk\":\"low\"}\nHope that helps.",
		"extra field": `{"command":"ls t*","explanation":"lists files","risk":"low","confidence":0.9}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseSuggestion(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestParseSuggestionErrors(t *testing.T) {
	t.Run("no json", func(t *testing.T) {
		_, err := ParseSuggestion("I can't help with that.")
		if err == nil || !strings.Contains(err.Error(), "no JSON object") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("bad json", func(t *testing.T) {
		if _, err := ParseSuggestion(`{"command": }`); err == nil {
			t.Fatal("want decode error")
		}
	})
	t.Run("empty command carries reason", func(t *testing.T) {
		_, err := ParseSuggestion(`{"command":"","explanation":"needs a running GUI"}`)
		var nc *NoCommandError
		if !errors.As(err, &nc) || nc.Reason != "needs a running GUI" {
			t.Fatalf("got %v", err)
		}
	})
}
