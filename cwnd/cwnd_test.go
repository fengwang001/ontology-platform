package cwnd

import "testing"

// TestAckCostConstant：CongAvoid 下 cwnd=m，连续 ACK 到恰好 +1，
// 每次 OnAck 检查的 ACK 记录数（非导出 lastChecked）必须 <= 1，
// 且恰好在第 m 个 ACK 时 +1 —— 加性增 O(1)/ACK，不随 cwnd 线性扫描。
func TestAckCostConstant(t *testing.T) {
	ms := []int64{100, 333, 1000, 5000, 10000}
	for _, m := range ms {
		m := m
		t.Run("", func(t *testing.T) {
			w := New(m, 1<<40)
			w.cwnd, w.state, w.partial = m, CongAvoid, 0
			var n int64
			for w.cwnd == m {
				w.ApplyAck()
				n++
				if w.lastChecked > 1 {
					t.Fatalf("m=%d 第%d个ACK检查记录数=%d >1", m, n, w.lastChecked)
				}
				if n > m {
					t.Fatalf("m=%d 超过 %d 个ACK仍未+1", m, m)
				}
			}
			if n != m || w.cwnd != m+1 || w.partial != 0 {
				t.Fatalf("m=%d: 用了%d个ACK, cwnd=%d partial=%d, 想 (%d,%d,0)", m, n, w.cwnd, w.partial, m, m+1)
			}
		})
	}
}

// TestGrowthTable：表驱动核验慢启动/左闭切换/加性增/丢包减半原语。
func TestGrowthTable(t *testing.T) {
	cases := []struct {
		name          string
		ssthresh      int64
		acks          int
		wantCw, wantP int64
		wantSt        State
		lossSS        int64
	}{
		{"ss2-one-ack", 2, 1, 2, 0, SlowStart, 2},
		{"ss4-switch-at-4", 4, 4, 4, 1, CongAvoid, 2},
		{"ss4-additive-to-5", 4, 7, 5, 0, CongAvoid, 2},
		{"ss8-still-slowstart", 8, 7, 8, 0, SlowStart, 4},
		{"ss8-switch-at-8", 8, 8, 8, 1, CongAvoid, 4},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			w := New(c.ssthresh, 1<<40)
			for i := 0; i < c.acks; i++ {
				w.ApplyAck()
			}
			if w.cwnd != c.wantCw || w.partial != c.wantP || w.state != c.wantSt {
				t.Fatalf("acks后 got (%d,%s,%d) 想 (%d,%s,%d)",
					w.cwnd, w.state, w.partial, c.wantCw, c.wantSt, c.wantP)
			}
			w.ApplyLoss()
			if w.cwnd != 1 || w.ssthresh != c.lossSS || w.state != SlowStart || w.partial != 0 {
				t.Fatalf("loss后 got (%d,%d,%s,%d)", w.cwnd, w.ssthresh, w.state, w.partial)
			}
		})
	}
}

// TestEightStep：第三节八步序列（ssthresh=4），钉住 AIMD 与左闭切换。
func TestEightStep(t *testing.T) {
	w := New(4, 1<<40)
	type row struct {
		cw, ss, p int64
		st        State
	}
	want := []row{
		{2, 4, 0, SlowStart}, {3, 4, 0, SlowStart}, {4, 4, 0, SlowStart},
		{4, 4, 1, CongAvoid}, {4, 4, 2, CongAvoid}, {4, 4, 3, CongAvoid},
		{5, 4, 0, CongAvoid},
	}
	for i, r := range want {
		w.ApplyAck()
		if got := (row{w.cwnd, w.ssthresh, w.partial, w.state}); got != r {
			t.Fatalf("步%d: got %+v want %+v", i+1, got, r)
		}
	}
	w.ApplyLoss()
	if got := (row{w.cwnd, w.ssthresh, w.partial, w.state}); got != (row{1, 2, 0, SlowStart}) {
		t.Fatalf("步8: got %+v", got)
	}
}

// TestLossFloorTable：丢包减半向下取整且下限 2。
func TestLossFloorTable(t *testing.T) {
	cases := []struct{ cw, wantSS int64 }{
		{5, 2}, {4, 2}, {3, 2}, {2, 2}, {6, 3}, {7, 3}, {100, 50},
	}
	for _, c := range cases {
		w := New(1<<30, 1<<40)
		w.cwnd = c.cw
		w.ApplyLoss()
		if w.ssthresh != c.wantSS || w.cwnd != 1 || w.state != SlowStart {
			t.Fatalf("cwnd=%d 丢包 ssthresh=%d 想 %d", c.cw, w.ssthresh, c.wantSS)
		}
	}
}
