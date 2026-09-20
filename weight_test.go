package ontology

import (
	"errors"
	"math"
	"testing"
)

// 权重语义：w=3 等价于该值出现 3 次，且结果与展开后逐位相同。
func TestWeightEqualsExpansionBitExact(t *testing.T) {
	weighted := []struct {
		v float64
		w float64
	}{
		{1.5, 2}, {2.5, 1}, {3.5, 3}, {5.5, 1}, {8.5, 2}, {-4.5, 4},
	}
	qw, qe := New(), New()
	for _, x := range weighted {
		if err := qw.AddWeighted(x.v, x.w); err != nil {
			t.Fatalf("AddWeighted(%v, %v): %v", x.v, x.w, err)
		}
		for i := 0; i < int(x.w); i++ {
			mustAdd(t, qe, x.v)
		}
	}
	if qw.TotalWeight() != qe.TotalWeight() {
		t.Fatalf("total weight %d != expanded count %d",
			qw.TotalWeight(), qe.TotalWeight())
	}
	ps := []float64{0, 1, 0.5, 0.25, 0.75, 1.0 / 3.0, 0.999999}
	for p := 0.0; p <= 1.0; p += 0.01 {
		ps = append(ps, p)
	}
	for _, p := range ps {
		for _, m := range []Method{NearestRank, Linear} {
			a := mustQuantile(t, qw, p, m)
			b := mustQuantile(t, qe, p, m)
			if math.Float64bits(a) != math.Float64bits(b) {
				t.Fatalf("p=%v method=%v: weighted=%v expanded=%v", p, m, a, b)
			}
		}
	}
}

// 权重为 0、负数、非整数或 NaN 时在加入时就返回可判定错误。
func TestInvalidWeight(t *testing.T) {
	for _, w := range []float64{0, -1, -0.5, 2.5, 0.1, math.NaN(), math.Inf(1)} {
		q := New()
		err := q.AddWeighted(1.0, w)
		if !errors.Is(err, ErrInvalidWeight) {
			t.Fatalf("AddWeighted(1, %v): err = %v, want ErrInvalidWeight", w, err)
		}
		if q.TotalWeight() != 0 || q.UniqueCount() != 0 {
			t.Fatalf("AddWeighted(1, %v) 不应改变状态", w)
		}
	}
}

// 同一唯一值的权重应累加。
func TestWeightsAccumulate(t *testing.T) {
	q := New()
	mustAdd(t, q, 7, 7, 7)
	if err := q.AddWeighted(7, 3); err != nil {
		t.Fatalf("AddWeighted: %v", err)
	}
	if q.UniqueCount() != 1 || q.TotalWeight() != 6 {
		t.Fatalf("UniqueCount=%d TotalWeight=%d, want 1 和 6",
			q.UniqueCount(), q.TotalWeight())
	}
	if got := mustQuantile(t, q, 0.5, Linear); got != 7 {
		t.Fatalf("Linear(0.5) = %v, want 7", got)
	}
}
