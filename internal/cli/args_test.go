package cli

import (
	"errors"
	"testing"
)

func fakeLookPath(installed ...string) lookPathFunc {
	set := map[string]bool{}
	for _, s := range installed {
		set[s] = true
	}
	return func(name string) (string, error) {
		if set[name] {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
}

func TestParseArgs(t *testing.T) {
	lp := fakeLookPath("grep", "find", "git")

	tests := []struct {
		name    string
		args    []string
		want    Invocation
		wantErr bool
	}{
		{"hint + quoted query", []string{"grep", "files starting with t"}, Invocation{"grep", "files starting with t"}, false},
		{"hint + unquoted query", []string{"find", "empty", "dirs"}, Invocation{"find", "empty dirs"}, false},
		{"single quoted query", []string{"show disk usage"}, Invocation{"", "show disk usage"}, false},
		{"lone tool word is a query", []string{"git"}, Invocation{"", "git"}, false},
		{"unknown first word", []string{"show", "me", "ports"}, Invocation{"", "show me ports"}, false},
		{"path-like first word", []string{"/usr/bin/grep", "x"}, Invocation{"", "/usr/bin/grep x"}, false},
		{"hint with empty query", []string{"grep", "  "}, Invocation{}, true},
		{"blank query", []string{"   "}, Invocation{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args, lp)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
