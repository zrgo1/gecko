// Package config loads gecko settings and resolves the effective provider
// settings with precedence: flags > environment > config file > built-in defaults.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Environment variables gecko reads.
const (
	EnvConfig   = "GECKO_CONFIG"   // path to the config file
	EnvProvider = "GECKO_PROVIDER" // provider profile name
	EnvModel    = "GECKO_MODEL"
	EnvBaseURL  = "GECKO_BASE_URL"
	EnvShell    = "GECKO_SHELL"
)

// File mirrors config.toml.
type File struct {
	DefaultProvider string             `toml:"default_provider"`
	Shell           string             `toml:"shell"`
	Providers       map[string]Profile `toml:"providers"`
}

// Profile is one named provider entry, e.g. [providers.groq].
type Profile struct {
	Type      string `toml:"type"` // "openai" or "openai-compatible"
	Model     string `toml:"model"`
	BaseURL   string `toml:"base_url"`
	APIKeyEnv string `toml:"api_key_env"` // name of the env var holding the key (preferred)
	APIKey    string `toml:"api_key"`     // inline key; discouraged but supported
}

// Overrides are values from command-line flags. Empty means "not set".
type Overrides struct {
	Provider string
	Model    string
	BaseURL  string
	Shell    string
}

// Resolved is the final configuration used for a single run.
type Resolved struct {
	ProviderName string // profile name, e.g. "groq"
	Type         string // provider implementation, e.g. "openai-compatible"
	Model        string
	BaseURL      string // empty means the provider's default
	APIKey       string // may be empty (local servers often need none)
	Shell        string // empty means fall back to $SHELL at run time
}

// Default returns the built-in config used when no config file exists.
func Default() *File {
	return &File{
		DefaultProvider: "openai",
		Providers: map[string]Profile{
			"openai": {Type: "openai", Model: "gpt-4o-mini", APIKeyEnv: "OPENAI_API_KEY"},
		},
	}
}

// Path returns the config file location: $GECKO_CONFIG, else
// $XDG_CONFIG_HOME/gecko/config.toml, else ~/.config/gecko/config.toml.
// (os.UserConfigDir is avoided on purpose: on macOS it points at
// ~/Library/Application Support, which is unusual for CLI tools.)
func Path(getenv func(string) string) (string, error) {
	if p := getenv(EnvConfig); p != "" {
		return p, nil
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "gecko", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", "gecko", "config.toml"), nil
}

// Load reads the config file at path. A missing file yields Default() and
// found=false; any other read or parse error is returned. Unknown keys are
// rejected so typos (e.g. "api_key_evn") fail loudly.
func Load(path string) (cfg *File, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read config: %w", err)
	}

	cfg = &File{}
	md, err := toml.Decode(string(data), cfg)
	if err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", path, err)
	}
	if undec := md.Undecoded(); len(undec) > 0 {
		keys := make([]string, len(undec))
		for i, k := range undec {
			keys[i] = k.String()
		}
		return nil, true, fmt.Errorf("%s: unknown keys: %s", path, strings.Join(keys, ", "))
	}
	return cfg, true, nil
}

// Resolve merges file, environment and flag values into a Resolved config.
func Resolve(cfg *File, getenv func(string) string, ov Overrides) (Resolved, error) {
	name := first(ov.Provider, getenv(EnvProvider), cfg.DefaultProvider)
	if name == "" {
		return Resolved{}, errors.New("no provider selected: set default_provider in config, GECKO_PROVIDER, or --provider")
	}

	prof, ok := cfg.Providers[name]
	if !ok {
		return Resolved{}, fmt.Errorf("unknown provider %q (configured: %s)", name, available(cfg))
	}

	r := Resolved{
		ProviderName: name,
		Type:         first(prof.Type, "openai"),
		Model:        first(ov.Model, getenv(EnvModel), prof.Model),
		BaseURL:      first(ov.BaseURL, getenv(EnvBaseURL), prof.BaseURL),
		Shell:        first(ov.Shell, getenv(EnvShell), cfg.Shell),
	}
	if prof.APIKeyEnv != "" {
		r.APIKey = getenv(prof.APIKeyEnv)
	}
	r.APIKey = first(r.APIKey, prof.APIKey)

	if r.Model == "" {
		return Resolved{}, fmt.Errorf("provider %q has no model: set model in config, GECKO_MODEL, or --model", name)
	}
	return r, nil
}

// String renders r for verbose output with the API key masked.
func (r Resolved) String() string {
	return fmt.Sprintf("provider=%s type=%s model=%s base_url=%s shell=%s api_key=%s",
		r.ProviderName, r.Type, r.Model, orDash(r.BaseURL), orDash(r.Shell), mask(r.APIKey))
}

func first(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func available(cfg *File) string {
	if len(cfg.Providers) == 0 {
		return "none"
	}
	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func mask(key string) string {
	switch {
	case key == "":
		return "(unset)"
	case len(key) <= 8:
		return "****"
	default:
		return key[:3] + "…" + key[len(key)-4:]
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
