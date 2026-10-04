package lot

import "testing"

// TestTouchedBound 验证非导出计数器 touched 的复杂度：
// 一次 CloseToday 触碰批次数 <= 被完全平掉批次数 + 1，且与批次总数无关。
func TestTouchedBound(t *testing.T) {
	cases := []struct {
		name      string
		batches   int
		qtyPer    int64
		closeQty  int64 // 平掉前 n 批整数倍 + 1 手，制造 1 个部分批次
		wantFull  int
		wantTouch int
	}{
		{"10批:平3批零1手", 10, 5, 16, 3, 4},
		{"10000批:平3批零1手", 10000, 5, 16, 3, 4},
		{"10批:恰好平完8批", 10, 5, 40, 8, 8},
		{"10000批:恰好平完8批", 10000, 5, 40, 8, 8},
		{"10批:仅队首批1手", 10, 5, 1, 0, 1},
		{"10000批:仅队首批1手", 10000, 5, 1, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New()
			for i := 0; i < tc.batches; i++ {
				p.Open(int64(1000+i), tc.qtyPer)
			}
			p.CloseToday(tc.closeQty)
			got := p.touched
			t.Logf("输入 batches=%d qtyPer=%d closeQty=%d => 完全平掉批次=%d touched=%d；判定: touched<=完全平掉+1 且与总批数无关",
				tc.batches, tc.qtyPer, tc.closeQty, tc.wantFull, got)
			if got != tc.wantTouch {
				t.Fatalf("touched=%d, want %d", got, tc.wantTouch)
			}
			if got > tc.wantFull+1 {
				t.Fatalf("touched=%d 越界 full=%d", got, tc.wantFull)
			}
		})
	}

	// 10 批与 10000 批同操作同 touched 对照。
	for _, n := range []int{10, 10000} {
		p := New()
		for i := 0; i < n; i++ {
			p.Open(100, 7)
		}
		p.CloseToday(23) // 3 批完整 + 2 手
		if p.touched != 4 {
			t.Fatalf("n=%d touched=%d want 4", n, p.touched)
		}
		if p.TodayQty() != int64(n)*7-23 {
			t.Fatalf("n=%d 剩余今仓手数错误", n)
		}
	}
}

// TestMergeAndMTM 白盒核对结算后今仓并入昨仓、sp0 改价。
func TestMergeAndMTM(t *testing.T) {
	p := New()
	p.Open(100, 2)
	p.Open(110, 3)
	if got := p.MTM(Long, 10, 120); got != (20*2+10*3)*10 {
		t.Fatalf("MTM long=%d", got)
	}
	p.Merge(120)
	if p.qy != 5 || p.sp0 != 120 || p.TodayQty() != 0 {
		t.Fatalf("merge 后状态错误 qy=%d sp0=%d today=%d", p.qy, p.sp0, p.TodayQty())
	}
	if got := p.MTM(Short, 10, 115); got != 5*5*10 {
		t.Fatalf("MTM short after merge=%d want 250", got)
	}
}
