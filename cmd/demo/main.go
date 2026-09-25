// Command demo exercises the sparse dot/cosine engine end to end and
// prints one OK/FAIL verdict per requirement, then a summary line.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"

	sparse "ontology"
)

var failures int

func check(ok bool, format string, args ...any) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", verdict, fmt.Sprintf(format, args...))
}

func main() {
	// 1. Billion-scale indices: merge steps stay single digit.
	big1 := &sparse.Vector{Elems: []sparse.Element{
		{Index: 0, Value: 1}, {Index: 500_000_000, Value: 2}, {Index: 1_000_000_000, Value: 3},
	}}
	big2 := &sparse.Vector{Elems: []sparse.Element{
		{Index: 1, Value: 1}, {Index: 500_000_000, Value: 4}, {Index: 1_000_000_000, Value: 5},
	}}
	dot, stats, err := sparse.Dot(big1, big2)
	check(err == nil && stats.Steps < 10, "billion indices: steps=%d dot=%v", stats.Steps, dot)

	// 2. Cross-check against naive dense expansion.
	naive := 2.0*4 + 3.0*5
	check(err == nil && dot == naive, "naive dense cross-check: merge=%v naive=%v", dot, naive)

	// 3. Decreasing indices: located validation error.
	bad := &sparse.Vector{Elems: []sparse.Element{{Index: 7, Value: 1}, {Index: 3, Value: 1}}}
	_, _, err = sparse.Dot(big1, bad)
	var ve *sparse.ValidationError
	ok := errors.As(err, &ve) && ve.Vector == 1 && ve.Position == 1
	check(ok, "decreasing index error: %v", err)

	// 4. NaN value: located validation error.
	nanVec := &sparse.Vector{Elems: []sparse.Element{{Index: 0, Value: math.NaN()}}}
	_, _, err = sparse.Dot(nanVec, big2)
	ok = errors.As(err, &ve) && ve.Vector == 0 && ve.Position == 0
	check(ok, "NaN value error: %v", err)

	// 5. Explicit zeros: counted, dot unaffected.
	z1 := &sparse.Vector{Elems: []sparse.Element{{Index: 0, Value: 0}, {Index: 1, Value: 2}}}
	z2 := &sparse.Vector{Elems: []sparse.Element{{Index: 0, Value: 3}, {Index: 1, Value: 0}}}
	dot, stats, err = sparse.Dot(z1, z2)
	check(err == nil && stats.ExplicitZeros == 2 && dot == 0,
		"explicit zeros: count=%d dot=%v", stats.ExplicitZeros, dot)

	// 6. Mixed magnitudes 1e16 and 1 vs math/big reference.
	m1 := &sparse.Vector{Elems: []sparse.Element{
		{Index: 0, Value: 1e16}, {Index: 1, Value: 1}, {Index: 2, Value: -1e16},
	}}
	m2 := &sparse.Vector{Elems: []sparse.Element{
		{Index: 0, Value: 1}, {Index: 1, Value: 1}, {Index: 2, Value: 1},
	}}
	dot, _, err = sparse.Dot(m1, m2)
	ref := new(big.Float).SetPrec(256)
	for i, e := range m1.Elems {
		term := new(big.Float).SetPrec(256).SetFloat64(e.Value)
		term.Mul(term, new(big.Float).SetPrec(256).SetFloat64(m2.Elems[i].Value))
		ref.Add(ref, term)
	}
	ref64, _ := ref.Float64()
	check(err == nil && dot == ref64, "compensated sum: got=%v big-ref=%v", dot, ref64)

	// 7. Identical vectors: cosine is exactly 1.
	cos, _, err := sparse.Cosine(m1, m1)
	check(err == nil && cos == 1, "identical vectors: cosine=%v", cos)

	// 8. Zero-norm vector: cosine is a defined error, not NaN.
	zero := &sparse.Vector{Elems: []sparse.Element{{Index: 0, Value: 0}}}
	_, _, err = sparse.Cosine(zero, m2)
	var zn *sparse.ZeroNormError
	check(errors.As(err, &zn) && zn.Vector == 0, "zero-norm cosine error: %v", err)

	// 9. Infinite values: defined error instead of NaN.
	inf := &sparse.Vector{Elems: []sparse.Element{{Index: 0, Value: math.Inf(1)}}}
	_, _, err = sparse.Dot(inf, m2)
	var ne *sparse.NumericError
	check(errors.As(err, &ne), "infinite value error: %v", err)

	fmt.Printf("TOTAL %d/9 checks passed\n", 9-failures)
	if failures > 0 {
		os.Exit(1)
	}
}
