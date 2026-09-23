// Package dist computes the unrestricted Damerau-Levenshtein distance
// (Lowrance-Wagner) between two strings, by code point, with unit costs
// for insertion, deletion, substitution and adjacent transposition.
package dist

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// ErrTooLarge reports that the product of the two rune counts exceeds the
// configured limit; it is returned before any DP matrix is allocated.
var ErrTooLarge = errors.New("dist: rune-count product exceeds limit")

var (
	filledCells int64
	maxProduct  = 1 << 22
)

// FilledCells returns the number of DP cells filled so far.
func FilledCells() int64 { return filledCells }

// ResetFilledCells zeroes the DP-cell counter.
func ResetFilledCells() { filledCells = 0 }

// SetMaxProduct sets the rune-count product limit and returns the old one.
func SetMaxProduct(n int) int {
	old := maxProduct
	maxProduct = n
	return old
}

// Decode splits s into code points. Each invalid UTF-8 byte becomes a
// distinct synthetic code point 0x110000+byte, above any legal rune.
func Decode(s string) []rune {
	rs := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			r = 0x110000 + rune(s[i])
		}
		rs = append(rs, r)
		i += size
	}
	return rs
}

// Encode is the inverse of Decode: synthetic code points become bytes again.
func Encode(rs []rune) string {
	var b strings.Builder
	b.Grow(len(rs))
	for _, r := range rs {
		if r >= 0x110000 && r <= 0x1100FF {
			b.WriteByte(byte(r - 0x110000))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Distance returns the unrestricted transposition distance between a and b.
func Distance(a, b string) (int, error) {
	d, err := Table(a, b)
	if err != nil {
		return 0, err
	}
	last := d[len(d)-1]
	return last[len(last)-1], nil
}

// Table computes the full (m+1)x(n+1) DP matrix; row i, column j holds the
// distance between the first i code points of a and the first j of b.
func Table(a, b string) ([][]int, error) {
	ra, rb := Decode(a), Decode(b)
	m, n := len(ra), len(rb)
	if m*n > maxProduct {
		return nil, ErrTooLarge
	}
	d := make([][]int, m+1)
	for i := range d {
		d[i] = make([]int, n+1)
	}
	for i := 0; i <= m; i++ {
		d[i][0] = i
	}
	for j := 0; j <= n; j++ {
		d[0][j] = j
	}
	filledCells += int64((m + 1) * (n + 1))
	da := make(map[rune]int) // last row where each code point was seen in a
	for i := 1; i <= m; i++ {
		db := 0 // last column where a[i-1] was seen in b
		for j := 1; j <= n; j++ {
			i1, j1 := da[rb[j-1]], db
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost, db = 0, j
			}
			best := min(d[i-1][j-1]+cost, d[i-1][j]+1, d[i][j-1]+1)
			if i1 > 0 && j1 > 0 {
				best = min(best, d[i1-1][j1-1]+(i-i1-1)+1+(j-j1-1))
			}
			d[i][j] = best
		}
		da[ra[i-1]] = i
	}
	return d, nil
}
