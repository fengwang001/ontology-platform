// Package ring maintains an ascending-ordered set of member IDs and
// computes partition targets on a ring of P partitions.
package ring

// Set is an ascending-ordered set of member IDs in [0, P).
type Set struct{ m []int }

// Len returns the number of members.
func (s *Set) Len() int { return len(s.m) }

// lower returns the first index with m[i] >= x (or len(m)).
func (s *Set) lower(x int) int {
	lo, hi := 0, len(s.m)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if s.m[mid] < x {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

// Has reports whether x is a member.
func (s *Set) Has(x int) bool {
	i := s.lower(x)
	return i < len(s.m) && s.m[i] == x
}

// Add inserts x; false if already present.
func (s *Set) Add(x int) bool {
	i := s.lower(x)
	if i < len(s.m) && s.m[i] == x {
		return false
	}
	s.m = append(s.m, 0)
	copy(s.m[i+1:], s.m[i:])
	s.m[i] = x
	return true
}

// Remove deletes x; false if absent.
func (s *Set) Remove(x int) bool {
	i := s.lower(x)
	if i == len(s.m) || s.m[i] != x {
		return false
	}
	s.m = append(s.m[:i], s.m[i+1:]...)
	return true
}

// Target returns the member partition p maps to: the smallest member >= p,
// wrapping to the smallest member when none is >= p. False if empty.
func (s *Set) Target(p int) (int, bool) {
	if len(s.m) == 0 {
		return 0, false
	}
	if i := s.lower(p); i < len(s.m) {
		return s.m[i], true
	}
	return s.m[0], true
}

// Range calls f for every partition whose current target is x
// (x must be a member): the interval (pred, x], plus the wrap tail
// (max, P-1] when x is the minimum member.
func (s *Set) Range(x, P int, f func(p int)) {
	i := s.lower(x)
	lo := 0
	if i > 0 {
		lo = s.m[i-1] + 1
	}
	for p := lo; p <= x; p++ {
		f(p)
	}
	if i == 0 { // x is the minimum: it also owns the wrap tail above the max
		for p := s.m[len(s.m)-1] + 1; p < P; p++ {
			f(p)
		}
	}
}
