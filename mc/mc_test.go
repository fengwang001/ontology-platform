package mc

import (
	"testing"

	"ontology/fn"
)

func mustAcc(t *testing.T, a, b float64) *Accumulator {
	t.Helper()
	wf, err := fn.New(func(x float64) float64 { return x * x })
	if err != nil {
		t.Fatal(err)
	}
	acc, err := New(wf, a, b)
	if err != nil {
		t.Fatal(err)
	}
	return acc
}

// 第四节：Add m 个采样点（多档规模），为求平均保留的历史采样点个数恒为 0，
// 证明内存 O(1)——只保留 sum 与 n 两个标量，而不是存下全部 m 个点再重算。
// 白盒读取非导出字段 retained，不经由任何导出函数。
func TestRetainedSamplesAlwaysZero(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		acc := mustAcc(t, 0, 2)
		for i := 0; i < m; i++ {
			if err := acc.Add(1.0); err != nil {
				t.Fatal(err)
			}
		}
		if acc.retained != 0 {
			t.Fatalf("m=%d: retained=%d, want 0", m, acc.retained)
		}
		if acc.Samples() != int64(m) {
			t.Fatalf("m=%d: samples=%d, want %d", m, acc.Samples(), m)
		}
	}
}

func TestZeroWidthInterval(t *testing.T) {
	for _, a := range []float64{0, 1.5, -2} {
		acc := mustAcc(t, a, a)
		for i := 0; i < 3; i++ {
			if err := acc.Add(a); err != nil {
				t.Fatal(err)
			}
		}
		if got, err := acc.Estimate(); err != nil || got != 0 {
			t.Fatalf("a=%v: got %v,%v want 0,nil", a, got, err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	acc := mustAcc(t, 0, 2)
	if err := acc.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	// 接收者带状态时 SelfCheck 仍独立通过，且不读不改接收者。
	if err := acc.Add(1.0); err != nil {
		t.Fatal(err)
	}
	if err := acc.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	if acc.Samples() != 1 {
		t.Fatalf("SelfCheck touched receiver: samples=%d", acc.Samples())
	}
}
