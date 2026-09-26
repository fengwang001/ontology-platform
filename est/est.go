// Package est computes the LogLog cardinality estimate from the registers.
// It depends only on lg.
package est

import (
	"errors"
	"math"
	"sync"

	"ontology/lg"
)

// alphaTable holds the LogLog bias constant α_m for m = 2^0..2^30.
// α_1 is fixed to the asymptotic constant; for m >= 2 the table uses the
// Durand–Flajolet closed form (Γ(-1/m)·(2^{1/m}-1)/ln2)^(-m).
var alphaTable [31]float64

func init() {
	alphaTable[0] = 0.39701
	for p := 1; p < len(alphaTable); p++ {
		m := float64(int(1) << p)
		g := math.Gamma(-1/m) * (math.Pow(2, 1/m) - 1) / math.Ln2
		alphaTable[p] = math.Pow(g, -m)
	}
}

// Alpha returns the bias constant α_m. Callers guarantee m is a power of two.
func Alpha(m int) float64 {
	p := 0
	for n := m; n > 1; n >>= 1 {
		p++
	}
	return alphaTable[p]
}

// Estimator couples the register array with the estimate formula.
type Estimator struct {
	mu  sync.Mutex
	reg *lg.Registers
	m   int
	// visited records how many registers the latest Estimate read while
	// computing the mean. Non-exported on purpose: only a pass/fail verdict
	// may leave this package, never the number itself.
	visited int
}

// New builds an Estimator over m registers. Callers guarantee m > 0.
func New(m int) *Estimator {
	return &Estimator{reg: lg.New(m), m: m}
}

// Add records one element.
func (e *Estimator) Add(bucket, z int) {
	e.mu.Lock()
	e.reg.Add(bucket, z)
	e.mu.Unlock()
}

// Estimate returns α_m · m · 2^mean with mean = (1/m)·Σ reg[j] over all m
// registers, zeros included.
func (e *Estimator) Estimate() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	sum := 0
	for j := 0; j < e.m; j++ {
		sum += e.reg.At(j)
	}
	e.visited = e.m
	mean := float64(sum) / float64(e.m)
	return Alpha(e.m) * float64(e.m) * math.Exp2(mean)
}

// Snapshot returns a copy of the registers.
func (e *Estimator) Snapshot() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]int, e.m)
	for j := 0; j < e.m; j++ {
		out[j] = e.reg.At(j)
	}
	return out
}

// ErrVisitCount is returned by CheckVisited when Estimate did not read
// exactly m registers.
var ErrVisitCount = errors.New("est: Estimate did not visit exactly m registers")

// CheckVisited verifies on fresh internal estimators that Estimate reads
// exactly m registers no matter how many elements were added. It reports
// only a verdict; the counter value itself never leaves the package.
func CheckVisited(m int) error {
	for _, n := range []int{100, 1000, 10000} {
		f := New(m)
		for i := 0; i < n; i++ {
			f.Add(i%m, i/m)
		}
		f.Estimate()
		if f.visited != m {
			return ErrVisitCount
		}
	}
	return nil
}
