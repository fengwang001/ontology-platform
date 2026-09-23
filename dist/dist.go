// Package dist computes the unrestricted Damerau–Levenshtein distance between
// strings compared code point by code point. See DESIGN.md.
package dist

import (
	"errors"
	"unicode/utf8"
)

// ErrLimitExceeded is returned before any DP allocation when
// len([]rune(a))*len([]rune(b)) exceeds the configured product limit.
var ErrLimitExceeded = errors.New("dist: code point product limit exceeded")

// Source labels recorded for backtracking.
const (
	SrcMatch     = 0
	SrcReplace   = 1
	SrcDelete    = 2
	SrcInsert    = 3
	SrcTranspose = 4
)

// Table is a filled dynamic-programming table plus bookkeeping.
type Table struct {
	d, kk, ll [][]int // distances; transposition k,l per cell
	from      [][]uint8
	a, b      []rune
	m, n      int
	cell      int64 // number of DP cells filled (unexported counter)
}

// Build decodes a,b into code points and fills the DP table. The code-point
// product is checked against maxProduct before any matrix is allocated.
func Build(a, b string, maxProduct int64) (*Table, error) {
	ra, rb := DecodeRunes(a), DecodeRunes(b)
	if int64(len(ra))*int64(len(rb)) > maxProduct {
		return nil, ErrLimitExceeded
	}
	m, n := len(ra), len(rb)
	aa := make([]rune, m+1) // 1-indexed
	copy(aa[1:], ra)
	bb := make([]rune, n+1)
	copy(bb[1:], rb)
	d := make([][]int, m+1)
	kk, ll, from := make([][]int, m+1), make([][]int, m+1), make([][]uint8, m+1)
	for i := 0; i <= m; i++ {
		d[i] = make([]int, n+1)
		kk[i], ll[i], from[i] = make([]int, n+1), make([]int, n+1), make([]uint8, n+1)
		d[i][0], from[i][0] = i, SrcDelete
	}
	for j := 0; j <= n; j++ {
		d[0][j], from[0][j] = j, SrcInsert
	}
	from[0][0] = SrcMatch
	lastA := map[rune]int{} // last row index at which b[j]'s rune appeared in a
	for i := 1; i <= m; i++ {
		lastB := map[rune]int{} // last column index at which a[i]'s rune appeared in b
		for j := 1; j <= n; j++ {
			sub := d[i-1][j-1]
			best, src := sub, SrcMatch
			if aa[i] != bb[j] {
				best, src = sub+1, SrcReplace
			}
			if d[i-1][j]+1 < best { // delete
				best, src = d[i-1][j]+1, SrcDelete
			}
			if d[i][j-1]+1 < best { // insert
				best, src = d[i][j-1]+1, SrcInsert
			}
			if k, ok1 := lastA[bb[j]]; ok1 {
				if l, ok2 := lastB[aa[i]]; ok2 { // unrestricted transposition
					if cand := d[k-1][l-1] + i - k - 1 + j - l - 1 + 1; cand < best {
						best, src, kk[i][j], ll[i][j] = cand, SrcTranspose, k, l
					}
				}
			}
			d[i][j], from[i][j] = best, uint8(src)
			lastB[bb[j]] = j
		}
		lastA[aa[i]] = i
	}
	t := &Table{d: d, kk: kk, ll: ll, from: from, a: aa, b: bb, m: m, n: n,
		cell: int64(m) * int64(n)}
	return t, nil
}

// Distance returns the edit distance. Transposition of two adjacent code
// points costs 1, as do insertion, deletion and substitution.
func Distance(a, b string, maxProduct int64) (int, error) {
	t, err := Build(a, b, maxProduct)
	if err != nil {
		return 0, err
	}
	return t.Value(), nil
}

// Source returns the backtracking label and transposition k,l at (i,j).
func (t *Table) Source(i, j int) (src uint8, k, l int) {
	return t.from[i][j], t.kk[i][j], t.ll[i][j]
}

// DecodeRunes splits s into code points. Every invalid UTF-8 byte yields a
// distinct sentinel rune (0x1000000+byte) so different illegal bytes differ.
func DecodeRunes(s string) []rune {
	out := []rune{}
	for i := 0; i < len(s); {
		c := s[i]
		r, w := rune(c), 1
		switch {
		case c < 0x80:
		case c>>5 == 0b110 && c&0b11110 != 0:
			r, w = rune(c&0b11111)<<6, 2
		case c>>4 == 0b1110:
			r, w = rune(c&0b1111)<<12, 3
		case c>>3 == 0b11110 && c <= 0xF4:
			r, w = rune(c&0b111)<<18, 4
		default:
			out = append(out, rune(0x1000000)+rune(c))
			i++
			continue
		}
		ok := i+w <= len(s)
		for q := 1; q < w && ok; q++ {
			x := s[i+q]
			if ok = x&0b11000000 == 0b10000000; ok {
				r |= rune(x&0b00111111) << (6 * (w - 1 - q))
			}
		}
		if !ok || (w == 3 && 0xD800 <= r && r <= 0xDFFF) || r > 0x10FFFF {
			out = append(out, rune(0x1000000)+rune(c))
			i++
			continue
		}
		out, i = append(out, r), i+w
	}
	return out
}

// EncodeRunes is DecodeRunes' inverse: sentinel runes become their byte again.
func EncodeRunes(rs []rune) string {
	var buf []byte
	for _, r := range rs {
		if r >= 0x1000000 {
			buf = append(buf, byte(r))
		} else {
			var tmp [utf8.UTFMax]byte
			buf = append(buf, tmp[:utf8.EncodeRune(tmp[:], r)]...)
		}
	}
	return string(buf)
}
