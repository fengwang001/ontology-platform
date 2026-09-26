// Package api is the concurrency-safe entry point. Invalid requests
// return distinct sentinel errors without touching any state.
package api

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"

	"ontology/ewma"
	"ontology/series"
)

// Three pairwise-distinct sentinel errors (callers branch on errors.Is).
var (
	ErrInvalidAlpha = ewma.ErrInvalidAlpha
	ErrEmptyKey     = errors.New("api: key must not be the empty string")
	ErrNotFound     = errors.New("api: key has never been updated")
)

// API is the thread-safe façade over per-key EWMAs.
type API struct {
	mu  sync.RWMutex
	reg *series.Registry
}

// New validates alpha (0 < alpha < 1) and builds an API.
func New(alpha, seed float64, biasCorrect bool) (*API, error) {
	reg, err := series.New(alpha, seed, biasCorrect)
	if err != nil {
		return nil, err
	}
	return &API{reg: reg}, nil
}

// Update folds x into key's mean; an empty key fails before allocation.
func (a *API) Update(key string, x float64) error {
	if key == "" {
		return ErrEmptyKey
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reg.GetOrCreate(key).Update(x)
	return nil
}

// Value returns key's mean: empty key → ErrEmptyKey, unknown → ErrNotFound.
func (a *API) Value(key string) (float64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	e, ok := a.reg.Get(key)
	if !ok {
		return 0, ErrNotFound
	}
	return e.Value(), nil
}

// Count returns the observation count for key; 0 for empty/unknown keys.
func (a *API) Count(key string) int {
	if key == "" {
		return 0
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	e, ok := a.reg.Get(key)
	if !ok {
		return 0
	}
	return int(e.Count())
}

// closedForm recomputes the mean non-recursively, independent of the
// recurrence under test.
func closedForm(alpha, seed float64, xs []float64, bc bool) float64 {
	beta, n := 1-alpha, float64(len(xs))
	s := math.Pow(beta, n) * seed
	for i, x := range xs {
		s += alpha * math.Pow(beta, float64(len(xs)-1-i)) * x
	}
	if bc {
		s /= 1 - math.Pow(beta, n)
	}
	return s
}

const eps = 1e-10

func closeEq(a, b float64) bool {
	return math.Abs(a-b) <= eps*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// SelfCheck verifies the four invariants on built-in random sequences.
func (a *API) SelfCheck() error {
	rng := rand.New(rand.NewSource(20260926))
	for _, bc := range []bool{false, true} {
		alpha := 0.1 + rng.Float64()*0.8
		seed := 0.0 // corrected range claim needs seed 0; uncorrected allows any
		if !bc {
			seed = rng.NormFloat64() * 10
		}
		s, err := New(alpha, seed, bc)
		if err != nil {
			return err
		}
		for trial := 0; trial < 16; trial++ {
			key, xs := fmt.Sprintf("k%d", trial), make([]float64, 1+rng.Intn(40))
			lo, hi := seed, seed
			for i := range xs {
				xs[i] = rng.NormFloat64() * 50
				lo, hi = math.Min(lo, xs[i]), math.Max(hi, xs[i])
				if err := s.Update(key, xs[i]); err != nil {
					return err
				}
			}
			v, err := s.Value(key)
			if err != nil {
				return err
			}
			if v < lo-eps || v > hi+eps { // invariant 1: range
				return fmt.Errorf("range violated: %v not in [%v,%v]", v, lo, hi)
			}
			if w := closedForm(alpha, seed, xs, bc); !closeEq(v, w) { // invariant 3
				return fmt.Errorf("closed form mismatch: %v != %v", v, w)
			}
		}
	}
	c, _ := New(0.9, 0, true) // invariant 2: constant input is exact
	for i := 0; i < 20; i++ {
		c.Update("c", 7)
		if v, _ := c.Value("c"); !closeEq(v, 7) {
			return fmt.Errorf("constant input: %v != 7", v)
		}
	}
	if bad, err := New(1.5, 0, false); !errors.Is(err, ErrInvalidAlpha) || bad != nil {
		return fmt.Errorf("invalid alpha not rejected") // invariant 4
	}
	g, _ := New(0.5, 0, false)
	if err := g.Update("", 1); !errors.Is(err, ErrEmptyKey) || g.Count("") != 0 {
		return fmt.Errorf("empty key left state behind")
	}
	if _, err := g.Value("missing"); !errors.Is(err, ErrNotFound) || g.Count("missing") != 0 {
		return fmt.Errorf("unknown key left state behind")
	}
	return nil
}
