package agg

import "testing"

func TestAggregators(t *testing.T) {
	cases := []struct {
		name      string
		k         Kind
		adds      []float64
		removes   []float64 // 每个元素执行一次 Remove
		wantVal   float64
		wantExist bool
		needMem   bool
	}{
		{"count", KCount, []float64{1, 2, 3}, []float64{1}, 2, true, false},
		{"sum", KSum, []float64{1.5, -2.5, 1}, nil, 0, true, false},
		{"sum-empty", KSum, []float64{5}, []float64{5}, 0, false, false},
		{"min-keep", KMin, []float64{3, 1, 2}, []float64{3}, 1, true, true},
		{"min-hit-stale", KMin, []float64{1, 2, 3}, []float64{1}, 1, true, true},
		{"max-hit-stale", KMax, []float64{1, 9, 3}, []float64{9}, 9, true, true},
		{"distinct", KDistinctCount, []float64{1, 1, 2, 3}, nil, 3, true, true},
		{"distinct-empty-stale", KDistinctCount, []float64{1}, []float64{1}, 1, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.k.New()
			if a.NeedMembersOnDelete() != tc.needMem {
				t.Fatalf("NeedMembersOnDelete = %v, want %v",
					a.NeedMembersOnDelete(), tc.needMem)
			}
			for _, v := range tc.adds {
				a.Add(v)
			}
			for _, v := range tc.removes {
				a.Remove(v)
			}
			got, exist := a.Value()
			if exist != tc.wantExist || (exist && got != tc.wantVal) {
				t.Fatalf("Value = (%v,%v), want (%v,%v)",
					got, exist, tc.wantVal, tc.wantExist)
			}
		})
	}
}

func TestRemoveSignals(t *testing.T) {
	// 撤回信号表：删到不可反推状态时 Remove 必须返回 false。
	cases := []struct {
		k       Kind
		setup   []float64
		remove  float64
		wantOK  bool
	}{
		{KCount, []float64{1, 2}, 2, true},
		{KSum, []float64{1, 2}, 2, true},
		{KMin, []float64{1, 2, 3}, 2, true},
		{KMin, []float64{1, 2, 3}, 1, false},
		{KMax, []float64{1, 2, 3}, 2, true},
		{KMax, []float64{1, 2, 3}, 3, false},
		{KDistinctCount, []float64{1, 1, 2}, 1, true},
		{KDistinctCount, []float64{1, 2}, 1, false},
	}
	for _, tc := range cases {
		a := tc.k.New()
		for _, v := range tc.setup {
			a.Add(v)
		}
		if ok := a.Remove(tc.remove); ok != tc.wantOK {
			t.Fatalf("%s remove %v: ok=%v want %v",
				tc.k, tc.remove, ok, tc.wantOK)
		}
	}
}
