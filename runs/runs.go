// Package runs splits a sequence of Unicode code points into maximal runs and
// reads/writes unsigned decimal run lengths without integer overflow.
package runs

import (
	"errors"
	"math"
	"strconv"
)

// MaxCount is the largest accepted run length.
const MaxCount = math.MaxInt64

var (
	// ErrCountTooLarge reports a decimal count greater than MaxCount.
	ErrCountTooLarge = errors.New("runs: count exceeds MaxCount")
	// ErrEmptyCount reports an empty decimal count where digits were required.
	ErrEmptyCount = errors.New("runs: empty count")
)

// Run is one maximal run of a single code point.
type Run struct {
	Symbol rune
	Count  int64
}

// AppendCount appends the unsigned decimal form of n (n >= 0) to b.
func AppendCount(b []byte, n int64) []byte { return strconv.AppendInt(b, n, 10) }

// ParseCount parses decimal digits as a count in [0, MaxCount].
func ParseCount(digits []byte) (int64, error) {
	var n int64
	if len(digits) == 0 {
		return 0, ErrEmptyCount
	}
	for _, c := range digits {
		d := int64(c - '0')
		if d < 0 || d > 9 {
			return 0, ErrCountTooLarge
		}
		if n > (MaxCount-d)/10 {
			return 0, ErrCountTooLarge
		}
		n = n*10 + d
	}
	return n, nil
}

// Scan splits s into maximal runs. Invalid UTF-8 becomes RuneError runs.
func Scan(s string) []Run {
	if s == "" {
		return nil
	}
	out := make([]Run, 0, 1)
	for _, r := range s {
		if len(out) > 0 && out[len(out)-1].Symbol == r {
			out[len(out)-1].Count++
			continue
		}
		out = append(out, Run{Symbol: r, Count: 1})
	}
	return out
}
