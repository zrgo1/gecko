package cli

import (
	"runtime"

	"github.com/zrgo/gecko/internal/provider"
)

// tempRequest is a minimal stand-in so the provider can be exercised end to
// end. It is replaced by internal/prompt (task "Prompt builder"), which adds
// shell, cwd and installed-tool context.
func tempRequest(inv Invocation) provider.Request {
	system := `You turn a user's request into exactly one shell command for ` + runtime.GOOS + `.
Reply with only a JSON object, no prose:
{"command": "<shell command>", "explanation": "<one sentence>", "risk": "low" | "medium" | "high"}
If no command can do it, set "command" to "" and say why in "explanation".`

	user := inv.Query
	if inv.Hint != "" {
		user = "Prefer using `" + inv.Hint + "`.\n" + user
	}
	return provider.Request{System: system, User: user}
}
