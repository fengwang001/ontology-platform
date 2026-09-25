package agg

import "testing"

// TestWindowChecksO1 窗口内 Seq 数为 m 时做一次纠正，
// 断言成员判定检查条目数是不随 m 增长的小常数（映射定位而非线性扫描）。
func TestWindowChecksO1(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		k := NewKey(m)
		for i := 1; i <= m; i++ {
			k.Apply(int64(i), int64(i))
		}
		r := k.Apply(int64(m), 999) // 纠正窗口内最新 Seq
		if r.Kind != KindCorrection {
			t.Fatalf("m=%d kind=%v want Correction", m, r.Kind)
		}
		if k.checks > 2 {
			t.Fatalf("m=%d checks=%d，随窗口规模增长", m, k.checks)
		}
	}
}

// TestClassify 表驱动覆盖四类事件与窗口淘汰。
func TestClassify(t *testing.T) {
	k := NewKey(3)
	type step struct {
		seq, val int64
		kind     Kind
		sum      int64
	}
	steps := []step{
		{5, 10, KindNew, 10},
		{7, 20, KindNew, 30},
		{6, 15, KindLate, 45},
		{8, 5, KindNew, 50},
		{5, 99, KindStale, 50}, // 5 已被淘汰
		{6, 15, KindDuplicate, 50},
		{6, 30, KindCorrection, 65},
		{9, 3, KindNew, 68},
		{7, 77, KindStale, 68}, // 7 已被淘汰
		{8, 1, KindCorrection, 64},
	}
	for i, s := range steps {
		r := k.Apply(s.seq, s.val)
		if r.Kind != s.kind || r.NewSum != s.sum || k.Sum() != s.sum {
			t.Fatalf("step %d: kind=%v sum=%d want kind=%v sum=%d",
				i, r.Kind, r.NewSum, s.kind, s.sum)
		}
	}
	if len(k.window) != 3 || k.window[0] != 6 || k.window[1] != 8 || k.window[2] != 9 {
		t.Fatalf("window=%v want [6 8 9]", k.window)
	}
}
