package policy

// Shared derivation-rule primitives used by both the production engine and the
// naive reference implementation.

import (
	"fmt"
	"hash/fnv"
)

const redactedSentinel = "[REDACTED]"

func stableHash(raw any) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fmt.Sprintf("%v", raw)))
	return fmt.Sprintf("h:%016x", h.Sum64())
}

func maskString(raw any, keepRunes int) any {
	s, ok := raw.(string)
	if !ok {
		return redactedSentinel
	}
	runes := []rune(s)
	if keepRunes < 0 {
		keepRunes = 0
	}
	if keepRunes > len(runes) {
		keepRunes = len(runes)
	}
	return string(runes[:keepRunes]) + "***"
}
