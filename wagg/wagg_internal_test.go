package wagg

import "testing"

// TestComplexityProbe 白盒核验 lastExamined：m∈{100,1000,10000} 个未清除窗口下，
// 喂一个只让水位线前进 1、不触发任何窗口的事件，检查数必须恒为 0（不随 m 线性增长）；
// 另做对照：真触发时探针等于触发窗口数，证明探针确实计数而非恒 0。
func TestComplexityProbe(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a, err := NewAgg(10, 1<<50, 0, m+1)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ { // 巨大 delay：窗口全部未触发、未清除
			a.ingest(Event{Key: "k", TS: int64(i) * 10})
		}
		if a.nopen != m {
			t.Fatalf("m=%d setup nopen=%d", m, a.nopen)
		}
		a.ingest(Event{Key: "k", TS: int64(m-1)*10 + 1}) // 落入最大窗口，wm 仅 +1，不触发
		if a.lastExamined != 0 {
			t.Errorf("m=%d examined=%d, want 0 (must not scan all open windows)", m, a.lastExamined)
		}
	}
	// 对照：水位线真越过一个窗口的 end 时，探针恰好计到本次触发的那 1 个窗口。
	b, _ := NewAgg(10, 0, 100, 10)
	b.ingest(Event{Key: "k", TS: 5})  // wm=5，[0,10) 未触发
	b.ingest(Event{Key: "k", TS: 15}) // wm=15，触发 [0,10)（清除点 110 未到）
	if b.lastExamined != 1 {
		t.Errorf("fire examined=%d, want 1", b.lastExamined)
	}
}
