package sparsevec

import (
	"math/big"
	"sort"
)

// naiveDot expands both vectors into dense maps and sums products in
// ascending index order. Only used on small test vectors.
func naiveDot(a, b Vector) float64 {
	dense := map[uint32]float64{}
	for _, e := range a {
		dense[e.Index] = e.Value
	}
	keys := []uint32{}
	for _, e := range b {
		if _, ok := dense[e.Index]; ok {
			keys = append(keys, e.Index)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	sum := 0.0
	for _, k := range keys {
		sum += dense[k] * valueAt(b, k)
	}
	return sum
}

func valueAt(v Vector, idx uint32) float64 {
	for _, e := range v {
		if e.Index == idx {
			return e.Value
		}
	}
	return 0
}

// bigDot computes the dot product with 256-bit precision as a reference.
func bigDot(a, b Vector) *big.Float {
	dense := map[uint32]float64{}
	for _, e := range a {
		dense[e.Index] = e.Value
	}
	acc := new(big.Float).SetPrec(256).SetFloat64(0)
	for _, e := range b {
		av, ok := dense[e.Index]
		if !ok {
			continue
		}
		term := new(big.Float).SetPrec(256).SetFloat64(av)
		term.Mul(term, new(big.Float).SetPrec(256).SetFloat64(e.Value))
		acc.Add(acc, term)
	}
	return acc
}

// relErr returns |got - ref| / |ref| as a float64.
func relErr(got float64, ref *big.Float) float64 {
	g := new(big.Float).SetPrec(256).SetFloat64(got)
	diff := new(big.Float).SetPrec(256).Sub(g, ref)
	abs := new(big.Float).SetPrec(256).Abs(diff)
	den := new(big.Float).SetPrec(256).Abs(ref)
	q := new(big.Float).SetPrec(256).Quo(abs, den)
	f, _ := q.Float64()
	return f
}

// sortedCopy returns a copy of elems sorted by index; input is untouched.
func sortedCopy(elems []Element) Vector {
	out := make(Vector, len(elems))
	copy(out, elems)
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}
