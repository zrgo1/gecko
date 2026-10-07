package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

const sample = `
default_provider = "openai"
shell = "/bin/zsh"

[providers.openai]
type = "openai"
model = "gpt-4o-mini"
api_key_env = "OPENAI_API_KEY"

[providers.groq]
type = "openai-compatible"
base_url = "https://api.groq.com/openai/v1"
model = "llama-3.3-70b-versatile"
api_key_env = "GROQ_API_KEY"

[providers.local]
type = "openai-compatible"
base_url = "http://localhost:1234/v1"
model = "qwen2.5-coder"
`

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustLoad(t *testing.T, body string) *File {
	t.Helper()
	cfg, found, err := Load(writeConfig(t, body))
	if err != nil || !found {
		t.Fatalf("Load: found=%v err=%v", found, err)
	}
	return cfg
}

func TestLoadMissingFileUsesDefault(t *testing.T) {
	cfg, found, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if cfg.DefaultProvider != "openai" || cfg.Providers["openai"].Model == "" {
		t.Fatalf("unexpected default: %+v", cfg)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	_, _, err := Load(writeConfig(t, "[providers.openai]\nmodel = \"x\"\napi_key_evn = \"K\"\n"))
	if err == nil || !strings.Contains(err.Error(), "api_key_evn") {
		t.Fatalf("want unknown-key error, got %v", err)
	}
}

func TestLoadParseError(t *testing.T) {
	if _, _, err := Load(writeConfig(t, "default_provider = \n")); err == nil {
		t.Fatal("want parse error")
	}
}

func TestResolvePrecedence(t *testing.T) {
	cfg := mustLoad(t, sample)

	tests := []struct {
		name string
		env  map[string]string
		ov   Overrides
		want Resolved
	}{
		{
			name: "file defaults",
			env:  map[string]string{"OPENAI_API_KEY": "sk-file"},
			want: Resolved{ProviderName: "openai", Type: "openai", Model: "gpt-4o-mini", APIKey: "sk-file", Shell: "/bin/zsh"},
		},
		{
			name: "env beats file",
			env:  map[string]string{EnvProvider: "groq", EnvModel: "env-model", EnvShell: "/bin/bash", "GROQ_API_KEY": "gsk"},
			want: Resolved{ProviderName: "groq", Type: "openai-compatible", Model: "env-model",
				BaseURL: "https://api.groq.com/openai/v1", APIKey: "gsk", Shell: "/bin/bash"},
		},
		{
			name: "flags beat env",
			env:  map[string]string{EnvProvider: "groq", EnvModel: "env-model", EnvBaseURL: "http://env"},
			ov:   Overrides{Provider: "local", Model: "flag-model", BaseURL: "http://flag", Shell: "/bin/sh"},
			want: Resolved{ProviderName: "local", Type: "openai-compatible", Model: "flag-model",
				BaseURL: "http://flag", Shell: "/bin/sh"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(cfg, env(tt.env), tt.ov)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("\ngot  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestResolveAPIKey(t *testing.T) {
	cfg := &File{DefaultProvider: "p", Providers: map[string]Profile{
		"p": {Model: "m", APIKeyEnv: "MY_KEY", APIKey: "inline"},
	}}
	if r, _ := Resolve(cfg, env(map[string]string{"MY_KEY": "from-env"}), Overrides{}); r.APIKey != "from-env" {
		t.Fatalf("env key should win, got %q", r.APIKey)
	}
	if r, _ := Resolve(cfg, env(nil), Overrides{}); r.APIKey != "inline" {
		t.Fatalf("inline key fallback, got %q", r.APIKey)
	}
}

func TestResolveDefaultsTypeToOpenAI(t *testing.T) {
	cfg := &File{DefaultProvider: "p", Providers: map[string]Profile{"p": {Model: "m"}}}
	r, err := Resolve(cfg, env(nil), Overrides{})
	if err != nil || r.Type != "openai" {
		t.Fatalf("type=%q err=%v", r.Type, err)
	}
}

func TestResolveErrors(t *testing.T) {
	cfg := &File{DefaultProvider: "a", Providers: map[string]Profile{"a": {}, "b": {Model: "m"}}}

	tests := []struct {
		name, contains string
		cfg            *File
		ov             Overrides
	}{
		{"unknown provider", `unknown provider "zzz" (configured: a, b)`, cfg, Overrides{Provider: "zzz"}},
		{"missing model", `provider "a" has no model`, cfg, Overrides{}},
		{"no provider", "no provider selected", &File{}, Overrides{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Resolve(tt.cfg, env(nil), tt.ov)
			if err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("want error containing %q, got %v", tt.contains, err)
			}
		})
	}
}

func TestPath(t *testing.T) {
	if p, _ := Path(env(map[string]string{EnvConfig: "/x/c.toml", "XDG_CONFIG_HOME": "/xdg"})); p != "/x/c.toml" {
		t.Fatalf("GECKO_CONFIG should win, got %s", p)
	}
	if p, _ := Path(env(map[string]string{"XDG_CONFIG_HOME": "/xdg"})); p != "/xdg/gecko/config.toml" {
		t.Fatalf("XDG path, got %s", p)
	}
	if p, _ := Path(env(nil)); !strings.HasSuffix(p, filepath.Join(".config", "gecko", "config.toml")) {
		t.Fatalf("home path, got %s", p)
	}
}

func TestMask(t *testing.T) {
	for in, want := range map[string]string{"": "(unset)", "short": "****", "sk-abcdefghijkl": "sk-…ijkl"} {
		if got := mask(in); got != want {
			t.Errorf("mask(%q) = %q, want %q", in, got, want)
		}
	}
}
