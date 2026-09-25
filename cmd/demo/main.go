// 演示稀疏向量点积/余弦计算器。运行：go run ./cmd/demo
package main

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"

	"ontology/sparse"
)

var pass, fail int

func report(name string, ok bool, detail string) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		fail++
	} else {
		pass++
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

// naiveDense 在小规模上展开稠密数组朴素累加，仅作演示对拍。
func naiveDense(a, b sparse.Vector, dim int) float64 {
	da := make([]float64, dim)
	db := make([]float64, dim)
	for _, e := range a {
		da[e.Index] = e.Value
	}
	for _, e := range b {
		db[e.Index] = e.Value
	}
	var s float64
	for i := 0; i < dim; i++ {
		s += da[i] * db[i]
	}
	return s
}

func bigReference(a, b sparse.Vector) *big.Float {
	const prec = 200
	m := map[uint32]*big.Float{}
	for _, x := range a {
		for _, y := range b {
			if x.Index == y.Index {
				p := new(big.Float).SetPrec(prec)
				p.Mul(new(big.Float).SetPrec(prec).SetFloat64(x.Value),
					new(big.Float).SetPrec(prec).SetFloat64(y.Value))
				if old, ok := m[x.Index]; ok {
					p.Add(old, p)
				}
				m[x.Index] = p
			}
		}
	}
	sum := new(big.Float).SetPrec(prec)
	for _, p := range m {
		sum.Add(sum, p)
	}
	return sum
}

func main() {
	// 1) 十亿下标：推进步数为个位数。
	a := sparse.Vector{
		{Index: 0, Value: 1}, {Index: 500_000_000, Value: 2},
		{Index: 1_000_000_000, Value: 3},
	}
	b := sparse.Vector{
		{Index: 1, Value: 4}, {Index: 500_000_001, Value: 5},
		{Index: 1_000_000_000, Value: 6},
	}
	r1, err := sparse.Dot(a, b)
	report("billion-index steps",
		err == nil && r1.Steps < 10 && r1.Dot == 18,
		fmt.Sprintf("steps=%d dot=%v", r1.Steps, r1.Dot))

	// 2) 与朴素展开对拍（小规模）逐位一致。
	smallA := sparse.Vector{
		{Index: 0, Value: 2}, {Index: 2, Value: -3}, {Index: 4, Value: 5},
	}
	smallB := sparse.Vector{
		{Index: 1, Value: 7}, {Index: 2, Value: 4}, {Index: 4, Value: -2},
	}
	r2, _ := sparse.Dot(smallA, smallB)
	refDense := naiveDense(smallA, smallB, 5)
	report("naive dense cross-check",
		math.Float64bits(r2.Dot) == math.Float64bits(refDense),
		fmt.Sprintf("sparse=%v dense=%v", r2.Dot, refDense))

	// 3) 下标递减错误带向量号与位置。
	_, err = sparse.Dot(
		sparse.Vector{{Index: 2, Value: 1}, {Index: 0, Value: 2}},
		sparse.Vector{{Index: 0, Value: 1}},
	)
	var ve *sparse.ValidationError
	loc := errors.As(err, &ve) && errors.Is(err, sparse.ErrNotSorted) &&
		ve.Vector == sparse.VectorA && ve.Pos == 1
	report("descending index error", loc, fmt.Sprintf("err=%v", err))

	// 4) NaN 值错误。
	_, err = sparse.Dot(
		sparse.Vector{{Index: 0, Value: 1}},
		sparse.Vector{{Index: 0, Value: math.NaN()}},
	)
	report("NaN value error", errors.Is(err, sparse.ErrNaN), fmt.Sprintf("err=%v", err))

	// 5) 显式零元素计数且不影响点积。
	withZero := sparse.Vector{
		{Index: 0, Value: 2}, {Index: 1, Value: 0},
		{Index: 5, Value: 0}, {Index: 9, Value: 4},
	}
	noZero := sparse.Vector{{Index: 0, Value: 2}, {Index: 9, Value: 4}}
	r5a, _ := sparse.Dot(withZero, smallB)
	r5b, _ := sparse.Dot(noZero, smallB)
	report("explicit zeros",
		r5a.ExplicitZeros == 2 && math.Float64bits(r5a.Dot) == math.Float64bits(r5b.Dot),
		fmt.Sprintf("zeros=%d dot=%v (without-zeros dot=%v)",
			r5a.ExplicitZeros, r5a.Dot, r5b.Dot))

	// 6) 1e16 与 1 混合：与 math/big 高精度参考的相对误差。
	magA := sparse.Vector{
		{Index: 0, Value: 1e16}, {Index: 1, Value: 1}, {Index: 2, Value: 1},
	}
	magB := sparse.Vector{
		{Index: 0, Value: 1}, {Index: 1, Value: 1}, {Index: 2, Value: 1},
	}
	r6, _ := sparse.Dot(magA, magB)
	bigRef, _ := bigReference(magA, magB).Float64()
	relErr := math.Abs(r6.Dot-bigRef) / math.Abs(bigRef)
	report("magnitude vs big reference",
		relErr <= 1e-15 && r6.Dot == 1e16+2,
		fmt.Sprintf("dot=%v big=%v relErr=%.2e (Kahan)", r6.Dot, bigRef, relErr))

	// 7) 相同向量余弦精确为 1。
	c7, _, err := sparse.Cosine(smallA, append(sparse.Vector(nil), smallA...))
	report("identical cosine == exact 1",
		err == nil && math.Float64bits(c7) == math.Float64bits(1.0),
		fmt.Sprintf("cos=%.17g", c7))

	// 8) 全零向量余弦无定义。
	_, _, err = sparse.Cosine(
		sparse.Vector{{Index: 0, Value: 0}, {Index: 1, Value: 0}}, smallB)
	report("zero-norm cosine error", errors.Is(err, sparse.ErrZeroNorm),
		fmt.Sprintf("err=%v", err))

	// 9) 无穷值导致可判定错误。
	_, _, err = sparse.Cosine(
		sparse.Vector{{Index: 0, Value: 1}},
		sparse.Vector{{Index: 0, Value: math.Inf(1)}})
	report("infinity error", errors.Is(err, sparse.ErrInf),
		fmt.Sprintf("err=%v", err))

	fmt.Printf("---- total: %d passed, %d failed ----\n", pass, fail)
	if fail != 0 {
		os.Exit(1)
	}
}
