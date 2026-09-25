// Package topk maintains a candidate set of frequent keys against a
// threshold fraction of the total count. It never misses a key whose
// true count exceeds the threshold; it may over-report.
package topk

import (
	"sort"

	"ontology/bound"
	"ontology/sketch"
)

var _ Estimator = (*sketch.Sketch)(nil)

// Estimator is the part of sketch.Sketch the detector relies on.
type Estimator interface {
	Add(string, uint64) error
	Estimate(string) (uint64, error)
	Total() uint64
}

// Detector tracks candidates whose estimate reaches phi*Total.
type Detector struct {
	s    Estimator
	p    bound.Params
	phi  float64
	cand map[string]struct{}
}

// NewDetector requires phi in (0,1).
func NewDetector(s Estimator, p bound.Params, phi float64) (*Detector, error) {
	if phi <= 0 || phi >= 1 {
		return nil, bound.ErrBadProbability
	}
	return &Detector{s: s, p: p, phi: phi, cand: map[string]struct{}{}}, nil
}

func (d *Detector) threshold() uint64 {
	return uint64(d.phi*float64(d.s.Total())) + 1 // "exceeds phi*N" means strictly above
}

// Add counts n occurrences of key and admits it to the candidate set if its
// estimate reaches the threshold.
func (d *Detector) Add(key string, n uint64) error {
	if err := d.s.Add(key, n); err != nil {
		return err
	}
	if est, err := d.s.Estimate(key); err == nil && est >= d.threshold() {
		d.cand[key] = struct{}{}
	}
	return nil
}

// Merge unions another detector's candidates into d.
func (d *Detector) Merge(o *Detector) {
	for k := range o.cand {
		d.cand[k] = struct{}{}
	}
}

// HeavyHitters returns candidates whose current estimate reaches the
// threshold, sorted for determinism. Comparing Estimate (not Estimate minus
// the error bound) is what guarantees zero false negatives.
func (d *Detector) HeavyHitters() []string {
	th := d.threshold()
	out := []string{}
	for k := range d.cand {
		if est, err := d.s.Estimate(k); err == nil && est >= th {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Certain reports whether key stays above the threshold even after
// subtracting the (epsilon,delta) error bound.
func (d *Detector) Certain(key string) bool {
	est, err := d.s.Estimate(key)
	return err == nil && est >= d.threshold()+d.p.ErrorBound(d.s.Total())
}
