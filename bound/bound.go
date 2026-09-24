// Package bound converts between error parameters (eps, delta) and sketch
// dimensions (w, d), and expresses the error bound in checkable form.
// Formulas: w = ceil(e/eps), d = ceil(ln(1/delta)); see DESIGN.md section 3.
package bound

import (
	"errors"
	"math"

	"ontology/sketch"
)

// ErrInvalidProb reports eps or delta outside the open interval (0,1).
var ErrInvalidProb = errors.New("bound: epsilon and delta must be in (0,1)")

// Params derives (w, d) from (eps, delta).
func Params(eps, delta float64) (w, d int, err error) {
	if !(eps > 0 && eps < 1) || !(delta > 0 && delta < 1) {
		return 0, 0, ErrInvalidProb
	}
	return int(math.Ceil(math.E / eps)), int(math.Ceil(math.Log(1 / delta))), nil
}

// Epsilon is the nominal per-row error fraction implied by width w: e/w.
func Epsilon(w int) float64 { return math.E / float64(w) }

// Delta is the nominal confidence implied by depth d: e^-d. It is a design
// target, not a guarantee against adversarial input (DESIGN.md section 3).
func Delta(d int) float64 { return math.Exp(-float64(d)) }

// ErrorBound returns ceil(eps * total): the nominal worst overestimate.
func ErrorBound(w int, total uint64) uint64 {
	return uint64(math.Ceil(Epsilon(w) * float64(total)))
}

// Verify checks the estimate of key against its true count: it must satisfy
// true <= estimate <= true + eps*N (the never-underestimate invariant plus
// the nominal error upper bound).
func Verify(s *sketch.Sketch, key string, trueCount uint64) (bool, error) {
	est, err := s.Estimate(key)
	if err != nil {
		return false, err
	}
	w, _ := s.Dims()
	return est >= trueCount && est <= trueCount+ErrorBound(w, s.Total()), nil
}
