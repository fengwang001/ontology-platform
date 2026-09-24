// Package runs provides run-level primitives for the rle package: splitting
// rune sequences into maximal runs, overflow-safe decimal count I/O, and
// reading/writing single runs. It does not depend on package rle.
package runs

import (
	"bytes"
	"errors"
	"io"
	"math"
	"strconv"
	"unicode/utf8"
)

// ErrOverflow reports a decimal count that does not fit in a uint64.
var ErrOverflow = errors.New("runs: count overflows uint64")

// ErrLeadingZero reports a decimal count with a leading zero.
var ErrLeadingZero = errors.New("runs: leading zero in count")

// Run is a maximal run of N copies of the rune Sym.
type Run struct {
	Sym rune
	N   uint64
}

// Split cuts s into maximal runs of identical runes. No Unicode
// normalization is performed: runes are compared by code point.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out) - 1; n >= 0 && out[n].Sym == r {
			out[n].N++
		} else {
			out = append(out, Run{Sym: r, N: 1})
		}
	}
	return out
}

// AppendCount appends the canonical decimal form of n (no leading zeros).
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// AddDigit folds one decimal digit d ('0'..'9') into n. n == 0 means a zero
// digit was already folded in, so any further digit is a leading zero.
// Returns an error instead of silently accepting non-canonical counts.
func AddDigit(n uint64, d byte) (uint64, error) {
	if n == 0 {
		return 0, ErrLeadingZero
	}
	if n > (math.MaxUint64-uint64(d-'0'))/10 {
		return 0, ErrOverflow
	}
	return n*10 + uint64(d-'0'), nil
}

// AppendRun appends the canonical encoding of one run: the count if n > 1,
// then the symbol, backslash-escaped if it is a digit or a backslash.
func AppendRun(dst []byte, sym rune, n uint64) []byte {
	if n > 1 {
		dst = AppendCount(dst, n)
	}
	if sym == '\\' || '0' <= sym && sym <= '9' {
		dst = append(dst, '\\')
	}
	return utf8.AppendRune(dst, sym)
}

// WriteRun writes n copies of sym to w in bounded-size chunks, so memory
// use stays constant no matter how large n is.
func WriteRun(w io.Writer, sym []byte, n uint64) error {
	for k := n; n > 0; n -= k {
		k = min(n, uint64(8192/len(sym)))
		if _, err := w.Write(bytes.Repeat(sym, int(k))); err != nil {
			return err
		}
	}
	return nil
}
