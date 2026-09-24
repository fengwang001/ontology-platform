// Package runs splits a sequence of Unicode code points into maximal runs and
// provides decimal read/write helpers for run counts. It has no dependencies
// beyond the standard library and does not depend on any other package here.
package runs

import (
	"errors"
	"math"
	"strconv"
)

// MaxCount is the largest representable run count. Decoded output is a Go
// string, so no longer run can ever be materialized; larger explicit counts
// are rejected rather than overflowing.
const MaxCount = math.MaxInt

// ErrCountTooLarge reports that a decimal count overflowed MaxCount.
var ErrCountTooLarge = errors.New("runs: count too large")

// Run is one maximal sequence of the same code point.
type Run struct {
	Symbol rune
	Count  int
}

// Split returns the maximal runs of s. Invalid UTF-8 is seen as utf8.RuneError
// by ranging, exactly like the encoder's scan.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Symbol == r {
			out[n-1].Count++
		} else {
			out = append(out, Run{Symbol: r, Count: 1})
		}
	}
	return out
}

// AppendCount appends the canonical (unsigned, no leading zeros) decimal form
// of c. Count 1 is never emitted by the caller's format, so c must be >= 1.
func AppendCount(b []byte, c int) []byte {
	return strconv.AppendInt(b, int64(c), 10)
}

// ParseCount parses a run of ASCII digit bytes. The empty string yields
// (1, nil): an omitted count means exactly one repetition. Any non-empty
// sequence is parsed with overflow detection; a value above MaxCount or a
// malformed/empty-number token (such as one containing only leading zeros is
// left to the caller) returns ErrCountTooLarge.
func ParseCount(digits []byte) (int, error) {
	if len(digits) == 0 {
		return 1, nil
	}
	var c int
	for _, d := range digits {
		digit := int(d - '0')
		if c > (MaxCount-digit)/10 {
			return 0, ErrCountTooLarge
		}
		c = c*10 + digit
	}
	return c, nil
}

// IsDigit reports whether b is an ASCII digit byte.
func IsDigit(b byte) bool { return b >= '0' && b <= '9' }
