// Package api is the public, concurrency-safe face of the quantile
// summary. It depends on quant (which depends on gk).
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/gk"
	"ontology/quant"
)

// Distinguishable sentinel errors, one per rejected operation.
var (
	ErrEpsilon   = errors.New("api: epsilon must be in (0,1)")
	ErrDuplicate = errors.New("api: duplicate value")
	ErrPhi       = errors.New("api: phi must be in (0,1)")
	ErrEmpty     = errors.New("api: query on empty summary")
)

// Summary is a thread-safe ε-approximate quantile summary. Internally gk
// runs at ε/2 so the query rank error stays within ε·n (NOTES.md).
type Summary struct {
	mu    sync.RWMutex
	eps   float64
	s     *gk.Summary
	q     *quant.Engine
	seen  map[int64]struct{}
	every int64
}

func New(eps float64) (*Summary, error) {
	if eps <= 0 || eps >= 1 {
		return nil, ErrEpsilon
	}
	return &Summary{eps: eps, s: gk.New(eps / 2), q: &quant.Engine{}, seen: map[int64]struct{}{}, every: max(int64(1/eps), 1)}, nil
}

func (a *Summary) Insert(v int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, dup := a.seen[v]; dup {
		return ErrDuplicate
	}
	a.s.Insert(v)
	a.seen[v] = struct{}{}
	if a.s.N()%a.every == 0 {
		a.s.Compress()
	}
	return nil
}

func (a *Summary) Query(phi float64) (int64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if phi <= 0 || phi >= 1 {
		return 0, ErrPhi
	}
	if a.s.N() == 0 {
		return 0, ErrEmpty
	}
	return a.q.Query(a.s, phi), nil
}

func (a *Summary) Size() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.s.N()
}

// SelfCheck verifies the four invariants of NOTES.md (fresh instances only).
func (a *Summary) SelfCheck() error {
	a.mu.RLock()
	eps := a.eps
	a.mu.RUnlock()
	var seqs [3][]int64
	for i := 0; i < 300; i++ {
		seqs[0] = append(seqs[0], int64(i))
		seqs[1] = append(seqs[1], int64(299-i))
		seqs[2] = append(seqs[2], int64(i)*97%300) // gcd(97,300)=1: distinct
	}
	for _, seq := range seqs {
		f, _ := New(eps)
		for _, v := range seq {
			if err := f.Insert(v); err != nil {
				return err
			}
		}
		if err := f.check(seq); err != nil {
			return err
		}
	}
	return checkRejects()
}

// check verifies structure, band and naive-reference agreement (≤ ε·n).
func (a *Summary) check(seq []int64) error {
	ts := a.s.Tuples()
	for i := 1; i < len(ts); i++ {
		if ts[i-1].V >= ts[i].V {
			return errors.New("selfcheck: tuples not strictly ascending")
		}
	}
	if a.s.N() != int64(len(seq)) || !a.s.BandOK() {
		return errors.New("selfcheck: size or band invariant violated")
	}
	srt := slices.Clone(seq)
	slices.Sort(srt)
	n := float64(len(seq))
	for k := 1; k < 20; k++ {
		phi := float64(k) / 20
		got, _ := a.Query(phi)
		rank, _ := slices.BinarySearch(srt, got)
		if d := float64(rank+1) - phi*n; d > a.eps*n || d < -a.eps*n {
			return fmt.Errorf("selfcheck: phi=%v rank error %v exceeds eps*n", phi, d)
		}
	}
	return nil
}

// checkRejects verifies invariant 4: distinct sentinel errors, no trace.
func checkRejects() error {
	f, _ := New(0.1)
	for _, v := range []int64{5, 1, 9} {
		_ = f.Insert(v)
	}
	size0 := f.Size()
	q0, _ := f.Query(0.5)
	_, eEps := New(0)
	eDup := f.Insert(5)
	_, ePhi := f.Query(2)
	g, _ := New(0.1)
	_, eEmpty := g.Query(0.5)
	got := []error{eEps, eDup, ePhi, eEmpty}
	want := []error{ErrEpsilon, ErrDuplicate, ErrPhi, ErrEmpty}
	for i, err := range got {
		for j, w := range want {
			if (i == j) != errors.Is(err, w) {
				return fmt.Errorf("selfcheck: reject %d mismatches sentinel %d", i, j)
			}
		}
	}
	q1, _ := f.Query(0.5)
	if f.Size() != size0 || q1 != q0 {
		return errors.New("selfcheck: rejected op changed state")
	}
	return f.Insert(7) // still usable after rejections
}
