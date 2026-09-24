// Package ws holds the Welford online state for a single numeric group:
// (n, mean, M2) with M2 = Σ(xᵢ−mean)². It depends on no other package.
package ws

import "math"

// State is a single group's sufficient statistics. Zero value is an empty group.
type State struct {
	n    int64
	mean float64
	m2   float64
}

// New returns an empty State.
func New() *State { return &State{} }

// Add inserts x using the Welford update:
//
//	delta = x − mean; n' = n+1; mean' = mean + delta/n';
//	M2' = M2 + delta*(x − mean').
func (s *State) Add(x float64) {
	s.n++
	delta := x - s.mean
	s.mean += delta / float64(s.n)
	s.m2 += delta * (x - s.mean)
}

// Remove withdraws an element known to equal x. The caller must guarantee
// that such an element exists. It uses the exact inverse Welford step:
//
//	n' = n−1; mean' = mean + (mean − x)/n';
//	M2' = M2 − (x − mean)*(x − mean').
func (s *State) Remove(x float64) {
	if s.n <= 1 {
		s.n, s.mean, s.m2 = 0, 0, 0
		return
	}
	old := s.mean
	s.n--
	s.mean = old + (old-x)/float64(s.n)
	s.m2 -= (x - old) * (x - s.mean)
}

// Merge folds another non-empty group o into s:
//
//	n = n1+n2; delta = mean2−mean1;
//	mean = mean1 + delta*n2/n;
//	M2 = M2_1 + M2_2 + delta²*n1*n2/n.
//
// The caller guarantees o is non-empty and is not s itself.
func (s *State) Merge(o *State) {
	n1, n2 := float64(s.n), float64(o.n)
	n := n1 + n2
	delta := o.mean - s.mean
	s.mean += delta * n2 / n
	s.m2 += o.m2 + delta*delta*n1*n2/n
	s.n += o.n
}

// Count returns the number of elements.
func (s State) Count() int64 { return s.n }

// Mean returns the current mean (0 for an empty group).
func (s State) Mean() float64 { return s.mean }

// M2 returns the summed squared deviation from the mean.
func (s State) M2() float64 { return s.m2 }

// Variance is the population variance M2/n (0 for an empty group).
func (s State) Variance() float64 {
	if s.n == 0 {
		return 0
	}
	return s.m2 / float64(s.n)
}

// Std is the population standard deviation sqrt(M2/n).
func (s State) Std() float64 { return math.Sqrt(s.Variance()) }
