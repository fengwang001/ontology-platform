// Package pfx computes the prefix function (border array) of a byte string.
package pfx

// PrefixFunc holds the prefix function π of a fixed string.
// π[i] = length of the longest proper border of s[0..i]; π[0] = 0.
type PrefixFunc struct {
	pi []int
	// cmp counts character-pair comparisons actually performed while
	// building pi. Unexported on purpose: it must never leak through the
	// public API; only in-package tests may read it.
	cmp int
}

// Build computes the prefix function of s in O(len(s)) comparisons.
func Build(s string) *PrefixFunc {
	n := len(s)
	pf := &PrefixFunc{pi: make([]int, n)}
	for i := 1; i < n; i++ {
		j := pf.pi[i-1]
		for j > 0 {
			pf.cmp++
			if s[i] == s[j] {
				break
			}
			j = pf.pi[j-1]
		}
		if j == 0 {
			pf.cmp++
			if s[i] == s[0] {
				j = 1
			}
		} else {
			j++
		}
		pf.pi[i] = j
	}
	return pf
}

// At returns π[i]. Panics if i is out of range; callers guarantee bounds.
func (pf *PrefixFunc) At(i int) int { return pf.pi[i] }

// Len returns the length of the underlying string.
func (pf *PrefixFunc) Len() int { return len(pf.pi) }

// LongestBorder returns the longest proper border length of the whole string.
func (pf *PrefixFunc) LongestBorder() int {
	if len(pf.pi) == 0 {
		return 0
	}
	return pf.pi[len(pf.pi)-1]
}

// IsBorder reports whether b is a proper border length of the whole string,
// by walking the border chain π[n-1], π[π[n-1]-1], ...
func (pf *PrefixFunc) IsBorder(b int) bool {
	if b <= 0 || b >= len(pf.pi) {
		return false
	}
	for j := pf.pi[len(pf.pi)-1]; j > 0; j = pf.pi[j-1] {
		if j == b {
			return true
		}
	}
	return false
}
