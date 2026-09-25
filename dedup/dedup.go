// Package dedup holds the set of already-applied global sequence numbers.
//
// It depends on no other package. Membership is an O(1) hash probe over an
// open-addressing table (linear probing), never a linear scan of the table.
package dedup

import "errors"

// ErrProbeCost means a membership probe examined more than probeBound slots.
// It carries no counter value: the numeric probe cost stays unexported.
var ErrProbeCost = errors.New("dedup: probe cost exceeded the constant bound")

const (
	initialCap    = 16
	loadFactorNum = 7 // grow past 7/10 occupancy
	loadFactorDen = 10
	// probeBound is a constant, m-independent upper bound on how many
	// slots one membership probe may examine below the load threshold.
	probeBound = 16
)

// Set is the set of Seq values that have been applied exactly once.
type Set struct {
	keys []int64
	used []bool
	mask uint64
	size int

	// lastCheck records how many slots the most recent membership probe
	// examined. It is unexported: no exported field or method exposes it.
	lastCheck int
}

// New returns an empty dedup set.
func New() *Set {
	return &Set{
		keys: make([]int64, initialCap),
		used: make([]bool, initialCap),
		mask: uint64(initialCap - 1),
	}
}

// mix is splitmix64's finalizer, so consecutive Seq values do not cluster.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// probe returns the slot holding seq, or the first empty slot where seq
// would be inserted. It records the number of slots examined in lastCheck.
func (s *Set) probe(seq int64) int {
	i := mix(uint64(seq)) & s.mask
	checked := 0
	for {
		checked++
		if !s.used[i] || s.keys[i] == seq {
			s.lastCheck = checked
			return int(i)
		}
		i = (i + 1) & s.mask
	}
}

// Add inserts seq. It returns true when seq is new (applied for the first
// time) and false when it was already present (an idempotent no-op).
func (s *Set) Add(seq int64) bool {
	i := s.probe(seq)
	if s.used[i] {
		return false
	}
	s.used[i] = true
	s.keys[i] = seq
	s.size++
	if s.size*loadFactorDen > len(s.keys)*loadFactorNum {
		s.grow()
	}
	return true
}

// Has reports whether seq has already been applied.
func (s *Set) Has(seq int64) bool {
	i := s.probe(seq)
	return s.used[i]
}

// Len returns the number of distinct applied Seq values.
func (s *Set) Len() int { return s.size }

// grow doubles the table and rehashes. Rehashing is not a membership probe,
// so it does not touch lastCheck.
func (s *Set) grow() {
	oldKeys, oldUsed := s.keys, s.used
	n := len(oldKeys) * 2
	s.keys = make([]int64, n)
	s.used = make([]bool, n)
	s.mask = uint64(n - 1)
	for j, k := range oldKeys {
		if !oldUsed[j] {
			continue
		}
		i := mix(uint64(k)) & s.mask
		for s.used[i] {
			i = (i + 1) & s.mask
		}
		s.used[i] = true
		s.keys[i] = k
	}
}

// CheckProbeCost verifies at several sizes m that probing a brand-new Seq
// examines only a constant, m-independent number of slots. It exposes a
// pass/fail error only; the counter value itself never crosses the API.
func CheckProbeCost() error {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for j := 0; j < m; j++ {
			s.Add(int64(j + 1))
		}
		fresh := int64(m*7 + 1_000_003) // disjoint from the inserted range
		s.Add(fresh)
		if s.lastCheck > probeBound {
			return ErrProbeCost
		}
	}
	return nil
}
