// Package api is the public, concurrency-safe face of the GK quantile summary.
package api

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"sync"

	"ontology/gk"
	"ontology/quant"
)

// Decidable sentinel errors, all distinct.
var (
	ErrBadEpsilon = errors.New("api: epsilon not in (0,1)")
	ErrDuplicate  = errors.New("api: duplicate value")
	ErrBadPhi     = errors.New("api: phi not in (0,1)")
	ErrEmpty      = errors.New("api: query on empty summary")
)

// Summary is a thread-safe ε-approximate quantile summary.
type Summary struct {
	mu sync.Mutex
	s  *gk.Summary
	q  *quant.Querier
}

// New returns a summary with precision eps, or ErrBadEpsilon.
func New(eps float64) (*Summary, error) {
	if eps <= 0 || eps >= 1 {
		return nil, ErrBadEpsilon
	}
	s := gk.New(eps)
	return &Summary{s: s, q: quant.New(s)}, nil
}

// Insert adds v, or fails with ErrDuplicate leaving no trace.
func (a *Summary) Insert(v int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.s.Has(v) {
		return ErrDuplicate
	}
	a.s.Insert(v)
	a.s.Compress()
	return nil
}

// Query returns the approximate φ-quantile, or ErrBadPhi / ErrEmpty.
func (a *Summary) Query(phi float64) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if phi <= 0 || phi >= 1 {
		return 0, ErrBadPhi
	}
	if a.s.N() == 0 {
		return 0, ErrEmpty
	}
	return a.q.Query(phi), nil
}

// Size returns the number of values inserted so far.
func (a *Summary) Size() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.s.N()
}

// SelfCheck verifies the four invariants on built-in insert sequences.
// It touches no receiver state and is safe for concurrent use.
func (a *Summary) SelfCheck() error {
	for _, eps := range []float64{0.25, 0.1, 0.02} {
		if err := checkStream(eps, 400); err != nil {
			return err
		}
	}
	return nil
}

func checkStream(eps float64, n int) error {
	r := rand.New(rand.NewSource(int64(n)))
	s := gk.New(eps)
	q := quant.New(s)
	ref := make([]int64, 0, n)
	for _, x := range r.Perm(n) {
		v := int64(x)
		s.Insert(v)
		s.Compress()
		ref = append(ref, v)
		tp := s.Tuples() // invariant 1: structure
		for i := 1; i < len(tp); i++ {
			if tp[i-1].V >= tp[i].V {
				return fmt.Errorf("selfcheck: tuples not strictly ascending")
			}
		}
		if s.N() != len(ref) {
			return fmt.Errorf("selfcheck: Size != inserted count")
		}
		if !s.BandOK() { // invariant 2: band
			return fmt.Errorf("selfcheck: band invariant violated")
		}
	}
	sort.Slice(ref, func(i, j int) bool { return ref[i] < ref[j] })
	fn := float64(len(ref))
	for k := 1; k < 20; k++ { // invariant 3: naive reference.
		// The given rules (band g+Δ ≤ 2εn, query = first rmax ≥ φn) guarantee
		// rank error < 2εn, not εn — see NOTES.md. Assert the true bound.
		phi := float64(k) / 20
		got := q.Query(phi)
		rank := float64(sort.Search(len(ref), func(i int) bool { return ref[i] >= got }) + 1)
		if math.Abs(rank-phi*fn) > 2*eps*fn {
			return fmt.Errorf("selfcheck: rank error > 2*eps*n at phi=%v", phi)
		}
	}
	chk, err := New(eps) // invariant 4: rejected ops leave no trace
	if err != nil {
		return err
	}
	for _, v := range ref[:10] {
		if err := chk.Insert(v); err != nil {
			return err
		}
	}
	n0, tp0 := chk.Size(), chk.s.Tuples()
	if err := chk.Insert(ref[0]); !errors.Is(err, ErrDuplicate) {
		return fmt.Errorf("selfcheck: duplicate insert not rejected")
	}
	if _, err := chk.Query(0); !errors.Is(err, ErrBadPhi) {
		return fmt.Errorf("selfcheck: bad phi not rejected")
	}
	if empty, _ := New(eps); true {
		if _, err := empty.Query(0.5); !errors.Is(err, ErrEmpty) {
			return fmt.Errorf("selfcheck: empty query not rejected")
		}
	}
	if chk.Size() != n0 || !reflect.DeepEqual(tp0, chk.s.Tuples()) {
		return fmt.Errorf("selfcheck: rejected operation changed state")
	}
	return nil
}
