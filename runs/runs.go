// Package runs splits code-point sequences into maximal runs and
// provides overflow-safe decimal count I/O for the rle package.
package runs

import "strconv"

// Run is Count consecutive copies of Rune.
type Run struct {
	Rune  rune
	Count int
}

// Split returns the maximal runs of identical code points in s.
// No Unicode normalization: distinct code points are never merged.
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out) - 1; n >= 0 && out[n].Rune == r {
			out[n].Count++
		} else {
			out = append(out, Run{r, 1})
		}
	}
	return out
}

// IsDigit reports whether b is an ASCII digit.
func IsDigit(b byte) bool { return '0' <= b && b <= '9' }

// AppendDecimal appends n in decimal with no sign and no leading zeros.
func AppendDecimal(dst []byte, n uint64) []byte { return strconv.AppendUint(dst, n, 10) }

// MaxCount bounds parsed counts so count*UTFMax never overflows uint64.
const MaxCount = uint64(1) << 60

// AddDigit folds one decimal digit into n, saturating at MaxCount
// instead of overflowing.
func AddDigit(n uint64, b byte) uint64 {
	if n > MaxCount/10 {
		return MaxCount
	}
	if n = n*10 + uint64(b-'0'); n > MaxCount {
		n = MaxCount
	}
	return n
}

// Assembler assembles one UTF-8 rune byte by byte; the zero value is
// ready to use.
type Assembler struct {
	r      rune
	need   int
	lo, hi byte
}

// Feed consumes one byte. done reports the rune complete; ok is false
// on invalid UTF-8 (done is true then as well).
func (a *Assembler) Feed(b byte) (r rune, done, ok bool) {
	if a.need > 0 {
		if b < a.lo || b > a.hi {
			a.need = 0
			return 0, true, false
		}
		a.lo, a.hi = 0x80, 0xBF
		a.r = a.r<<6 | rune(b&0x3F)
		if a.need--; a.need > 0 {
			return 0, false, true
		}
		return a.r, true, true
	}
	a.lo, a.hi = 0x80, 0xBF
	switch {
	case b < 0x80:
		return rune(b), true, true
	case b < 0xC2:
	case b < 0xE0:
		a.r, a.need = rune(b&0x1F), 1
	case b < 0xF0:
		a.r, a.need = rune(b&0x0F), 2
		if b == 0xE0 {
			a.lo = 0xA0
		}
		if b == 0xED {
			a.hi = 0x9F
		}
	case b <= 0xF4:
		a.r, a.need = rune(b&0x07), 3
		if b == 0xF0 {
			a.lo = 0x90
		}
		if b == 0xF4 {
			a.hi = 0x8F
		}
	default:
		return 0, true, false
	}
	if a.need == 0 {
		return 0, true, false
	}
	return 0, false, true
}
