package ontology

import (
	"errors"
	"math"
	"testing"
)

// +0.0 与 -0.0 合并为同一个值，返回时统一为 +0.0。
func TestSignedZeroMerged(t *testing.T) {
	q := New()
	mustAdd(t, q, math.Copysign(0, -1), 0.0, math.Copysign(0, -1), 1.0)
	if got := q.UniqueCount(); got != 2 {
		t.Fatalf("UniqueCount = %d, want 2 (±0.0 合并)", got)
	}
	if got := q.TotalWeight(); got != 4 {
		t.Fatalf("TotalWeight = %d, want 4", got)
	}
	for _, m := range []Method{NearestRank, Linear} {
		got := mustQuantile(t, q, 0, m)
		if math.Float64bits(got) != math.Float64bits(0.0) {
			t.Fatalf("method %v: Quantile(0) bits = %x, want +0.0", m,
				math.Float64bits(got))
		}
	}
}

// NaN 样本被拒绝加入并计入跳过计数，不污染结果。
func TestNaNRejectedAndCounted(t *testing.T) {
	q := New()
	mustAdd(t, q, 1, 2, 3)
	for i := 0; i < 3; i++ {
		if err := q.Add(math.NaN()); !errors.Is(err, ErrNaNSample) {
			t.Fatalf("Add(NaN): err = %v, want ErrNaNSample", err)
		}
	}
	if got := q.SkippedNaN(); got != 3 {
		t.Fatalf("SkippedNaN = %d, want 3", got)
	}
	if got := q.UniqueCount(); got != 3 {
		t.Fatalf("UniqueCount = %d, want 3 (NaN 未进入数据集)", got)
	}
	if got := mustQuantile(t, q, 0.5, Linear); got != 2 {
		t.Fatalf("Linear(0.5) = %v, want 2 (结果未被 NaN 污染)", got)
	}
}

// 正负无穷是合法样本，参与排序；落在无穷上时直接返回无穷。
func TestInfParticipatesInOrdering(t *testing.T) {
	q := New()
	mustAdd(t, q, math.Inf(-1), 0, math.Inf(1))
	if got := mustQuantile(t, q, 0, NearestRank); !math.IsInf(got, -1) {
		t.Fatalf("Quantile(0) = %v, want -Inf", got)
	}
	if got := mustQuantile(t, q, 1, Linear); !math.IsInf(got, 1) {
		t.Fatalf("Quantile(1) = %v, want +Inf", got)
	}
	if got := mustQuantile(t, q, 0.5, Linear); got != 0 {
		t.Fatalf("Linear(0.5) = %v, want 0", got)
	}
}

// 分位点落在无穷与有限值之间时，线性插值返回无穷而不是 NaN。
func TestInfInterpolationReturnsInfNotNaN(t *testing.T) {
	q := New()
	mustAdd(t, q, 1, math.Inf(1))
	got := mustQuantile(t, q, 0.5, Linear)
	if !math.IsInf(got, 1) {
		t.Fatalf("Linear(0.5) on {1, +Inf} = %v, want +Inf", got)
	}
	q2 := New()
	mustAdd(t, q2, math.Inf(-1), 1)
	got = mustQuantile(t, q2, 0.5, Linear)
	if !math.IsInf(got, -1) {
		t.Fatalf("Linear(0.5) on {-Inf, 1} = %v, want -Inf", got)
	}
	// 两端同为无穷时返回该无穷。
	q3 := New()
	mustAdd(t, q3, math.Inf(1), math.Inf(1), 0)
	if got := mustQuantile(t, q3, 0.9, Linear); !math.IsInf(got, 1) {
		t.Fatalf("Linear(0.9) = %v, want +Inf", got)
	}
}
