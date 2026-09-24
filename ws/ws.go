// Package ws performs deferred trailing-whitespace classification.
// A run of spaces/tabs is trailing only when followed by a line ending or
// EOF; until then it must stay buffered.
package ws

// IsWS reports whether b is a deferrable trailing-whitespace byte.
func IsWS(b byte) bool { return b == ' ' || b == '\t' }

// Trailing reports whether a buffered run is trailing given the decider that
// follows it: a line ending byte or EOF (b < 0).
func Trailing(b int) bool { return b < 0 || b == '\n' || b == '\r' }
