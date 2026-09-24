// Package runs provides maximal codepoint-run splitting and overflow-safe
// decimal run-count arithmetic for the rle package.
package runs

import (
	"errors"
	"math"
	"strconv"
	"unicode/utf8"
)

// MaxCount is the largest accepted run repetition count.
const MaxCount = uint64(math.MaxInt64)

// ErrCountTooLarge reports that a decimal count exceeds MaxCount.
var ErrCountTooLarge = errors.New("runs: run count exceeds " +
	strconv.FormatUint(MaxCount, 10))

// Run is one maximal sequence of equal codepoints.
type Run struct {
	Symbol rune
	Count  uint64
}

// Split cuts s into maximal runs of equal codepoints. Codepoints are decoded
// strictly; invalid UTF-8 yields utf8.RuneError runs rather than panicking.
func Split(s string) []Run {
	var out []Run
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		if n := len(out); n > 0 && out[n-1].Symbol == r {
			out[n-1].Count++
		} else {
			out = append(out, Run{Symbol: r, Count: 1})
		}
	}
	return out
}

// AppendCount appends the canonical decimal form of count (no sign, no
// leading zeros; count 1 appends nothing) to b.
func AppendCount(b []byte, count uint64) []byte {
	if count == 1 {
		return b
	}
	return strconv.AppendUint(b, count, 10)
}

// PushDigit appends one decimal digit to a count being parsed, rejecting
// overflow beyond MaxCount.
func PushDigit(value uint64, digit byte) (uint64, error) {
	d := uint64(digit - '0')
	if value > (MaxCount-d)/10 {
		return 0, ErrCountTooLarge
	}
	next := value*10 + d
	if next > MaxCount {
		return 0, ErrCountTooLarge
	}
	return next, nil
}
