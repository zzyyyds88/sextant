package engine

import (
	"strings"

	"chopin-core/internal/token"
)

// isErr reports whether err's text mentions substr. The unlock package returns
// sentinel errors; this avoids importing them into the HTTP layer while keeping
// the mapping in one place.
func isErr(err error, substr string) bool {
	return err != nil && strings.Contains(err.Error(), substr)
}

func tokenDefaultScopes() []string { return token.DefaultScopes() }
