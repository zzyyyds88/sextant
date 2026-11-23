package engine

import "chopin-core/internal/hud"

// hudEncode renders a HUD state as an SSE frame.
func hudEncode(st hud.State) []byte { return hud.Encode(st) }
