package gwin

import "testing"

// TestReadsO1 钉住复杂度约束：period=m+1，先累积 m 个元素（不触发），
// 第 m+1 个恰好触发一次；断言为算快照重读的已累积元素个数恒为 0，
// 不随 m 线性增长。本测试在包内，直接读非导出字段 reads。
func TestReadsO1(t *testing.T) {
	for _, m := range []int64{100, 500, 1000, 5000, 10000} {
		mgr, err := NewManager(m+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		evs := make([]Event, m+1)
		wantSum := int64(0)
		for i := range evs {
			evs[i] = Event{Key: "k", Val: int64(i)}
			wantSum += int64(i)
		}
		if err := mgr.Apply(evs); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		w := mgr.keys["k"]
		if w == nil || len(w.snaps) != 1 {
			t.Fatalf("m=%d: want exactly 1 snapshot", m)
		}
		if w.reads != 0 {
			t.Fatalf("m=%d: snapshot re-read %d accumulated elements, want 0", m, w.reads)
		}
		if w.snaps[0].Sum != wantSum || w.snaps[0].Cnt != m+1 {
			t.Fatalf("m=%d: snapshot = %+v, want (%d,%d)", m, w.snaps[0], wantSum, m+1)
		}
		if !mgr.SnapshotReadO1("k") {
			t.Fatalf("m=%d: SnapshotReadO1 = false", m)
		}
	}
}

// TestApplyAtomic 钉住不变量 4 在 gwin 层的两段式提交：
// 混合批次中任一元素被拒，整批不生效。
func TestApplyAtomic(t *testing.T) {
	cases := []struct {
		name    string
		period  int64
		maxSnap int
		pre     []Event
		batch   []Event
		wantErr error
	}{
		{"empty key mid-batch", 3, 8,
			[]Event{{Key: "k", Val: 10}, {Key: "k", Val: 20}, {Key: "k", Val: 30}},
			[]Event{{Key: "k", Val: 40}, {Key: "", Val: 50}}, ErrEmptyKey},
		{"snap limit mid-batch", 3, 1,
			nil,
			[]Event{{Key: "k", Val: 1}, {Key: "k", Val: 2}, {Key: "k", Val: 3},
				{Key: "k", Val: 4}, {Key: "k", Val: 5}, {Key: "k", Val: 6}}, ErrTooManySnaps},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr, err := NewManager(tc.period, tc.maxSnap)
			if err != nil {
				t.Fatal(err)
			}
			if err := mgr.Apply(tc.pre); err != nil {
				t.Fatal(err)
			}
			s0, c0 := mgr.Totals("k")
			n0 := len(mgr.Snapshots("k"))
			if err := mgr.Apply(tc.batch); err != tc.wantErr {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			s1, c1 := mgr.Totals("k")
			if s1 != s0 || c1 != c0 || len(mgr.Snapshots("k")) != n0 {
				t.Fatalf("state changed: (%d,%d,%d) -> (%d,%d,%d)", s0, c0, n0, s1, c1, len(mgr.Snapshots("k")))
			}
		})
	}
}

// TestStepTrace 钉住 NOTES.md 第三节八行表（period=3，元素 10..80）。
func TestStepTrace(t *testing.T) {
	wantSum := []int64{10, 30, 60, 100, 150, 210, 280, 360}
	mgr, _ := NewManager(3, 8)
	for i, v := range []int64{10, 20, 30, 40, 50, 60, 70, 80} {
		if err := mgr.Apply([]Event{{Key: "k", Val: v}}); err != nil {
			t.Fatal(err)
		}
		if s, c := mgr.Totals("k"); s != wantSum[i] || c != int64(i+1) {
			t.Fatalf("step %d: (%d,%d), want (%d,%d)", i+1, s, c, wantSum[i], i+1)
		}
	}
	snaps := mgr.Snapshots("k")
	if len(snaps) != 2 || snaps[0] != (Snapshot{Sum: 60, Cnt: 3}) || snaps[1] != (Snapshot{Sum: 210, Cnt: 6}) {
		t.Fatalf("snapshots=%v, want (60,3) (210,6)", snaps)
	}
}

// TestNoReset 钉住不变量 2：每步（含触发后）Totals 都等于已接受元素全量累计。
func TestNoReset(t *testing.T) {
	mgr, _ := NewManager(3, 1000)
	var sum, cnt int64
	for i := 0; i < 300; i++ {
		v := int64((i*37)%201 - 100)
		if err := mgr.Apply([]Event{{Key: "k", Val: v}}); err != nil {
			t.Fatal(err)
		}
		sum, cnt = sum+v, cnt+1
		if s, c := mgr.Totals("k"); s != sum || c != cnt {
			t.Fatalf("totals=(%d,%d), want (%d,%d): trigger reset state", s, c, sum, cnt)
		}
	}
}

// TestMonotonic 钉住不变量 3：快照 cnt 严格递增且都是 period 的倍数。
func TestMonotonic(t *testing.T) {
	mgr, _ := NewManager(4, 1000)
	for i := 0; i < 999; i++ {
		if err := mgr.Apply([]Event{{Key: "k", Val: int64(i % 7)}}); err != nil {
			t.Fatal(err)
		}
	}
	prev := int64(0)
	for _, s := range mgr.Snapshots("k") {
		if s.Cnt <= prev || s.Cnt%4 != 0 {
			t.Fatalf("snapshot %+v breaks monotonicity", s)
		}
		prev = s.Cnt
	}
}
