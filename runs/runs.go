// Package runs provides run-length segmentation over Unicode code points
// and decimal read/write helpers for non-negative run counts.
package runs

import (
	"errors"
	"math"
	"strconv"
	"unicode/utf8"
)

// MaxCount is the largest representable run count. Counts are carried by
// int64 so that no integer overflow can silently corrupt a decoded run.
const MaxCount = math.MaxInt64

var (
	// ErrCountTooLarge reports that a decimal count exceeds MaxCount.
	ErrCountTooLarge = errors.New("runs: count exceeds MaxInt64")
	// ErrLeadingZero reports that a non-zero decimal count starts with '0'.
	ErrLeadingZero = errors.New("runs: count has a leading zero")
	// ErrCountZero reports the decimal count "0".
	ErrCountZero = errors.New("runs: count is zero")
	// ErrEmptyCount reports an empty or zero-length decimal count.
	ErrEmptyCount = errors.New("runs: empty count")
)

// Run is one maximal run of identical code points.
type Run struct {
	Symbol rune
	Count  int64
}

// Split cuts s into maximal runs of equal code points.
// Invalid UTF-8 bytes are treated as RuneError runs byte by byte; callers
// that need strict validity must validate the input beforehand.
func Split(s string) []Run {
	if s == "" {
		return nil
	}
	var out []Run
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		j := i + size
		for j < len(s) {
			next, n := utf8.DecodeRuneInString(s[j:])
			if next != r {
				break
			}
			j += n
		}
		out = append(out, Run{Symbol: r, Count: int64((j - i) / size)})
		i = j
	}
	return out
}

// AppendCount appends the canonical decimal form of n (n >= 1):
// unsigned, no leading zeros; 1 appends nothing.
func AppendCount(buf []byte, n int64) []byte {
	if n <= 1 {
		return buf
	}
	return strconv.AppendInt(buf, n, 10)
}

// ParseCount parses a decimal count of 1..MaxCount.
// It rejects empty input, the value 0, leading zeros and overflow without
// relying on wraparound arithmetic.
func ParseCount(p string) (int64, error) {
	if len(p) == 0 {
		return 0, ErrEmptyCount
	}
	if len(p) > 1 && p[0] == '0' {
		return 0, ErrLeadingZero
	}
	n, err := strconv.ParseInt(p, 10, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, ErrCountTooLarge
		}
		return 0, err
	}
	if n == 0 {
		return 0, ErrCountZero
	}
	return n, nil
}
