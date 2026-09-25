// Package runs splits Unicode code points into maximal runs and provides
// canonical decimal run-length arithmetic. It has no dependencies on the
// other packages in this module.
package runs

import (
	"math/big"
)

// Run is one maximal sequence of equal code points.
type Run struct {
	Symbol rune
	Count  *big.Int
}

// Split partitions s into maximal runs of identical code points. Invalid
// UTF-8 bytes are treated as RuneError, like range over a string.
func Split(s string) []Run {
	if s == "" {
		return nil
	}
	var out []Run
	current := rune(-1)
	count := new(big.Int)
	for _, r := range s {
		if r == current {
			count.Add(count, big.NewInt(1))
			continue
		}
		if current >= 0 {
			out = append(out, Run{current, new(big.Int).Set(count)})
		}
		current = r
		count.SetInt64(1)
	}
	out = append(out, Run{current, new(big.Int).Set(count)})
	return out
}

// AppendCount appends the canonical decimal representation of n: no sign and
// no leading zeros. A count of one appends nothing because it is elided.
func AppendCount(dst []byte, n *big.Int) []byte {
	if n.Sign() <= 0 || n.Cmp(big.NewInt(1)) == 0 {
		return dst
	}
	return n.Append(dst, 10)
}

// Accumulator parses a stream of decimal digits into an unbounded count. It
// retains no input bytes: each digit is folded into the accumulator, so
// feeding digits one byte at a time costs O(digits) total work.
type Accumulator struct {
	n       *big.Int
	digits  int
	started bool
}

// AddDigit folds one ASCII digit into the count.
func (a *Accumulator) AddDigit(d byte) {
	if !a.started {
		a.n = new(big.Int)
		a.started = true
	}
	a.n.Mul(a.n, big.NewInt(10))
	a.n.Add(a.n, big.NewInt(int64(d-'0')))
	a.digits++
}

// Digits reports how many digits have been accumulated.
func (a *Accumulator) Digits() int { return a.digits }

// Value returns the accumulated count and true after at least one digit.
func (a *Accumulator) Value() (*big.Int, bool) {
	return a.n, a.started
}

// Reset returns the accumulator to its empty state.
func (a *Accumulator) Reset() {
	a.n = nil
	a.digits = 0
	a.started = false
}
