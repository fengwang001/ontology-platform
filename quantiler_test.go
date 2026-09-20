package ontology

import (
	"errors"
	"math"
	"testing"
)

func mustAdd(t *testing.T, q *Quantiler, vs ...float64) {
	t.Helper()
	for _, v := range vs {
		if err := q.Add(v); err != nil {
			t.Fatalf("Add(%v): %v", v, err)
		}
	}
}

func mustQuantile(t *testing.T, q *Quantiler, p float64, m Method) float64 {
	t.Helper()
	v, err := q.Quantile(p, m)
	if err != nil {
		t.Fatalf("Quantile(%v, %v): %v", p, m, err)
	}
	return v
}

// 分位点落在样本之间时，两种口径必须给出不同结果。
func TestMethodsDifferBetweenSamples(t *testing.T) {
	q := New()
	mustAdd(t, q, 1, 2, 3, 4)
	nr := mustQuantile(t, q, 0.5, NearestRank)
	li := mustQuantile(t, q, 0.5, Linear)
	if nr == li {
		t.Fatalf("expected different results, both got %v", nr)
	}
	if nr != 2 {
		t.Fatalf("NearestRank(0.5) = %v, want 2 (真实样本值)", nr)
	}
	if li != 2.5 {
		t.Fatalf("Linear(0.5) = %v, want 2.5 (插值结果)", li)
	}
}

// 分位点恰好落在样本点上时，两种口径必须一致。
func TestMethodsAgreeOnSamplePoint(t *testing.T) {
	q := New()
	mustAdd(t, q, 10, 20, 30, 40, 50)
	for _, p := range []float64{0, 0.25, 0.5, 0.75, 1} {
		nr := mustQuantile(t, q, p, NearestRank)
		li := mustQuantile(t, q, p, Linear)
		if math.Float64bits(nr) != math.Float64bits(li) {
			t.Fatalf("p=%v: NearestRank=%v Linear=%v, want identical", p, nr, li)
		}
	}
}

// p=0 返回最小值、p=1 返回最大值，且都是样本中真实存在的值。
func TestBoundsReturnRealSamples(t *testing.T) {
	q := New()
	mustAdd(t, q, 3.5, -7.25, 42, 0.5)
	for _, m := range []Method{NearestRank, Linear} {
		if got := mustQuantile(t, q, 0, m); got != -7.25 {
			t.Fatalf("method %v: Quantile(0) = %v, want -7.25", m, got)
		}
		if got := mustQuantile(t, q, 1, m); got != 42 {
			t.Fatalf("method %v: Quantile(1) = %v, want 42", m, got)
		}
	}
}

// p 非法（越界或 NaN）返回 ErrInvalidP，且与空数据集错误可区分。
func TestInvalidP(t *testing.T) {
	q := New()
	mustAdd(t, q, 1, 2, 3)
	for _, p := range []float64{-0.1, -1, 1.0001, 2, math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, m := range []Method{NearestRank, Linear} {
			_, err := q.Quantile(p, m)
			if !errors.Is(err, ErrInvalidP) {
				t.Fatalf("Quantile(%v): err = %v, want ErrInvalidP", p, err)
			}
			if errors.Is(err, ErrEmpty) {
				t.Fatalf("Quantile(%v): ErrInvalidP 不应与 ErrEmpty 混淆", p)
			}
		}
	}
}

// 空数据集返回 ErrEmpty，且与参数非法错误可区分。
func TestEmptyDataSet(t *testing.T) {
	q := New()
	for _, m := range []Method{NearestRank, Linear} {
		_, err := q.Quantile(0.5, m)
		if !errors.Is(err, ErrEmpty) {
			t.Fatalf("empty: err = %v, want ErrEmpty", err)
		}
		if errors.Is(err, ErrInvalidP) {
			t.Fatal("ErrEmpty 不应与 ErrInvalidP 混淆")
		}
	}
}

// 单元素数据集上任何合法 p 都返回那个唯一元素。
func TestSingleElement(t *testing.T) {
	q := New()
	mustAdd(t, q, 9.75)
	for p := 0.0; p <= 1.0; p += 0.01 {
		for _, m := range []Method{NearestRank, Linear} {
			if got := mustQuantile(t, q, p, m); got != 9.75 {
				t.Fatalf("p=%v method=%v: got %v, want 9.75", p, m, got)
			}
		}
	}
}

// 未知口径返回可判定错误。
func TestUnknownMethod(t *testing.T) {
	q := New()
	mustAdd(t, q, 1)
	if _, err := q.Quantile(0.5, Method(99)); !errors.Is(err, ErrUnknownMethod) {
		t.Fatalf("err = %v, want ErrUnknownMethod", err)
	}
}
