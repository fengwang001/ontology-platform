// Command demo exercises the sparse vector dot product and cosine
// similarity package, printing one OK/FAIL verdict per scenario.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"

	"ontology/sparse"
)

var passed, failed int

func ent(i uint32, v float64) sparse.Entry {
	return sparse.Entry{Index: i, Value: v}
}

func check(name string, ok bool, detail string) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s: %s\n", verdict, name, detail)
}

func naiveDot(a, b sparse.Vector) float64 {
	dense := map[uint32]float64{}
	for _, e := range a {
		dense[e.Index] = e.Value
	}
	sum := 0.0
	for _, e := range b {
		if v, ok := dense[e.Index]; ok {
			sum += v * e.Value
		}
	}
	return sum
}

func bigDot(a, b sparse.Vector) float64 {
	vals := map[uint32]*big.Float{}
	for _, e := range a {
		vals[e.Index] = new(big.Float).SetPrec(256).SetFloat64(e.Value)
	}
	sum := new(big.Float).SetPrec(256)
	for _, e := range b {
		if v, ok := vals[e.Index]; ok {
			sum.Add(sum, new(big.Float).SetPrec(256).Mul(v, big.NewFloat(e.Value)))
		}
	}
	r, _ := sum.Float64()
	return r
}

func main() {
	big1 := sparse.Vector{ent(1, 3), ent(500_000_000, -2), ent(1_000_000_000, 0.5)}
	big2 := sparse.Vector{ent(7, 4), ent(500_000_000, 1.5), ent(1_000_000_000, 2)}

	d, st, err := sparse.Dot(big1, big2)
	check("billion-index steps", err == nil && st.Steps < 10,
		fmt.Sprintf("steps=%d (indices up to 1e9)", st.Steps))

	check("naive expansion match", err == nil && d == naiveDot(big1, big2),
		fmt.Sprintf("dot=%v naive=%v", d, naiveDot(big1, big2)))

	bad := sparse.Vector{ent(5, 1), ent(3, 2)}
	var oe *sparse.OrderError
	_, _, err = sparse.Dot(bad, big2)
	check("descending index located", errors.As(err, &oe) && oe.Vector == 0 && oe.Position == 1,
		fmt.Sprintf("err=%v", err))

	nanVec := sparse.Vector{ent(0, math.NaN())}
	var ne *sparse.NaNError
	_, _, err = sparse.Dot(big1, nanVec)
	check("NaN value located", errors.As(err, &ne) && ne.Vector == 1 && ne.Position == 0,
		fmt.Sprintf("err=%v", err))

	withZero := sparse.Vector{ent(1, 2), ent(5, 0), ent(9, 3)}
	withoutZero := sparse.Vector{ent(1, 2), ent(9, 3)}
	other := sparse.Vector{ent(1, 7), ent(5, 11), ent(9, 13)}
	dz, sz, _ := sparse.Dot(withZero, other)
	dn, _, _ := sparse.Dot(withoutZero, other)
	check("explicit zero neutral", sz.ExplicitZeros == 1 && dz == dn,
		fmt.Sprintf("zeros=%d dot=%v (unchanged)", sz.ExplicitZeros, dz))

	ma := sparse.Vector{ent(0, 1e16), ent(1, 1), ent(2, -1e16)}
	mb := sparse.Vector{ent(0, 1), ent(1, 1), ent(2, 1)}
	dm, _, _ := sparse.Dot(ma, mb)
	ref := bigDot(ma, mb)
	check("mixed 1e16 and 1 vs big", math.Abs(dm-ref)/math.Abs(ref) <= 1e-15,
		fmt.Sprintf("dot=%v big-ref=%v", dm, ref))

	v := sparse.Vector{ent(0, 0.1), ent(7, 0.3), ent(42, -0.7)}
	c, _, err := sparse.Cosine(v, v)
	check("identical cosine exactly 1", err == nil && c == 1,
		fmt.Sprintf("cosine=%v", c))

	zero := sparse.Vector{ent(0, 0), ent(3, 0)}
	var ze *sparse.ZeroNormError
	_, _, err = sparse.Cosine(zero, v)
	check("zero-norm cosine error", errors.As(err, &ze) && ze.Vector == 0,
		fmt.Sprintf("err=%v", err))

	inf := sparse.Vector{ent(0, math.Inf(1))}
	var nfe *sparse.NonFiniteError
	_, _, err = sparse.Dot(inf, v)
	check("infinite value error", errors.As(err, &nfe),
		fmt.Sprintf("err=%v", err))

	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
