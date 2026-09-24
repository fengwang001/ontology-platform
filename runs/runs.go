// Package runs splits a sequence of Unicode code points into maximal runs
// and reads/writes decimal run counts without silent overflow.
package runs

import (
	"errors"
	"math"
	"strconv"
)

// MaxCount is the largest representable run count on this platform.
// Go values cannot be longer than math.MaxInt bytes, so no larger run can
// ever be produced.
const MaxCount uint64 = math.MaxInt

// ErrCountTooLarge reports a decimal count greater than MaxCount.
var ErrCountTooLarge = errors.New("runs: run count exceeds MaxInt")

// Run is one maximal run: Sym repeated Count times.
type Run struct {
	Sym   rune
	Count uint64
}

// Split breaks s into maximal runs of equal code points. A count larger
// than MaxCount cannot occur: s itself is bounded by MaxInt bytes.
func Split(s string, emit func(Run)) {
	var cur rune
	var n uint64
	flush := func() {
		if n > 0 {
			emit(Run{cur, n})
		}
	}
	for _, r := range s {
		if n > 0 && r == cur {
			n++
			continue
		}
		flush()
		cur, n = r, 1
	}
	flush()
}

// AppendCount appends the unsigned, no-leading-zero decimal form of n.
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// ParseCount parses a non-empty run of ASCII decimal digits. Leading zeros
// are tolerated here; canonical-form checks belong to the caller. It returns
// ErrCountTooLarge instead of overflowing.
func ParseCount(s string) (uint64, error) {
	if s == "" {
		return 0, errors.New("runs: empty count")
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		d := uint64(s[i] - '0')
		if d > 9 {
			return 0, errors.New("runs: non-digit in count")
		}
		if n > (MaxCount-d)/10 {
			return 0, ErrCountTooLarge
		}
		n = n*10 + d
	}
	return n, nil
}
