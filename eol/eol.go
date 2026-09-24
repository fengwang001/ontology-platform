// Package eol identifies line endings: CRLF, lone CR, and LF.
// A trailing CR is "pending" until the next byte decides CR vs CRLF.
package eol

const (
	CR byte = '\r'
	LF byte = '\n'
)

// Pair reports whether a pending CR followed by b forms a CRLF pair.
func Pair(b byte) bool { return b == LF }

// Lone reports that a flushed CR starts its own line ending because b is
// neither the LF of CRLF nor absent at a cut boundary (b < 0 means EOF/cut).
func Lone(b int) bool { return b != int(LF) }

// Pending reports whether b is a CR whose matching LF has not arrived.
func Pending(b byte) bool { return b == CR }

// IsEnd reports whether b alone is a line-ending byte.
func IsEnd(b byte) bool { return b == CR || b == LF }
