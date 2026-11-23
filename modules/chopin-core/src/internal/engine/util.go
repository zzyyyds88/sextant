package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func jsonMarshalIndent(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return append(b, '\n'), nil
}

// lastComponentLine returns the last pkg/.Cls line of a `cmd package
// resolve-activity --brief` dump.
//
// The command prints the resolved component on the final line, but a dump can be
// preceded by nothing at all, by warnings, or by nothing resolvable — so parsing
// it has to tolerate all three rather than assume the last line is the answer.
func lastComponentLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.ContainsAny(t, " \t=") {
			continue
		}
		if strings.Contains(t, "/") {
			return t
		}
	}
	return ""
}
