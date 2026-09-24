// Package runs splits a string into maximal runs of identical code points
// and reads and writes decimal run counts without overflow.
package runs

import (
	"errors"
	"math/big"
	"strconv"
)

// Run is a maximal run of N copies of the code point Sym.
type Run struct {
	Sym rune
	N   uint64
}

// Split returns the maximal runs of s in order. No Unicode normalization
// is performed: runs are over code points exactly as encoded.
func Split(s string) []Run {
	var out []Run
	var prev rune
	var n uint64
	for _, r := range s {
		if n > 0 && r == prev {
			n++
			continue
		}
		if n > 0 {
			out = append(out, Run{prev, n})
		}
		prev, n = r, 1
	}
	if n > 0 {
		out = append(out, Run{prev, n})
	}
	return out
}

// Count-canonicity errors: a canonical count has no leading zeros and is
// neither zero nor one (a count of one is omitted in the encoding).
var (
	ErrLeadingZero = errors.New("leading zero in count")
	ErrZeroCount   = errors.New("zero count")
	ErrExplicitOne = errors.New("explicit count 1")
)

// IsDigit reports whether r is an ASCII digit.
func IsDigit(r rune) bool { return '0' <= r && r <= '9' }

// AppendCount appends the decimal form of n: unsigned, no leading zeros.
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// ParseCount parses a canonical decimal count: nonempty ASCII digits, no
// leading zeros, value at least 2. The big integer result never overflows,
// so callers must bound it before expanding.
func ParseCount(digits string) (*big.Int, error) {
	switch {
	case len(digits) > 1 && digits[0] == '0':
		return nil, ErrLeadingZero
	case digits == "0":
		return nil, ErrZeroCount
	case digits == "1":
		return nil, ErrExplicitOne
	}
	n, _ := new(big.Int).SetString(digits, 10)
	return n, nil
}
