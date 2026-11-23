package unlock

import "sextant-core/internal/ui"

// parseTestNodes parses a UI dump the same way the production path does.
func parseTestNodes(xmlText string) []ui.Node { return ui.Parse(xmlText) }
