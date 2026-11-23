package device

import (
	"strings"
)

func splitLines(s string) []string { return strings.Split(s, "\n") }

func indexOf(s, sub string) int { return strings.Index(s, sub) }

func trimSpace(s string) string  { return strings.TrimSpace(s) }
func hasPrefix(s, p string) bool { return strings.HasPrefix(s, p) }

func firstToken(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
