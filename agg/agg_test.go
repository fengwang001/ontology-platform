package agg

import "testing"

func TestAggregators(t *testing.T) {
	// 每行：族；初始成员；删除值；删除是否应要求重算；删除+（必要时）重算后的期望结果。
	cases := []struct {
		k        Kind
		members  []float64
		remove   float64
		needRec  bool
		expected float64
	}{
		{Count, []float64{1, 2, 3}, 2, false, 2},
		{Sum, []float64{1, 2, 3}, 2, false, 4},
		{Min, []float64{5, 1, 9, 1}, 1, false, 1}, // 删重复最小值，仍剩一个 1
		{Min, []float64{5, 1, 9}, 1, true, 5},     // 删唯一最小值 → 重算
		{Min, []float64{5, 1, 9}, 9, false, 1},    // 删非极值 → 不动
		{Max, []float64{5, 9, 1, 9}, 9, false, 9},
		{Max, []float64{5, 9, 1}, 9, true, 5},
		{Max, []float64{5, 9, 1}, 1, false, 9},
		{DistinctCount, []float64{2, 2, 3}, 2, true, 2},
	}
	for _, tc := range cases {
		a := New(tc.k)
		for _, v := range tc.members {
			a.Add(v)
		}
		need := a.Remove(tc.remove)
		if need != tc.needRec {
			t.Fatalf("kind=%d recompute flag got %v want %v", tc.k, need, tc.needRec)
		}
		if a.NeedsMembersOnDelete() != (tc.k != Count && tc.k != Sum) {
			t.Fatalf("kind=%d NeedsMembersOnDelete mismatch", tc.k)
		}
		if need {
			// need=true 行待删值只出现一次，重建为去掉一个后的成员。
			rest := withoutOne(tc.members, tc.remove)
			a.Recompute(rest)
		}
		if a.Value() != tc.expected {
			t.Fatalf("kind=%d value got %v want %v", tc.k, a.Value(), tc.expected)
		}
	}
}

func withoutOne(m []float64, v float64) []float64 {
	out := make([]float64, 0, len(m))
	done := false
	for _, x := range m {
		if !done && normZero(x) == normZero(v) {
			done = true
			continue
		}
		out = append(out, x)
	}
	return out
}
