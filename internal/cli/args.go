package cli

import (
	"errors"
	"strings"
)

// Invocation is the parsed positional input: an optional tool hint and the
// natural-language query.
type Invocation struct {
	Hint  string // e.g. "grep"; empty when no hint was given
	Query string
}

// lookPathFunc matches exec.LookPath so tests can stub tool detection.
type lookPathFunc func(string) (string, error)

// parseArgs splits positional args into a hint and a query.
//
// The first arg is treated as a tool hint only when more args follow and it
// names an executable on PATH. That keeps unquoted queries working:
//
//	gecko grep "files starting with t"   -> hint=grep
//	gecko show me disk usage             -> no hint ("show" is not on PATH)
func parseArgs(args []string, lookPath lookPathFunc) (Invocation, error) {
	var inv Invocation
	rest := args
	if len(args) >= 2 && isToolHint(args[0], lookPath) {
		inv.Hint = args[0]
		rest = args[1:]
	}
	inv.Query = strings.TrimSpace(strings.Join(rest, " "))
	if inv.Query == "" {
		return Invocation{}, errors.New("empty query")
	}
	return inv, nil
}

func isToolHint(s string, lookPath lookPathFunc) bool {
	if s == "" || strings.ContainsAny(s, " \t/") {
		return false
	}
	_, err := lookPath(s)
	return err == nil
}
