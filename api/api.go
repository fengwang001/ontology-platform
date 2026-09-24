// Package api is the public facade over the partition store.
package api

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/store"
)

// Re-exported sentinel errors; the three kinds are mutually distinct.
var (
	ErrBadParam      = store.ErrBadParam
	ErrKeyOutOfRange = store.ErrKeyOutOfRange
	ErrKeyNotFound   = store.ErrKeyNotFound
)

// Set is a split/merge key-space partition set.
type Set struct{ st *store.Store }

// New validates parameters; on error no state is created.
func New(low, high int64, splitThreshold, mergeThreshold int) (*Set, error) {
	st, err := store.New(low, high, splitThreshold, mergeThreshold)
	if err != nil {
		return nil, err
	}
	return &Set{st: st}, nil
}

func (s *Set) Insert(key int64) error                { return s.st.Insert(key) }
func (s *Set) Delete(key int64) error                { return s.st.Delete(key) }
func (s *Set) Compact()                              { s.st.Compact() }
func (s *Set) Ranges() []store.Range                 { return s.st.Ranges() }
func (s *Set) Locate(key int64) (store.Range, error) { return s.st.Locate(key) }

// Verify checks invariants 1-3 against the naive model (the exact set of
// keys expected to be present): key multiset preserved, ranges ordered /
// contiguous / exactly covering, and loads equal to a naive recount.
func (s *Set) Verify(model map[int64]bool) error {
	rs := s.Ranges()
	if len(rs) == 0 {
		return errors.New("api: no partitions")
	}
	seen := map[int64]int{}
	for i, r := range rs {
		if r.Lo >= r.Hi {
			return fmt.Errorf("api: empty range [%d,%d)", r.Lo, r.Hi)
		}
		if i > 0 && rs[i-1].Hi != r.Lo {
			return fmt.Errorf("api: gap/overlap before [%d,%d)", r.Lo, r.Hi)
		}
		naive := 0
		for k := range model {
			if r.Lo <= k && k < r.Hi {
				naive++
			}
		}
		if r.Load != naive || r.Load != len(r.Keys) {
			return fmt.Errorf("api: load %d != naive %d on [%d,%d)", r.Load, naive, r.Lo, r.Hi)
		}
		for _, k := range r.Keys {
			if k < r.Lo || k >= r.Hi {
				return fmt.Errorf("api: key %d outside [%d,%d)", k, r.Lo, r.Hi)
			}
			seen[k]++
		}
	}
	for k := range model {
		if seen[k] != 1 {
			return fmt.Errorf("api: key %d seen %d times", k, seen[k])
		}
	}
	if len(seen) != len(model) {
		return fmt.Errorf("api: %d keys stored, model has %d", len(seen), len(model))
	}
	return nil
}

// Bounds returns the key-space bounds for coverage checks.
func (s *Set) Bounds() (low, high int64) {
	rs := s.Ranges()
	return rs[0].Lo, rs[len(rs)-1].Hi
}

// SelfCheck runs built-in operation sequences against a naive model and
// verifies all four invariants; nil means every check passed.
func SelfCheck() error {
	for seed := int64(0); seed < 5; seed++ {
		if err := checkSeed(seed); err != nil {
			return err
		}
	}
	return nil
}

func checkSeed(seed int64) error {
	s, err := New(0, 4096, 4, 2)
	if err != nil {
		return err
	}
	model := map[int64]bool{}
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 2000; i++ {
		key := rng.Int63n(4096)
		switch rng.Intn(3) {
		case 0:
			if err := s.Insert(key); err != nil {
				return fmt.Errorf("seed %d op %d: %w", seed, i, err)
			}
			model[key] = true
		case 1:
			err := s.Delete(key)
			if model[key] {
				if err != nil {
					return fmt.Errorf("seed %d op %d: delete present: %w", seed, i, err)
				}
				delete(model, key)
			} else if !errors.Is(err, ErrKeyNotFound) {
				return fmt.Errorf("seed %d op %d: delete absent: %v", seed, i, err)
			}
		case 2:
			s.Compact()
		}
		if i%64 == 0 || i == 1999 {
			if err := s.Verify(model); err != nil {
				return fmt.Errorf("seed %d op %d: %w", seed, i, err)
			}
		}
	}
	return nil
}
