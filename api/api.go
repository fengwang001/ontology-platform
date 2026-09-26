// Package api is the public entry point for Householder QR. It depends on qr.
package api

import (
	"errors"

	"ontology/hh"
	"ontology/qr"
)

// Decidable sentinel errors (aliases of qr's distinct sentinels).
var (
	ErrDimMismatch = qr.ErrDimMismatch // len(a) != n*n
	ErrEmptyMatrix = qr.ErrEmptyMatrix // n < 1
	ErrZeroTail    = qr.ErrZeroTail    // a column tail is zero end to end
)

const eps = 1e-9

// API is the stateless public facade. New returns a ready-to-use instance.
type API struct{}

// New creates an API. A rejected call leaves it fully usable afterwards.
func New() *API { return &API{} }

// Factor returns Q, R with a = Q R. a is treated as read-only.
func (*API) Factor(a []float64, n int) (Q, R []float64, err error) {
	return qr.Factor(a, n)
}

// SelfCheck runs the four invariants over built-in matrices and returns a
// non-nil error on the first violation. It reports pass/fail only.
func (x *API) SelfCheck() error {
	cases := []struct {
		n int
		a []float64
	}{
		{1, []float64{7}},
		{1, []float64{-7}},
		{2, []float64{3, 1, 4, 1}},
		{2, []float64{0, 1, 1, 0}},   // zero first element, sign(0)=+1
		{2, []float64{-3, -1, 4, 2}}, // negative first element
		{3, []float64{1, 2, 3, 0, 1, 4, 5, 6, 0}},
		{4, []float64{2, -1, 0, 3, 1, 0, 1, -2, 0, 4, 2, 1, -3, 1, 2, 1}},
	}
	for _, c := range cases {
		src := append([]float64(nil), c.a...)
		Q, R, err := qr.Factor(c.a, c.n)
		if err != nil {
			return err
		}
		if err := checkQR(Q, R, c.a, c.n); err != nil { // invariants 1+2
			return err
		}
		for i := range src { // input must be read-only
			if c.a[i] != src[i] {
				return errors.New("api: Factor mutated its input")
			}
		}
	}
	if err := checkEarlierColumns(); err != nil { // invariant 3
		return err
	}
	return checkRejections(x) // invariant 4
}

// checkQR verifies Q*R == a, Q^T Q == I and R upper triangular, all within eps.
func checkQR(Q, R, a []float64, n int) error {
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			var qr, qq float64
			for t := 0; t < n; t++ {
				qr += Q[i*n+t] * R[t*n+j]
				qq += Q[t*n+i] * Q[t*n+j]
			}
			if d := qr - a[i*n+j]; d > eps || d < -eps {
				return errors.New("api: Q*R does not reproduce A")
			}
			want := 0.0
			if i == j {
				want = 1
			}
			if d := qq - want; d > eps || d < -eps {
				return errors.New("api: Q is not orthogonal")
			}
			if i > j && (R[i*n+j] > eps || R[i*n+j] < -eps) {
				return errors.New("api: R is not upper triangular")
			}
		}
	}
	return nil
}

// checkEarlierColumns drives hh.Apply directly and compares columns 0..k-1
// byte-for-byte before and after.
func checkEarlierColumns() error {
	n, k := 4, 1
	a := []float64{2, -1, 0, 3, 1, 0, 1, -2, 0, 4, 2, 1, -3, 1, 2, 1}
	tail := make([]float64, n-k)
	for i := range tail {
		tail[i] = a[(k+i)*n+k]
	}
	v, ok := hh.Reflector(tail)
	if !ok {
		return errors.New("api: expected a reflector for the fixture")
	}
	before := append([]float64(nil), a...)
	hh.Apply(v, a, n, k)
	for i := 0; i < n; i++ {
		for j := 0; j < k; j++ { // columns 0..k-1, every row
			if a[i*n+j] != before[i*n+j] {
				return errors.New("api: reflector rewrote an earlier column")
			}
		}
	}
	return nil
}

func checkRejections(x *API) error {
	bs := []struct {
		a       []float64
		n       int
		wantErr error
	}{
		{[]float64{1, 2, 3}, 2, ErrDimMismatch}, // 3 elements, n*n = 4
		{[]float64{}, 0, ErrEmptyMatrix},
		{[]float64{0, 1, 0, 1}, 2, ErrZeroTail}, // column 0 tail is [0,0]
	}
	for _, b := range bs {
		if _, _, err := x.Factor(b.a, b.n); !errors.Is(err, b.wantErr) {
			return errors.New("api: rejection did not return its sentinel error")
		}
	}
	if _, _, err := x.Factor([]float64{3, 1, 4, 1}, 2); err != nil {
		return errors.New("api: API unusable after rejection")
	}
	return nil
}
