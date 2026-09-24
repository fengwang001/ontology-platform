// Package ver holds one key's ordered version timeline with AS OF lookup.
package ver

import "sort"

// Version is one entry in a single key's version timeline.
type Version struct {
	ValidFrom int64
	Value     string
	Tombstone bool
}

// Versions stores one key's versions sorted by ValidFrom ascending, unique.
type Versions struct {
	vs     []Version
	probes int // versions inspected by the most recent AsOf (unexported)
}

// Put inserts v, replacing any existing version with the same ValidFrom.
func (s *Versions) Put(v Version) {
	i := sort.Search(len(s.vs), func(i int) bool { return s.vs[i].ValidFrom >= v.ValidFrom })
	if i < len(s.vs) && s.vs[i].ValidFrom == v.ValidFrom {
		s.vs[i] = v
		return
	}
	s.vs = append(s.vs, Version{})
	copy(s.vs[i+1:], s.vs[i:])
	s.vs[i] = v
}

// AsOf returns the version with the greatest ValidFrom <= ts. The second
// result is false when no version has ValidFrom <= ts. A tombstone is a
// normal lookup hit: callers see Tombstone and must report no value.
func (s *Versions) AsOf(ts int64) (Version, bool) {
	s.probes = 0
	// Rightmost position whose ValidFrom <= ts, found by binary search.
	lo, hi := 0, len(s.vs)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		s.probes++
		if s.vs[mid].ValidFrom <= ts {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return Version{}, false
	}
	return s.vs[lo-1], true
}

// Len reports the number of stored versions (tombstones included).
func (s *Versions) Len() int { return len(s.vs) }

// probeBound is ceil(log2(m+1)) + 3, the contractual probe upper bound.
func probeBound(m int) int {
	c := 0
	for 1<<c < m+1 {
		c++
	}
	return c + 3
}

// VerifyProbeBound runs the logarithmic-probe experiment internally and
// returns only pass/fail, so the probe counter never leaves the package.
func VerifyProbeBound() bool {
	for _, m := range []int{100, 1000, 10000} {
		var s Versions
		for i := 0; i < m; i++ {
			s.Put(Version{ValidFrom: int64(i)})
		}
		for _, ts := range []int64{-1, 0, int64(m) / 2, int64(m - 1), int64(m)} {
			s.AsOf(ts)
			if s.probes > probeBound(m) {
				return false
			}
		}
	}
	return true
}
