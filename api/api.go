// Package api is the outward face of the smooth weighted round-robin balancer.
package api

import (
	"errors"
	"fmt"

	"ontology/svc"
)

// Re-exported sentinel errors; the three are mutually distinct.
var (
	ErrInvalidConfig   = svc.ErrInvalidConfig
	ErrIndexOutOfRange = svc.ErrIndexOutOfRange
	ErrInvalidWeight   = svc.ErrInvalidWeight
)

// Balancer is safe for concurrent use.
type Balancer struct {
	reg *svc.Registry
}

// New builds a balancer; invalid configs fail without side effects.
func New(weights []int) (*Balancer, error) {
	r, err := svc.New(weights)
	if err != nil {
		return nil, err
	}
	return &Balancer{reg: r}, nil
}

// Next returns the index of the server picked by smooth weighted round-robin.
func (b *Balancer) Next() int { return b.reg.Next() }

// SetWeight updates one server's weight; invalid arguments are rejected
// without changing any state.
func (b *Balancer) SetWeight(i, w int) error { return b.reg.SetWeight(i, w) }

// Weight returns w[i], or 0 when i is out of range.
func (b *Balancer) Weight(i int) int { return b.reg.Weight(i) }

// SelfCheck verifies the four invariants on built-in configs plus the
// max-location scalability bound. It reports only a verdict.
func (b *Balancer) SelfCheck() error {
	configs := [][]int{{3, 1, 2}, {1}, {1, 1, 1, 1}, {2, 5, 3, 7, 1}, {4, 4, 2}}
	for _, w := range configs {
		if err := checkFairAndSum(w); err != nil {
			return err
		}
	}
	if err := checkDeterminism(); err != nil {
		return err
	}
	if err := checkRejectAtomic(); err != nil {
		return err
	}
	return svc.CheckScalability()
}

// checkFairAndSum: over W consecutive picks server i is chosen exactly w_i
// times, and sum(cw)==0 holds after every single pick.
func checkFairAndSum(w []int) error {
	r, err := svc.New(w)
	if err != nil {
		return err
	}
	total := 0
	for _, x := range w {
		total += x
	}
	got := make([]int, len(w))
	for k := 0; k < total; k++ {
		got[r.Next()]++
		if r.SumCW() != 0 {
			return fmt.Errorf("api: sum(cw) != 0 for %v", w)
		}
	}
	for i := range w {
		if got[i] != w[i] {
			return fmt.Errorf("api: unfair for %v: server %d got %d want %d", w, i, got[i], w[i])
		}
	}
	return nil
}

// checkDeterminism: identical configs produce identical pick sequences.
func checkDeterminism() error {
	a, err1 := svc.New([]int{3, 1, 2})
	c, err2 := svc.New([]int{3, 1, 2})
	if err1 != nil || err2 != nil {
		return errors.New("api: cannot build self-check balancers")
	}
	for k := 0; k < 60; k++ {
		if a.Next() != c.Next() {
			return errors.New("api: nondeterministic Next sequence")
		}
	}
	return nil
}

// checkRejectAtomic: rejected SetWeight calls leave every observable
// behaviour identical to an untouched control balancer.
func checkRejectAtomic() error {
	r, err := svc.New([]int{3, 1, 2})
	if err != nil {
		return err
	}
	ctrl, err := svc.New([]int{3, 1, 2})
	if err != nil {
		return err
	}
	r.Next()
	ctrl.Next()
	if err := r.SetWeight(-1, 5); !errors.Is(err, svc.ErrIndexOutOfRange) {
		return errors.New("api: bad index not rejected")
	}
	if err := r.SetWeight(0, 0); !errors.Is(err, svc.ErrInvalidWeight) {
		return errors.New("api: bad weight not rejected")
	}
	for i, w := range []int{3, 1, 2} {
		if r.Weight(i) != w {
			return errors.New("api: weight changed by rejected call")
		}
	}
	for k := 0; k < 12; k++ {
		if r.Next() != ctrl.Next() {
			return errors.New("api: state changed by rejected call")
		}
	}
	return nil
}
