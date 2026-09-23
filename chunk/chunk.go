// Package chunk splits a string into alternating numeric and non-numeric
// segments. Only ASCII bytes '0'-'9' are treated as digits.
package chunk

// Segment is one maximal run of either ASCII digits or non-digits.
type Segment struct {
	Text  string // raw text of the run, including any leading zeroes
	Digit bool   // true for an ASCII-digit run
}

// IsDigit reports whether b is an ASCII digit.
func IsDigit(b byte) bool { return b >= '0' && b <= '9' }

// Split returns the maximal alternating segments of s.
// The empty string yields no segments.
func Split(s string) []Segment {
	if s == "" {
		return nil
	}
	var segs []Segment
	start := 0
	digit := IsDigit(s[0])
	for i := 1; i < len(s); i++ {
		if IsDigit(s[i]) != digit {
			segs = append(segs, Segment{Text: s[start:i], Digit: digit})
			start = i
			digit = !digit
		}
	}
	segs = append(segs, Segment{Text: s[start:], Digit: digit})
	return segs
}

// Next returns the maximal run starting at pos and the position just past it.
// It is a streaming, allocation-free counterpart of Split: pos must satisfy
// 0 <= pos <= len(s); when pos == len(s) it returns an empty segment and pos.
func Next(s string, pos int) (Segment, int) {
	if pos < 0 || pos > len(s) {
		panic("chunk: position out of range")
	}
	if pos == len(s) {
		return Segment{}, pos
	}
	digit := IsDigit(s[pos])
	end := pos + 1
	for end < len(s) && IsDigit(s[end]) == digit {
		end++
	}
	return Segment{Text: s[pos:end], Digit: digit}, end
}
