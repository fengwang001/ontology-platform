package eigen

import (
	"errors"
	"math"
	"testing"

	"ontology/power"
)

// veq 比较两向量；tol<0 时逐位比较，否则按容差比较。
func veq(x, y []float64, tol float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if tol < 0 {
			if math.Float64bits(x[i]) != math.Float64bits(y[i]) {
				return false
			}
		} else if math.Abs(x[i]-y[i]) > tol {
			return false
		}
	}
	return true
}

func TestPowerPrimitives(t *testing.T) { // 白盒顺带覆盖无独立测试文件的 power 包
	a := []float64{3, 1, 1, 3}
	for i, r := range []struct{ v, w []float64 }{
		{[]float64{1, 0}, []float64{3, 1}},
		{[]float64{1, 1.0 / 3}, []float64{10.0 / 3, 2}},
		{[]float64{-1, 1}, []float64{-2, 2}},
	} {
		w := power.MatVec(a, r.v, 2)
		if !veq(w, r.w, 1e-12) || math.Abs(power.InfNorm(w)-power.InfNorm(r.w)) > 1e-12 {
			t.Fatalf("row %d w=%v", i, w)
		}
	}
	if math.Abs(power.Rayleigh(a, []float64{1, 1.0 / 3}, 2)-3.6) > 1e-12 || power.InfNorm(nil) != 0 {
		t.Fatal("Rayleigh or InfNorm(nil)")
	}
}

func TestIterate(t *testing.T) {
	// 前三例一步精确收敛、残差严格 0；末例受 float64 Rayleigh 判据地板限制（见 NOTES）。
	for i, c := range []struct {
		a, v0 []float64
		tol   float64
		l, rt float64
	}{
		{[]float64{1, 1, 1, 1}, []float64{1, 0}, 1e-13, 2, 1e-9},
		{[]float64{-2, 2, 2, -2}, []float64{0, 1}, 1e-13, -4, 1e-9},
		{[]float64{3, 0, -3, 0, 0, 0, -3, 0, 3}, []float64{1, 0, 0}, 1e-13, 6, 1e-9},
		{[]float64{3, 1, 1, 3}, []float64{1, 0}, 1e-14, 4, 1e-7},
	} {
		n := len(c.v0)
		lam, v, it, err := Iterate(c.a, c.v0, n, c.tol, 100000)
		w := power.MatVec(c.a, v, n)
		ok := err == nil && it >= 1 && math.Abs(lam-c.l) <= 1e-9 && math.Abs(power.InfNorm(v)-1) <= 1e-12
		for j := range v {
			ok = ok && math.Abs(w[j]-lam*v[j]) <= c.rt
		}
		if !ok {
			t.Fatalf("case %d lam=%v it=%d err=%v", i, lam, it, err)
		}
	}
}

func TestDeterministic(t *testing.T) {
	for i, m := range []struct{ a, v0 []float64 }{
		{[]float64{3, 1, 1, 3}, []float64{1, 0}},
		{[]float64{-2, 2, 2, -2}, []float64{0, 1}},
		{[]float64{3, 0, -3, 0, 0, 0, -3, 0, 3}, []float64{1, 0, 0}},
	} {
		l1, v1, _, e1 := Iterate(m.a, m.v0, len(m.v0), 1e-13, 100000)
		l2, v2, _, e2 := Iterate(m.a, append([]float64(nil), m.v0...), len(m.v0), 1e-13, 100000)
		if e1 != nil || e2 != nil || math.Float64bits(l1) != math.Float64bits(l2) || !veq(v1, v2, -1) {
			t.Fatalf("case %d not bit-deterministic", i)
		}
	}
}

func TestIterateErrors(t *testing.T) {
	id := []float64{1, 0, 0, 1}
	for i, c := range []struct {
		a, v []float64
		n    int
		tol  float64
		mi   int
		want error
	}{
		{make([]float64, 3), []float64{1, 0}, 2, 1e-9, 1, ErrDimensionMismatch},
		{id, []float64{1}, 2, 1e-9, 1, ErrDimensionMismatch},
		{id, nil, 0, 1e-9, 1, ErrEmptySystem},
		{id, []float64{0, 0}, 2, 1e-9, 1, ErrZeroStartVector},
		{id, []float64{1, 0}, 2, 0, 1, ErrNonPositiveTol},
		{id, []float64{1, 0}, 2, math.NaN(), 1, ErrNonPositiveTol},
		{id, []float64{1, 0}, 2, 1e-9, 0, ErrBadMaxIter},
		{[]float64{3, 1, 1, 3}, []float64{1, 0}, 2, 1e-30, 1, ErrNotConverged},
	} {
		if _, _, _, err := Iterate(c.a, c.v, c.n, c.tol, c.mi); !errors.Is(err, c.want) {
			t.Fatalf("case %d err=%v", i, err)
		}
	}
	if len(map[error]bool{ErrEmptySystem: true, ErrDimensionMismatch: true, ErrZeroStartVector: true, ErrNotConverged: true}) != 4 {
		t.Fatal("sentinels must be distinct")
	}
}

func TestRejectedLeavesNoTrace(t *testing.T) {
	g := []float64{1, 1, 1, 1}
	before := matVecOps.Load()
	Iterate(make([]float64, 3), []float64{1, 0}, 2, 1e-9, 1)
	Iterate(g, nil, 0, 1e-9, 1)
	Iterate(g, []float64{0, 0}, 2, 1e-9, 1)
	Iterate(g, []float64{1, 0}, 2, 0, 1)
	Iterate(g, []float64{1, 0}, 2, 1e-9, 0)
	if matVecOps.Load() != before { // 校验类拒绝：计数器不动
		t.Fatal("validation rejects changed counter")
	}
	if _, _, _, err := Iterate([]float64{3, 1, 1, 3}, []float64{1, 0}, 2, 1e-30, 2); !errors.Is(err, ErrNotConverged) {
		t.Fatal(err)
	}
	if matVecOps.Load() != before { // 非收敛：本轮计数精确回滚
		t.Fatal("non-converged left counter delta")
	}
	_, _, it, err := Iterate(g, []float64{1, 0}, 2, 1e-9, 100)
	if err != nil || matVecOps.Load()-before != int64(it)+1 { // 成功调用记 iters+1 次
		t.Fatalf("accounting it=%d err=%v", it, err)
	}
}

func TestScaleIndependentOfN(t *testing.T) {
	prev := -1
	for _, n := range []int{100, 1000, 10000} { // A=diag(3,1,0,..)，固定间隙 2
		a := make([]float64, n*n)
		a[0], a[n+1] = 3, 1
		v0 := make([]float64, n)
		for i := range v0 {
			v0[i] = 1
		}
		lam, v, it, err := Iterate(a, v0, n, 1e-9, 100000)
		if err != nil || math.Abs(lam-3) > 1e-9 || power.InfNorm(v) != 1 || it > 20 || (prev >= 0 && it != prev) {
			t.Fatalf("n=%d lam=%v it=%d prev=%d err=%v", n, lam, it, prev, err)
		}
		prev = it
	}
}
