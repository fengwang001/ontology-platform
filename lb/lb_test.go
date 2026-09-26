package lb

import "testing"

// TestDrainBoundary 排水边界：出发时间 ≤ t 移除，> t 保留。
func TestDrainBoundary(t *testing.T) {
	cases := []struct {
		drainAt      int64
		wantInSystem int
	}{
		{4, 3}, {5, 2}, {9, 2}, {10, 1}, {14, 1}, {15, 0}, {100, 0},
	}
	for _, c := range cases {
		b := New(3, 5)
		for i := 0; i < 3; i++ { // 出发时间 5、10、15
			if _, full := b.Admit(0); full {
				t.Fatalf("unexpected full")
			}
		}
		b.Drain(c.drainAt)
		if got := b.InSystem(); got != c.wantInSystem {
			t.Errorf("Drain(%d): InSystem=%d want %d", c.drainAt, got, c.wantInSystem)
		}
	}
}

// TestAdmitFull 满则拒且不改状态：拒绝后 last 与计数保持，后续接纳仍接在原队尾。
func TestAdmitFull(t *testing.T) {
	b := New(1, 5)
	if dep, full := b.Admit(0); full || dep != 5 {
		t.Fatalf("first admit: dep=%d full=%v", dep, full)
	}
	if dep, full := b.Admit(1); !full || dep != 0 {
		t.Fatalf("should be full: dep=%d full=%v", dep, full)
	}
	if got := b.InSystem(); got != 1 {
		t.Fatalf("rejection changed InSystem: %d", got)
	}
	b.Drain(5) // 漏出 dep=5
	if dep, full := b.Admit(5); full || dep != 10 {
		t.Fatalf("after drain: dep=%d full=%v, want 10,false", dep, full)
	}
}

// TestDrainChecksConstant 队头指针证明：m 个未出发项下 Drain 只探测队头一项。
func TestDrainChecksConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		b := New(int64(m)+1, 1<<40)
		for i := 0; i < m; i++ {
			if _, full := b.Admit(0); full {
				t.Fatalf("m=%d: unexpected full", m)
			}
		}
		b.Drain(0) // 所有项出发时间 1<<40 起，均未出发
		if b.drainChecks > 2 {
			t.Fatalf("m=%d: drainChecks=%d, 随 m 线性增长", m, b.drainChecks)
		}
	}
}
