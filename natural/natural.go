// Package natural compares strings in natural order: ASCII digit runs
// compare by numeric value, everything else byte by byte. Numerically
// equal digit runs that differ in leading zeros are not equal; the
// first such difference decides only when the rest of the strings
// compare equal, so Compare(a, b) == 0 holds iff a == b.
package natural

import (
	"sort"
	"sync/atomic"

	"ontology/chunk"
)

var lastBytes atomic.Int64

// LastCompareBytes reports how many input bytes the most recent
// Compare call examined.
func LastCompareBytes() int64 { return lastBytes.Load() }

// Compare returns -1, 0 or +1 as a orders before, equal to, or after b.
func Compare(a, b string) int {
	ca, cb := newCursor(a), newCursor(b)
	examined, zeros := 0, 0
	for ca.ok && cb.ok {
		if ca.off == 0 && cb.off == 0 && ca.cur.Digits && cb.cur.Digits {
			examined += len(ca.cur.Text) + len(cb.cur.Text)
			if c := compareNum(ca.cur.Text, cb.cur.Text); c != 0 {
				return done(examined, c)
			}
			if zeros == 0 {
				zeros = sign(leadingZeros(ca.cur.Text) - leadingZeros(cb.cur.Text))
			}
			ca.advance()
			cb.advance()
			continue
		}
		examined += 2
		if ba, bb := ca.b(), cb.b(); ba != bb {
			return done(examined, sign(int(ba)-int(bb)))
		}
		ca.step()
		cb.step()
	}
	switch {
	case ca.ok:
		return done(examined, 1)
	case cb.ok:
		return done(examined, -1)
	}
	return done(examined, zeros)
}

// Less reports whether a orders before b.
func Less(a, b string) bool { return Compare(a, b) < 0 }

// Sort orders ss by Compare. The order is total, so the result is the
// same for every initial permutation of the same multiset.
func Sort(ss []string) {
	sort.Slice(ss, func(i, j int) bool { return Less(ss[i], ss[j]) })
}

func done(examined, c int) int {
	lastBytes.Store(int64(examined))
	return c
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// compareNum compares two digit runs by value without integer
// conversion, so runs of any length are safe.
func compareNum(x, y string) int {
	sx, sy := x[leadingZeros(x):], y[leadingZeros(y):]
	if len(sx) != len(sy) {
		return sign(len(sx) - len(sy))
	}
	for i := 0; i < len(sx); i++ {
		if sx[i] != sy[i] {
			return sign(int(sx[i]) - int(sy[i]))
		}
	}
	return 0
}

func leadingZeros(s string) int {
	n := 0
	for n < len(s) && s[n] == '0' {
		n++
	}
	return n
}

type cursor struct {
	it  *chunk.Iter
	cur chunk.Chunk
	off int
	ok  bool
}

func newCursor(s string) *cursor {
	c := &cursor{it: chunk.New(s)}
	c.advance()
	return c
}

func (c *cursor) advance() {
	c.cur, c.ok = c.it.Next()
	c.off = 0
}

func (c *cursor) b() byte { return c.cur.Text[c.off] }

func (c *cursor) step() {
	c.off++
	if c.off >= len(c.cur.Text) {
		c.advance()
	}
}
