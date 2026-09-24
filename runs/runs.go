// Package runs splits rune sequences into maximal runs and reads/writes
// decimal run counts without overflow.
package runs

import (
	"errors"
	"math/big"
	"strconv"
)

// Count-syntax errors returned by ParseCount.
var (
	ErrExplicitOne = errors.New("runs: explicit count 1")
	ErrZeroCount   = errors.New("runs: zero count")
	ErrLeadingZero = errors.New("runs: leading zero in count")
)

// Run is a maximal run of N copies of Sym.
type Run struct {
	Sym rune
	N   uint64
}

// Split cuts s into maximal runs of identical code points. No Unicode
// normalization is performed: U+00E9 and "e"+U+0301 stay distinct.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Sym == r {
			out[n-1].N++
		} else {
			out = append(out, Run{Sym: r, N: 1})
		}
	}
	return out
}

// AppendCount writes n in canonical decimal (no sign, no leading zeros).
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// ParseCount parses canonical count digits. An empty string means the
// omitted count 1. The value may exceed int64; big.Int cannot overflow.
func ParseCount(d string) (*big.Int, error) {
	if d == "" {
		return big.NewInt(1), nil
	}
	if d[0] == '0' {
		if len(d) > 1 {
			return nil, ErrLeadingZero
		}
		return nil, ErrZeroCount
	}
	if d == "1" {
		return nil, ErrExplicitOne
	}
	n, _ := new(big.Int).SetString(d, 10) // d is all digits by contract
	return n, nil
}
