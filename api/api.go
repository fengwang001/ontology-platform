// Package api is the outward-facing front of the EWMA service.
// It validates every request before touching state, so a rejected
// operation never leaves a trace. It depends only on package series.
package api

import (
	"errors"
	"math"

	"ontology/series"
)

// Decidable sentinel errors, each distinct from the others.
var (
	ErrBadAlpha   = errors.New("ewma: alpha must satisfy 0 < alpha < 1")
	ErrEmptyKey   = errors.New("ewma: key must not be empty")
	ErrUnknownKey = errors.New("ewma: key was never updated")
)

// Service is the public EWMA facade.
type Service struct {
	set *series.Set
}

// New validates alpha and returns a Service; on invalid alpha it
// returns ErrBadAlpha and no usable Service.
func New(alpha, seed float64, biasCorrect bool) (*Service, error) {
	if !(alpha > 0 && alpha < 1) {
		return nil, ErrBadAlpha
	}
	return &Service{set: series.New(alpha, seed, biasCorrect)}, nil
}

// Update folds x into key's series. Empty key is rejected without
// changing any state.
func (s *Service) Update(key string, x float64) error {
	if key == "" {
		return ErrEmptyKey
	}
	s.set.Update(key, x)
	return nil
}

// Value returns key's current mean, or ErrUnknownKey if the key was
// never updated. A failed lookup changes nothing.
func (s *Service) Value(key string) (float64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	v, ok := s.set.Value(key)
	if !ok {
		return 0, ErrUnknownKey
	}
	return v, nil
}

// Count returns key's observation count, or ErrUnknownKey.
func (s *Service) Count(key string) (int, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	n, ok := s.set.Count(key)
	if !ok {
		return 0, ErrUnknownKey
	}
	return n, nil
}

// SelfCheck verifies the four invariants on built-in observation
// sequences and returns a non-nil error on the first violation.
func (s *Service) SelfCheck() error {
	// 1. range: s stays within [min(seed,xs), max(seed,xs)].
	//    Corrected case uses seed=0 (the canonical correction
	//    scenario): the corrected value is a convex combination of
	//    the observations, hence inside their range. Uncorrected
	//    case uses a non-zero seed: s is a convex combination of
	//    seed and observations.
	xs := []float64{-2, 8, 0, 3, -1}
	for i, seed := range []float64{0, 5} {
		r, err := New(0.3, seed, i == 0)
		if err != nil {
			return err
		}
		lo, hi := seed, seed
		for _, x := range xs {
			lo, hi = math.Min(lo, x), math.Max(hi, x)
			if err := r.Update("k", x); err != nil {
				return err
			}
			v, err := r.Value("k")
			if err != nil || v < lo-1e-12 || v > hi+1e-12 {
				return errors.New("selfcheck: range invariant violated")
			}
		}
	}
	// 2. constant input exactness (corrected and uncorrected).
	c, _ := New(0.5, 0, true)
	u, _ := New(0.5, 0, false)
	for n := 1; n <= 7; n++ {
		if err := c.Update("k", 4); err != nil {
			return err
		}
		if err := u.Update("k", 4); err != nil {
			return err
		}
		vc, _ := c.Value("k")
		vu, _ := u.Value("k")
		if vc != 4 || math.Abs(vu-4*(1-math.Pow(0.5, float64(n)))) > 1e-12 {
			return errors.New("selfcheck: constant-input invariant violated")
		}
	}
	// 3. agreement with the closed-form reference.
	f, _ := New(0.25, 1.5, false)
	seq := []float64{3, -1, 2.5, 0, 7}
	for _, x := range seq {
		if err := f.Update("k", x); err != nil {
			return err
		}
	}
	got, _ := f.Value("k")
	sum, w := 0.0, 1.0
	for i := len(seq) - 1; i >= 0; i-- {
		sum += w * seq[i]
		w *= 0.75
	}
	want := 0.25*sum + w*1.5
	if math.Abs(got-want) > 1e-9 {
		return errors.New("selfcheck: closed-form mismatch")
	}
	// 4. rejection leaves no trace.
	g, _ := New(0.5, 0, false)
	if err := g.Update("k", 2); err != nil {
		return err
	}
	before, _ := g.Value("k")
	if err := g.Update("", 99); !errors.Is(err, ErrEmptyKey) {
		return errors.New("selfcheck: empty key not rejected")
	}
	if _, err := g.Value("ghost"); !errors.Is(err, ErrUnknownKey) {
		return errors.New("selfcheck: unknown key not rejected")
	}
	if _, err := New(0, 0, false); !errors.Is(err, ErrBadAlpha) {
		return errors.New("selfcheck: bad alpha not rejected")
	}
	after, _ := g.Value("k")
	if after != before {
		return errors.New("selfcheck: rejection mutated state")
	}
	return nil
}
