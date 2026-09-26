// Package pfx computes the KMP prefix function π (the border array).
package pfx

import "math/rand"

// Table holds the prefix function for one fixed string.
type Table struct {
	s   string
	pi  []int
	cmp int // number of character-pair comparisons actually made while building π
}

// Build constructs the prefix function of s.
func Build(s string) *Table {
	n := len(s)
	t := &Table{s: s, pi: make([]int, n)}
	for i := 1; i < n; i++ {
		j := t.pi[i-1]
		for {
			t.cmp++ // one actual comparison of a character pair
			if t.s[i] == t.s[j] {
				j++
				break
			}
			if j == 0 {
				break
			}
			j = t.pi[j-1]
		}
		t.pi[i] = j
	}
	return t
}

// Len returns the length of the underlying string.
func (t *Table) Len() int { return len(t.s) }

// PiAt returns π[i], the length of the longest proper border of s[0..i].
func (t *Table) PiAt(i int) int { return t.pi[i] }

// VerifyLinear reports whether the real comparison counter stayed within a
// small constant multiple (2n) of the input size on random strings of several
// lengths from 100 to 10000. The raw counter value is never exposed.
func VerifyLinear() bool {
	r := rand.New(rand.NewSource(1))
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + r.Intn(4)) // small alphabet stresses border fallbacks
		}
		t := Build(string(b))
		if t.cmp > 2*n {
			return false
		}
	}
	return true
}
