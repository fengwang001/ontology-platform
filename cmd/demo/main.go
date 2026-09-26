// Command demo runs the determinant self-demonstration for ontology.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"

	"ontology/api"
	"ontology/elim"
	"ontology/pivot"
)

type step struct {
	c  []float64
	r  int
	sw bool
	s  int
}

func eliminate(v []float64, n int, sf, pp, lp bool) (float64, []step, []float64) {
	m := append([]float64(nil), v...)
	sign, ss := 1, []step{}
	for k := 0; k < n; k++ {
		r := k
		var cand []float64
		if pp {
			for i := k; i < n; i++ {
				cand = append(cand, math.Abs(m[i*n+k]))
			}
			p, ok := pivot.Pick(m, n, k)
			if !ok {
				return 0, ss, m
			}
			r = p
		}
		if r != k {
			for j := 0; j < n; j++ {
				m[k*n+j], m[r*n+j] = m[r*n+j], m[k*n+j]
			}
			if sf {
				sign = -sign
			}
		}
		ss = append(ss, step{cand, r, r != k, sign})
		for i := k + 1; i < n; i++ {
			f := m[i*n+k] / m[k*n+k]
			for j := k; j < n; j++ {
				m[i*n+j] -= f * m[k*n+j]
			}
		}
	}
	det := 1.0
	for k := 0; k < n; k++ {
		if k < n-1 || lp {
			det *= m[k*n+k]
		}
	}
	return float64(sign) * det, ss, m
}

func sameBits(x, y []float64) bool {
	for i := range x {
		if i >= len(y) || math.Float64bits(x[i]) != math.Float64bits(y[i]) {
			return false
		}
	}
	return len(x) == len(y)
}

func naiveDet(a []float64, n int) float64 {
	if n == 1 {
		return a[0]
	}
	d := 0.0
	for j := 0; j < n; j++ {
		s := make([]float64, 0, (n-1)*(n-1))
		for i := 1; i < n; i++ {
			s = append(s, a[i*n:i*n+j]...)
			s = append(s, a[i*n+j+1:(i+1)*n]...)
		}
		d += (1 - 2*float64(j&1)) * a[j] * naiveDet(s, n-1)
	}
	return d
}

var resultTag = map[bool]string{true: "OK ", false: "FAIL "}

func ck(name string, ok bool) { fmt.Print(resultTag[ok] + name + "\n") }

func main() {
	A := []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}
	det, ss, m := eliminate(A, 3, true, true, true)
	ws := []step{{[]float64{0, 1, 1}, 1, true, -1}, {[]float64{1, 1}, 1, false, -1}, {[]float64{2}, 2, false, -1}}
	ok := math.Abs(det-2) <= 1e-9 && len(ss) == 3 && sameBits(m, []float64{1, 0, 1, 0, 1, 1, 0, 0, -2})
	for i := range ws {
		ok = ok && ss[i].r == ws[i].r && ss[i].sw == ws[i].sw && ss[i].s == ws[i].s && sameBits(ss[i].c, ws[i].c)
	}
	ck("5 elimination steps (pivot/swap/sign/matrix)", ok)
	varD := func(sf, pp, lp bool) float64 { d, _, _ := eliminate(A, 3, sf, pp, lp); return d }
	ck("jia no sign flip -> -2", math.Abs(varD(false, true, true)+2) <= 1e-9)
	ck("yi no pivot -> NaN (div0 not 0)", math.IsNaN(varD(true, false, true)))
	ck("bing drop last pivot -> -1", math.Abs(varD(true, true, false)+1) <= 1e-9)
	x, rng := api.New(), rand.New(rand.NewSource(1))
	mats := [][]float64{A, {1, 2, 3, 2, 4, 6, 7, 8, 9}}
	for n := 1; n <= 5; n++ {
		t := make([]float64, n*n)
		for i := range t {
			t[i] = float64(rng.Intn(11) - 5)
		}
		mats = append(mats, t)
	}
	cases := append(append([][]float64{}, mats...), nil, []float64{1, 2, 3}, []float64{1, math.NaN(), 0, 1})
	ok5, ok6 := true, true
	for _, a := range cases {
		before := append([]float64(nil), a...)
		n := int(math.Sqrt(float64(len(a))))
		g, err := x.Det(a, n)
		ok5 = ok5 && (err != nil || math.Abs(g-naiveDet(a, n)) <= 1e-9)
		ok6 = ok6 && sameBits(a, before)
	}
	ck("Det matches naive expansion", ok5)
	ck("input never modified", ok6)
	_, e0 := x.Det(nil, 0)
	_, e1 := x.Det([]float64{1, 2, 3}, 2)
	_, e2 := x.Det([]float64{1, math.NaN(), 0, 1}, 2)
	ck("three distinct sentinel errors", errors.Is(e0, elim.ErrEmpty) && errors.Is(e1, elim.ErrDim) && errors.Is(e2, elim.ErrNaNOrInf) && e0.Error() != e1.Error() && e1.Error() != e2.Error())
	d, err := x.Det(A, 3)
	ck("state intact after rejection (det=2)", err == nil && d == 2)
	ck("zero ops on triangular n=100/1000/10000", elim.VerifyTriangularNoOps(100, 1000, 10000) == nil)
	shared := make([]float64, 64)
	for i := range shared {
		shared[i] = float64(rng.Intn(9) - 4)
	}
	res := make([]uint64, 32)
	var wg sync.WaitGroup
	wg.Add(len(res))
	for g := range res {
		go func(g int) { v, _ := x.Det(shared, 8); res[g] = math.Float64bits(v); wg.Done() }(g)
	}
	wg.Wait()
	ok10 := true
	for _, b := range res[1:] {
		ok10 = ok10 && b == res[0]
	}
	ck("concurrent Det byte-identical", ok10)
}
