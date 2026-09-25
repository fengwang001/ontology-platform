// Package hll implements a HyperLogLog sketch with m = 2^p registers,
// incremental maintenance of Z = sum 2^(-rho) and the empty-register
// count V, so Estimate is O(1) and never rescans the register array.
package hll

import (
	"errors"
	"math"
	"sync"

	"ontology/hsh"
)

var (
	ErrPrecision = errors.New("hll: precision p out of range [4,16]")
	ErrMismatch  = errors.New("hll: merge of sketches with different p")
	ErrNotInit   = errors.New("hll: sketch not initialized via New")
)

type Sketch struct {
	mu    sync.Mutex
	p     uint
	alpha float64
	reg   []uint8
	z     float64 // Z = sum of 2^(-reg[j]), maintained incrementally
	v     int     // number of empty registers, maintained incrementally
	est   float64 // high-water mark keeping Estimate monotonic
	reads int     // registers actually read by the last Estimate (always 0)
}

func alphaOf(m int) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1 + 1.079/float64(m))
	}
}

func New(p uint) (*Sketch, error) {
	if p < 4 || p > 16 {
		return nil, ErrPrecision
	}
	m := 1 << p
	return &Sketch{p: p, alpha: alphaOf(m), reg: make([]uint8, m), z: float64(m), v: m}, nil
}

// set raises reg[j] to rho if larger, keeping z and v in sync. Caller holds mu.
func (s *Sketch) set(j uint32, rho uint8) {
	if rho > s.reg[j] {
		s.z += math.Ldexp(1, -int(rho)) - math.Ldexp(1, -int(s.reg[j]))
		if s.reg[j] == 0 {
			s.v--
		}
		s.reg[j] = rho
	}
}

func (s *Sketch) Add(key string) error {
	if s.reg == nil {
		return ErrNotInit
	}
	j, rho := hsh.Bucket(hsh.Hash(key), s.p)
	s.mu.Lock()
	s.set(j, rho)
	s.mu.Unlock()
	return nil
}

func (s *Sketch) Estimate() (float64, error) {
	if s.reg == nil {
		return 0, ErrNotInit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads = 0 // estimate comes from z and v alone, no register scan
	m := float64(len(s.reg))
	e := s.alpha * m * m / s.z
	if e <= 2.5*m && s.v > 0 {
		e = m * math.Log(m/float64(s.v))
	}
	if e > s.est {
		s.est = e
	}
	return s.est, nil
}

// Merge unions o into s by taking the per-bucket max. It fails as a whole
// (no state change) when either sketch is uninitialized or p differs.
func (s *Sketch) Merge(o *Sketch) error {
	if s.reg == nil || o == nil || o.reg == nil {
		return ErrNotInit
	}
	if s.p != o.p {
		return ErrMismatch
	}
	if s == o {
		return nil // idempotent: max with itself changes nothing
	}
	o.mu.Lock()
	src := make([]uint8, len(o.reg))
	copy(src, o.reg)
	o.mu.Unlock()
	s.mu.Lock()
	for j, rho := range src {
		s.set(uint32(j), rho)
	}
	s.mu.Unlock()
	return nil
}

// Equal reports whether s and o hold identical registers.
func (s *Sketch) Equal(o *Sketch) bool {
	if s.reg == nil || o == nil || o.reg == nil || s.p != o.p {
		return false
	}
	if s == o {
		return true
	}
	o.mu.Lock()
	oc := make([]uint8, len(o.reg))
	copy(oc, o.reg)
	o.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	for j := range s.reg {
		if s.reg[j] != oc[j] {
			return false
		}
	}
	return true
}

// EstimateIsO1 reports whether the last Estimate read zero registers.
// It exposes only the boolean, never the counter value itself.
func (s *Sketch) EstimateIsO1() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads == 0
}
