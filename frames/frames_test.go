package frames

import "testing"

func TestRingWindowAndSums(t *testing.T) {
	r := New(4)
	if got := r.Cur(); got != 0 || r.Low() != 1 {
		t.Fatalf("空环 cur/low = %d/%d, 期望 0/1", got, r.Low())
	}
	sizes := []int{3, 5, 7, 11, 13} // 追加后保留 5,7,11,13（帧 2..5）
	for _, sz := range sizes {
		r.Append(sz)
	}
	tests := []struct {
		name        string
		from, to    int
		want        int64
		wantTouched int
	}{
		{"空区间", 4, 3, 0, 0},
		{"单帧两端皆活", 3, 3, 7, 2},
		{"左端已挤出读base", 2, 5, 5 + 7 + 11 + 13, 1},
	}
	for _, tc := range tests {
		before := r.touchedCount()
		got := r.RangeSum(tc.from, tc.to)
		gotTouched := r.touchedCount() - before
		if got != tc.want || gotTouched != tc.wantTouched {
			t.Errorf("%s: RangeSum=%d(期望 %d), touched=%d(期望 %d)",
				tc.name, got, tc.want, gotTouched, tc.wantTouched)
		}
	}
	if r.Cur() != 5 || r.Low() != 2 || r.Has(1) || !r.Has(2) || !r.Has(5) || r.Has(6) {
		t.Fatalf("窗口错误 cur=%d low=%d has1=%v has2=%v has5=%v has6=%v",
			r.Cur(), r.Low(), r.Has(1), r.Has(2), r.Has(5), r.Has(6))
	}
}

// TestTouchedIndependentOfGap 证明一次区间和只触及 2 条帧记录，与 K 及缺口长度无关。
func TestTouchedIndependentOfGap(t *testing.T) {
	for _, k := range []int{64, 65536} {
		r := New(k)
		for i := 0; i < k; i++ {
			r.Append(i%7 + 1)
		}
		before := r.touchedCount()
		_ = r.RangeSum(1, k) // 左端帧1已挤出读 base（0次），右端活帧（1次）
		if got := r.touchedCount() - before; got != 1 {
			t.Fatalf("K=%d 全区间求和触及 %d 条记录, 期望 1", k, got)
		}
		// Reconnect 的真实形态：两个端点均为活帧，恰触及 2 条。
		before = r.touchedCount()
		_ = r.RangeSum(2, k)
		if got := r.touchedCount() - before; got != 2 {
			t.Fatalf("K=%d 活帧区间求和触及 %d 条记录, 期望 2", k, got)
		}
	}
}
