// Package bound converts between error parameters (epsilon, delta) and
// sketch dimensions (w, d), and expresses the error bound checkably.
package bound

import (
	"errors"
	"math"

	"ontology/sketch"
)

// ErrBadProbability rejects epsilon/delta outside the open interval (0,1).
var ErrBadProbability = errors.New("bound: epsilon and delta must be in (0,1)")

// Params are sketch dimensions with their implied error guarantee:
// overestimate <= Epsilon()*N with probability >= 1-Delta().
type Params struct {
	W, D int
}

// New validates directly given dimensions.
func New(w, d int) (Params, error) {
	if w <= 0 || d <= 0 {
		return Params{}, sketch.ErrBadDimensions
	}
	return Params{W: w, D: d}, nil
}

// FromEpsilonDelta derives w = ceil(e/eps), d = ceil(ln(1/delta)).
// e comes from the Markov bound per row; ln(1/delta) from d independent rows.
func FromEpsilonDelta(eps, delta float64) (Params, error) {
	if eps <= 0 || eps >= 1 || delta <= 0 || delta >= 1 {
		return Params{}, ErrBadProbability
	}
	return New(int(math.Ceil(math.E/eps)), int(math.Ceil(math.Log(1/delta))))
}

// Epsilon returns e/w, a conservative (never overstated) relative error.
func (p Params) Epsilon() float64 { return math.E / float64(p.W) }

// Delta returns e^-d, a conservative failure probability.
func (p Params) Delta() float64 { return math.Exp(-float64(p.D)) }

// ErrorBound returns the absolute error ceiling Epsilon()*total.
func (p Params) ErrorBound(total uint64) uint64 {
	return uint64(math.Ceil(p.Epsilon() * float64(total)))
}
