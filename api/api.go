// Package api is the public, concurrency-safe face of the GK quantile
// summary. It depends on quant (which depends on gk), never the reverse.
package api

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"sync"

	"ontology/gk"
	"ontology/quant"
)

// ErrEpsilon reports an ε outside (0,1).
var ErrEpsilon = errors.New("api: epsilon out of range (0,1)")

// Summary is a concurrency-safe ε-approximate quantile summary.
type Summary struct {
	mu sync.RWMutex
	s  *gk.Summary
	q  *quant.Engine
}

// New builds an empty summary; ε must satisfy 0 < ε < 1.
func New(eps float64) (*Summary, error) {
	if eps <= 0 || eps >= 1 {
		return nil, ErrEpsilon
	}
	return &Summary{s: gk.New(eps), q: &quant.Engine{}}, nil
}

// Insert adds v; a duplicate fails with gk.ErrDuplicate and changes nothing.
func (a *Summary) Insert(v int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.s.Insert(v); err != nil {
		return err
	}
	a.s.Compress()
	return nil
}

// Query returns the ε-approximate φ-quantile of all inserted values.
func (a *Summary) Query(phi float64) (int64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.q.Query(a.s, phi)
}

// Size returns the number of inserted values.
func (a *Summary) Size() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.s.N()
}

// SelfCheck replays built-in insert sequences and verifies the four
// invariants of NOTES.md. It never touches the receiver's state.
func (a *Summary) SelfCheck() error {
	for _, c := range []struct {
		eps float64
		m   int
	}{
		{0.25, 5}, {0.05, 500}, {0.01, 2000},
	} {
		if err := checkCase(c.eps, c.m); err != nil {
			return err
		}
	}
	return nil
}

func checkCase(eps float64, m int) error {
	vals := make([]int64, m)
	for i := range vals {
		vals[i] = int64(i) + 1
	}
	rand.New(rand.NewSource(int64(m))).Shuffle(m, func(i, j int) {
		vals[i], vals[j] = vals[j], vals[i]
	})
	s := gk.New(eps)
	q := &quant.Engine{}
	for _, v := range vals {
		if err := s.Insert(v); err != nil {
			return fmt.Errorf("selfcheck insert: %w", err)
		}
		s.Compress()
	}
	// Invariant 1: structure — n equals inserts, tuples strictly increasing.
	if s.N() != int64(m) {
		return fmt.Errorf("selfcheck: n=%d, want %d", s.N(), m)
	}
	ts := s.Tuples()
	for i := 1; i < len(ts); i++ {
		if ts[i-1].V >= ts[i].V {
			return errors.New("selfcheck: tuples not strictly increasing")
		}
	}
	// Invariant 2: band.
	if !s.BandOK() {
		return errors.New("selfcheck: band invariant violated")
	}
	// Invariant 3: rank of every answer within εn of φn vs sorted reference.
	slices.Sort(vals)
	for _, phi := range []float64{0.1, 0.25, 0.5, 0.75, 0.9} {
		got, err := q.Query(s, phi)
		if err != nil {
			return fmt.Errorf("selfcheck query: %w", err)
		}
		rank, _ := slices.BinarySearch(vals, got)
		if d := math.Abs(float64(rank+1) - phi*float64(m)); d > eps*float64(m) {
			return fmt.Errorf("selfcheck: phi=%v rank off by %v", phi, d)
		}
	}
	// Invariant 4: rejected operations leave no trace.
	if err := s.Insert(vals[0]); !errors.Is(err, gk.ErrDuplicate) || s.N() != int64(m) {
		return errors.New("selfcheck: duplicate insert not rejected cleanly")
	}
	if _, err := q.Query(s, 0); !errors.Is(err, quant.ErrBadPhi) {
		return errors.New("selfcheck: bad phi not rejected")
	}
	if _, err := q.Query(gk.New(eps), 0.5); !errors.Is(err, quant.ErrEmpty) {
		return errors.New("selfcheck: empty query not rejected")
	}
	return nil
}
