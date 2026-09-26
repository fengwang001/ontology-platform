package wrr

import "testing"

func mustNew(t *testing.T, w []int) *WRR {
	t.Helper()
	r, err := New(w)
	if err != nil {
		t.Fatalf("New%v: %v", w, err)
	}
	return r
}

// 钉住 NOTES.md 第三节的六行分步表：权重 [3,1,2]，W=6。
func TestSmoothSequence312(t *testing.T) {
	r := mustNew(t, []int{3, 1, 2})
	wantSel := []int{0, 2, 0, 1, 2, 0}
	wantCW := [][3]int{{-3, 1, 2}, {0, 2, -2}, {-3, 3, 0}, {0, -2, 2}, {3, -1, -2}, {0, 0, 0}}
	for k := 0; k < 6; k++ {
		if s := r.Next(); s != wantSel[k] {
			t.Fatalf("第 %d 步选中 %d, 期望 %d", k+1, s, wantSel[k])
		}
		if got := [3]int(r.cw); got != wantCW[k] {
			t.Fatalf("第 %d 步后 cw=%v, 期望 %v", k+1, got, wantCW[k])
		}
	}
}

// 复杂度：定位最大者的检查个数不随服务器数 m 增长（白盒读非导出计数器）。
func TestLocateChecksBounded(t *testing.T) {
	const bound = 4 // 与 m 无关的小常数
	for _, m := range []int{100, 1000, 5000, 10000} {
		w := make([]int, m)
		for i := range w {
			w[i] = i + 1 // 权重互异
		}
		r := mustNew(t, w)
		r.Next()
		if r.checked > bound {
			t.Fatalf("m=%d: 定位最大者检查了 %d 台, 超过常数上界 %d", m, r.checked, bound)
		}
	}
}
