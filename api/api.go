// Package api is the public face of the LogLog cardinality estimator.
// It depends on est (which depends on lg); nothing depends back.
package api

import (
	"errors"
	"math"

	"ontology/est"
)

// Sentinel errors; each rejection kind is distinguishable via errors.Is.
var (
	ErrInvalidM         = errors.New("api: m must be a positive power of two")
	ErrBucketOutOfRange = errors.New("api: bucket out of range")
	ErrInvalidZ         = errors.New("api: z must be >= 0")
)

// LogLog is a fixed-memory probabilistic cardinality estimator.
type LogLog struct {
	m   int
	est *est.Estimator
}

// New builds an estimator with m registers. m must be a positive power of
// two; otherwise it fails wholesale with ErrInvalidM and no state exists.
func New(m int) (*LogLog, error) {
	if m <= 0 || m&(m-1) != 0 {
		return nil, ErrInvalidM
	}
	return &LogLog{m: m, est: est.New(m)}, nil
}

// Add records one element given its injected (bucket, z). A rejected call
// changes no register at all.
func (l *LogLog) Add(bucket, z int) error {
	if bucket < 0 || bucket >= l.m {
		return ErrBucketOutOfRange
	}
	if z < 0 {
		return ErrInvalidZ
	}
	l.est.Add(bucket, z)
	return nil
}

// Estimate returns α_m · m · 2^mean.
func (l *LogLog) Estimate() float64 { return l.est.Estimate() }

// Registers returns a snapshot copy of the m registers.
func (l *LogLog) Registers() []int { return l.est.Snapshot() }

// seq is the built-in (bucket, z) sequence used by SelfCheck.
var seq = [][2]int{{0, 0}, {2, 2}, {2, 4}, {5, 1}, {0, 3}, {5, 5}, {2, 3}}

// SelfCheck verifies the four invariants on built-in (bucket, z) sequences
// using fresh internal instances; the receiver is never touched.
func (l *LogLog) SelfCheck() error {
	m := 8
	c, _ := New(m)
	naive := make([]int, m)
	prev := c.Registers()
	for _, p := range seq { // invariants 1+3: monotonic, matches naive replay
		if err := c.Add(p[0], p[1]); err != nil {
			return err
		}
		if r := p[1] + 1; r > naive[p[0]] {
			naive[p[0]] = r
		}
		cur := c.Registers()
		for j := range cur {
			if cur[j] < prev[j] {
				return errors.New("selfcheck: register decreased")
			}
			if cur[j] != naive[j] {
				return errors.New("selfcheck: differs from naive replay")
			}
		}
		prev = cur
	}
	sum := 0
	for _, v := range naive {
		sum += v
	}
	if want := est.Alpha(m) * float64(m) * math.Exp2(float64(sum)/float64(m)); c.Estimate() != want {
		return errors.New("selfcheck: estimate differs from mean formula")
	}
	one, _ := New(m) // invariant 2: single element exact
	if err := one.Add(3, 4); err != nil {
		return err
	}
	for j, v := range one.Registers() {
		if want := 5 * b2i(j == 3); v != want {
			return errors.New("selfcheck: single element not exact")
		}
	}
	if _, err := New(0); !errors.Is(err, ErrInvalidM) { // invariant 4
		return errors.New("selfcheck: m=0 not rejected")
	}
	if _, err := New(3); !errors.Is(err, ErrInvalidM) {
		return errors.New("selfcheck: m=3 not rejected")
	}
	before := c.Registers()
	for _, b := range [][2]int{{-1, 0}, {m, 0}, {0, -1}} {
		if c.Add(b[0], b[1]) == nil {
			return errors.New("selfcheck: bad add accepted")
		}
	}
	for j, v := range c.Registers() {
		if v != before[j] {
			return errors.New("selfcheck: rejected add left a trace")
		}
	}
	return c.Add(2, 5) // still usable afterwards
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
