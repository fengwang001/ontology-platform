// Package runs splits rune sequences into maximal runs and performs
// overflow-free decimal I/O of repetition counts.
package runs

import (
	"io"
	"math/big"
	"strconv"
	"unicode/utf8"
)

// Run is a maximal run of Count copies of the code point Sym.
type Run struct {
	Sym   rune
	Count uint64
}

// Split cuts s into maximal runs of identical code points.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Sym == r {
			out[n-1].Count++
		} else {
			out = append(out, Run{r, 1})
		}
	}
	return out
}

// AppendCount appends the decimal form of n: unsigned, no leading zeros.
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// ParseCount parses decimal digits into a big.Int; it cannot overflow.
func ParseCount(digits string) *big.Int {
	n, _ := new(big.Int).SetString(digits, 10)
	return n
}

// Expand writes n copies of sym to w in bounded-size chunks, so memory
// use never grows with n.
func Expand(w io.Writer, sym rune, n *big.Int) error {
	b := utf8.AppendRune(nil, sym)
	size := int64(len(b))
	for len(b) < 1<<13 {
		b = append(b, b...)
	}
	per := big.NewInt(int64(len(b)) / size)
	left := new(big.Int).Set(n)
	for left.Cmp(per) >= 0 {
		if _, err := w.Write(b); err != nil {
			return err
		}
		left.Sub(left, per)
	}
	if left.Sign() > 0 {
		_, err := w.Write(b[:left.Int64()*size])
		return err
	}
	return nil
}
