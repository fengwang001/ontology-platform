package mc

import (
	"testing"

	"ontology/fn"
)

// TestRetainedStaysZero 钉住 O(1) 内存：无论 Add 多少点，
// 为求平均而保留的历史采样点个数恒为 0（只留 sum 与 n 两个标量）。
func TestRetainedStaysZero(t *testing.T) {
	g, err := fn.New(func(x float64) float64 { return x * x })
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		ac := New(g, 0, 2)
		for i := 0; i < m; i++ {
			if err := ac.Add(float64(i%201) / 100); err != nil {
				t.Fatalf("m=%d i=%d: %v", m, i, err)
			}
		}
		if ac.retained != 0 {
			t.Fatalf("m=%d: retained=%d, want 0 (O(1) memory)", m, ac.retained)
		}
		if ac.n != int64(m) {
			t.Fatalf("m=%d: n=%d", m, ac.n)
		}
	}
}

// TestAddRejectsOutOfRange 越界拒绝且状态不变（包内视角核验 sum/n）。
func TestAddRejectsOutOfRange(t *testing.T) {
	g, _ := fn.New(func(x float64) float64 { return x })
	ac := New(g, 0, 2)
	if err := ac.Add(1.5); err != nil {
		t.Fatal(err)
	}
	sum, n := ac.sum, ac.n
	for _, x := range []float64{-0.001, -1, 2.001, 100} {
		if err := ac.Add(x); err != ErrOutOfRange {
			t.Fatalf("x=%v: err=%v, want ErrOutOfRange", x, err)
		}
	}
	if ac.sum != sum || ac.n != n {
		t.Fatalf("rejected Add changed state: sum %v->%v n %v->%v", sum, ac.sum, n, ac.n)
	}
	if _, err := ac.Estimate(); err != nil {
		t.Fatalf("accumulator unusable after rejections: %v", err)
	}
}
