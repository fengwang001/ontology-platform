// Package api is the concurrency-safe public front end to the append-only
// changelog with key compaction (in-process state, standard library only).
// Dependency direction is one-way: api -> cpt -> clog.
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/cpt"
)

// The three sentinel errors are mutually distinct and matchable via errors.Is.
var (
	ErrEmptyKey      = errors.New("clog: key must not be empty")
	ErrSeqOutOfRange = errors.New("clog: read seq out of range")
	ErrBadRange      = errors.New("clog: invalid compact interval")
)

type Store struct {
	mu  sync.RWMutex
	log *cpt.Log
}

func New() *Store { return &Store{log: cpt.NewLog()} }

// Append writes one entry and returns its assigned Seq (from 1, dense).
func (s *Store) Append(key string, val int) (int64, error) {
	if key == "" { // validate before touching state (invariant 4)
		return 0, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.log.Append(key, val), nil
}

// Read returns the visible value of key at seq (a compaction record counts as
// Seq=lo). ok=false with nil error means the key does not exist there.
func (s *Store) Read(seq int64, key string) (int, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if seq < 1 || seq >= s.log.NextSeq() {
		return 0, false, ErrSeqOutOfRange
	}
	v, ok := s.log.Read(seq, key)
	return v, ok, nil
}

// Compact folds the half-open interval [lo, hi).
func (s *Store) Compact(lo, hi int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lo < 1 || hi > s.log.NextSeq() || lo >= hi { // all rejections pre-mutation
		return fmt.Errorf("%w: [%d,%d) next=%d", ErrBadRange, lo, hi, s.log.NextSeq())
	}
	s.log.Compact(lo, hi)
	return nil
}

type pt2 = [2]any           // {val int, ok bool}; comparable for int/bool elements
func mk(v int, ok bool) pt2 { return pt2{v, ok} }

// SelfCheck replays a built-in sequence on throwaway state and verifies all
// four invariants; it never mutates the receiver, so it is safe alongside Read.
func (s *Store) SelfCheck() error {
	l := cpt.NewLog()
	type w struct {
		seq int64
		key string
		val int
	}
	var raws []w
	put := func(k string, v int) { raws = append(raws, w{l.Append(k, v), k, v}) }
	put("K1", 10)
	put("K2", 20)
	put("K1", 30)
	put("K3", 40)
	put("K1", 50)
	put("K2", 60)
	put("K4", 70)
	keys := []string{"K1", "K2", "K3", "K4", "KX"}

	pre := map[[2]any]pt2{}
	for at := int64(1); at < l.NextSeq(); at++ {
		for _, k := range keys {
			pre[[2]any{at, k}] = mk(l.Read(at, k))
		}
	}
	l.Compact(3, 7)

	ref := func(key string) pt2 { // batch: raw Seq<hi=7, last value per key
		p := mk(0, false)
		for _, r := range raws {
			if r.seq < 7 && r.key == key {
				p = pt2{r.val, true}
			}
		}
		return p
	}
	for at := int64(3); at < 7; at++ { // invariant 1
		for _, k := range keys {
			if mk(l.Read(at, k)) != ref(k) {
				return fmt.Errorf("invariant1 at=%d k=%s", at, k)
			}
		}
	}
	for at := int64(1); at < 3; at++ { // invariant 2: sites before lo unchanged
		for _, k := range keys {
			if mk(l.Read(at, k)) != pre[[2]any{at, k}] {
				return fmt.Errorf("invariant2 at=%d k=%s", at, k)
			}
		}
	}
	for at := int64(1); at < l.NextSeq(); at++ { // invariant 3: every site reads
		for _, k := range keys {
			_ = mk(l.Read(at, k)) // a missing site mapping would panic here
		}
	}
	return selfCheckRejections() // invariant 4
}

// selfCheckRejections exercises the three distinct sentinels and confirms a
// rejected operation leaves the log/site map untouched and the store usable.
func selfCheckRejections() error {
	s := New()
	s.Append("K1", 1)
	before := mk(s.log.Read(1, "K1"))
	if _, err := s.Append("", 9); !errors.Is(err, ErrEmptyKey) {
		return fmt.Errorf("invariant4 empty-key err=%v", err)
	}
	for _, q := range []int64{0, 2} {
		if _, _, err := s.Read(q, "K1"); !errors.Is(err, ErrSeqOutOfRange) {
			return fmt.Errorf("invariant4 seq=%d err=%v", q, err)
		}
	}
	for _, r := range [][2]int64{{0, 2}, {1, 99}, {2, 2}} {
		if err := s.Compact(r[0], r[1]); !errors.Is(err, ErrBadRange) {
			return fmt.Errorf("invariant4 range=%v err=%v", r, err)
		}
	}
	if mk(s.log.Read(1, "K1")) != before {
		return errors.New("invariant4 state changed after rejection")
	}
	if _, err := s.Append("K2", 2); err != nil {
		return err
	}
	return nil
}
