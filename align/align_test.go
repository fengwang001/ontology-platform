package align

import (
	"slices"
	"testing"
)

// 滞留 m 条后只应发出最小 TS 那一条，且定位最小值的比较数不随 m 线性增长。
func TestLocateMinComparesBounded(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		a := New()
		for i := 0; i < m; i++ {
			if _, ok := a.Feed(Event{Stream: 'A', TS: int64(10 + i)}); !ok {
				t.Fatalf("m=%d i=%d: dropped", m, i)
			}
		}
		out, ok := a.Feed(Event{Stream: 'B', TS: 5})
		if !ok || len(out) != 1 || out[0].TS != 5 {
			t.Fatalf("m=%d: out=%v ok=%v", m, out, ok)
		}
		if a.lastCmps > 64 {
			t.Fatalf("m=%d: cmps=%d, 随 m 线性增长", m, a.lastCmps)
		}
	}
}

// 八步序列的逐步发出与迟到判定（表驱动）。
func TestEightSteps(t *testing.T) {
	steps := []Event{
		{'A', 1}, {'A', 2}, {'B', 1}, {'A', 3}, {'B', 2}, {'B', 5}, {'A', 4}, {'A', 2},
	}
	wantTS := [][]int64{{}, {}, {1, 1}, {}, {2, 2}, {3}, {4}, {}}
	wantOK := []bool{true, true, true, true, true, true, true, false}
	a := New()
	for i, e := range steps {
		out, ok := a.Feed(e)
		if ok != wantOK[i] {
			t.Fatalf("步 %d: ok=%v want %v", i+1, ok, wantOK[i])
		}
		var ts []int64
		for _, o := range out {
			ts = append(ts, o.TS)
		}
		if !slices.Equal(ts, wantTS[i]) {
			t.Fatalf("步 %d: 发出 %v want %v", i+1, ts, wantTS[i])
		}
	}
	if tail := a.Close(); len(tail) != 1 || tail[0].TS != 5 {
		t.Fatalf("Close: %v", tail)
	}
}
