// Package runs provides longest-run splitting over codepoints and
// overflow-free decimal reading/writing of run counts. It has no dependencies
// on other packages in this module.
package runs

import "math/big"

// Run is one maximal run: codepoint Sym repeated N times.
type Run struct {
	Sym rune
	N   uint64
}

// Count accumulates the decimal digits of a run count using arbitrary
// precision, so no fixed-width integer overflow can occur.
type Count struct {
	n           *big.Int
	digits      int
	leadingZero bool
}

// Split partitions s into maximal runs of equal codepoints. No Unicode
// normalization is performed: e and U+0301 stay separate from é. Invalid
// UTF-8 bytes are delivered as utf8.RuneError, one per byte, like range.
func Split(s string) []Run {
	if s == "" {
		return nil
	}
	var out []Run
	var cur Run
	first := true
	for _, r := range s {
		if first || r != cur.Sym {
			if !first {
				out = append(out, cur)
			}
			cur = Run{Sym: r, N: 1}
			first = false
			continue
		}
		cur.N++
	}
	return append(out, cur)
}

// AppendCount appends the canonical decimal form of n (unsigned, no leading
// zero) to dst.
func AppendCount(dst []byte, n uint64) []byte {
	if n == 0 {
		return append(dst, '0')
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return append(dst, buf[i:]...)
}

// Add appends one decimal digit to the count.
func (c *Count) Add(digit byte) {
	d := int64(digit - '0')
	if c.n == nil {
		c.n = new(big.Int)
		c.leadingZero = digit == '0'
	}
	c.n.Mul(c.n, big.NewInt(10))
	c.n.Add(c.n, big.NewInt(d))
	c.digits++
}

// Present reports whether at least one digit has been accumulated.
func (c *Count) Present() bool { return c.digits > 0 }

// Len is the number of accumulated decimal digits.
func (c *Count) Len() int { return c.digits }

// LeadingZero reports whether the first accumulated digit was '0'.
func (c *Count) LeadingZero() bool { return c.leadingZero }

// Value returns the accumulated value, or nil when no digit is present.
func (c *Count) Value() *big.Int { return c.n }

// Reset clears the count for reuse.
func (c *Count) Reset() { c.n = nil; c.digits = 0; c.leadingZero = false }
