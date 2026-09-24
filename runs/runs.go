// Package runs cuts a string into maximal runs of equal code points and
// provides overflow-safe decimal count I/O for the RLE codec.
package runs

import "strconv"

// Run is one maximal run: Count consecutive copies of Sym.
type Run struct {
	Sym   rune
	Count uint64
}

// Split cuts s into the longest possible runs of identical code points.
// No Unicode normalization happens: é and e+U+0301 stay distinct.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Sym == r {
			out[n-1].Count++
		} else {
			out = append(out, Run{Sym: r, Count: 1})
		}
	}
	return out
}

// AppendCount writes n in decimal (no sign, no leading zeros) to dst.
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// AddDigit folds one decimal digit d (0-9) into n; ok is false on overflow.
func AddDigit(n, d uint64) (uint64, bool) {
	if n > (^uint64(0)-d)/10 {
		return 0, false
	}
	return n*10 + d, true
}
