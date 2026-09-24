package lww

import "testing"

// TestReplayCheckCountConstant 证明 winner 在 Apply 时增量维护、Replay 只读缓存：
// 对同一键喂 m 条变更后 Replay，为确定 winner 检查的历史变更条数不随 m 增长。
// 本测试与 Engine 同包，直接读非导出字段 checked；该值不经由任何导出接口暴露。
func TestReplayCheckCountConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		e := New(m)
		for i := 0; i < m; i++ {
			e.Apply("k", int64(i%7), int64(i))
		}
		e.Replay()
		if got := e.checked.Load(); got > 1 {
			t.Fatalf("m=%d: Replay 检查了 %d 条历史变更，超过常数上界", m, got)
		}
	}
}

// TestApplyTieBreak 表驱动钉住 winner 规则：Ver 大者胜，Ver 并列 sn 大者胜。
func TestApplyTieBreak(t *testing.T) {
	cases := []struct {
		name               string
		vers               []int64
		wantVer, wantSnIdx int64
	}{
		{"严格递增后者胜", []int64{1, 2, 3}, 3, 2},
		{"严格递减首者胜", []int64{3, 2, 1}, 3, 0},
		{"并列后至者胜", []int64{5, 5, 5}, 5, 2},
		{"中间高者胜", []int64{1, 9, 2}, 9, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := New(len(tc.vers))
			for i, v := range tc.vers {
				e.Apply("k", v, int64(i))
			}
			w, ok := e.Winner("k")
			if !ok || w.Ver != tc.wantVer || w.Val != tc.wantSnIdx {
				t.Fatalf("winner = %+v, 期望 Ver=%d Val=%d", w, tc.wantVer, tc.wantSnIdx)
			}
		})
	}
}
