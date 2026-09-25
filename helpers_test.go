package sparse

import (
	"math/big"
	"sort"
)

// naiveDotMap is the naive reference: expand both vectors into dense
// maps and sum products over matching indices, in ascending index order.
func naiveDotMap(a, b Vector) float64 {
	ma := make(map[uint32]float64, len(a))
	for _, e := range a {
		ma[e.Index] = e.Value
	}
	mb := make(map[uint32]float64, len(b))
	for _, e := range b {
		mb[e.Index] = e.Value
	}
	var common []uint32
	for k := range ma {
		if _, ok := mb[k]; ok {
			common = append(common, k)
		}
	}
	sort.Slice(common, func(i, j int) bool { return common[i] < common[j] })
	sum := 0.0
	for _, k := range common {
		sum += ma[k] * mb[k]
	}
	return sum
}

// bigDot is the high-precision reference: exact float64 -> big.Float
// conversion, products and sum carried out at 256-bit precision.
func bigDot(a, b Vector) *big.Float {
	ma := make(map[uint32]float64, len(a))
	for _, e := range a {
		ma[e.Index] = e.Value
	}
	acc := new(big.Float).SetPrec(256).SetFloat64(0)
	term := new(big.Float).SetPrec(256)
	for _, e := range b {
		if va, ok := ma[e.Index]; ok {
			term.SetFloat64(va)
			term.Mul(term, new(big.Float).SetPrec(256).SetFloat64(e.Value))
			acc.Add(acc, term)
		}
	}
	return acc
}

// relErr returns |got - ref| / |ref| as a float64.
func relErr(got float64, ref *big.Float) float64 {
	diff := new(big.Float).SetPrec(256).SetFloat64(got)
	diff.Sub(diff, ref)
	diff.Abs(diff)
	den := new(big.Float).SetPrec(256).Abs(ref)
	if den.Sign() == 0 {
		f, _ := diff.Float64()
		return f
	}
	q := new(big.Float).SetPrec(256).Quo(diff, den)
	f, _ := q.Float64()
	return f
}
