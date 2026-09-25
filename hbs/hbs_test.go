package hbs

import "testing"

// TestLateCheckDoesNotRescanHistory 证明迟到判定是 O(1)：
// 喂入 m 个历史事件后做一次迟到判定，判定期间检查过的历史事件数
// 必须被与 m 无关的常数 bound 界住，而不是随 m 线性增长。
func TestLateCheckDoesNotRescanHistory(t *testing.T) {
	const bound = int64(1) // 与 m 无关的小常数：允许 0 或 1 次标量比较，禁止回扫历史
	ms := []int{100, 1000, 10000}
	for _, m := range ms {
		s := New(3)
		for i := 0; i < m; i++ {
			s.wm.Advance(int64(i + 1))
		}
		before := s.lateChecks
		s.judgeLate(1, s.wm.Value()) // 1 必然迟到（wm 已远大于 1）
		inspected := s.lateChecks - before
		if inspected > bound {
			t.Fatalf("m=%d: late judgment inspected %d history events, want <= %d (rescan?)", m, inspected, bound)
		}
	}
}

func TestFeedAndHeartbeat(t *testing.T) {
	cases := []struct {
		name     string
		kind     string // "d" 数据 / "h" 心跳
		ts       int64
		wantWM   int64
		wantLate bool
		wantCnt  int64
	}{
		{"data-a-5", "d", 5, 2, false, 0},
		{"hb-12", "h", 12, 12, false, 0},
		{"data-b-8", "d", 8, 12, false, 0},
		{"hb-9-late", "h", 9, 12, true, 1},
		{"hb-20", "h", 20, 20, false, 1},
		{"data-c-14", "d", 14, 20, false, 1},
		{"hb-17-late", "h", 17, 20, true, 2},
		{"hb-25", "h", 25, 25, false, 2},
		{"hb-25-equal-is-late", "h", 25, 25, true, 3},
	}
	s := New(3)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var wm int64
			var late bool
			if c.kind == "d" {
				wm = s.Feed(c.ts)
			} else {
				wm, late = s.Heartbeat(c.ts)
			}
			if wm != c.wantWM || late != c.wantLate || s.LateHeartbeats() != c.wantCnt {
				t.Fatalf("after %s: wm=%d late=%v lateCnt=%d, want wm=%d late=%v cnt=%d",
					c.name, wm, late, s.LateHeartbeats(), c.wantWM, c.wantLate, c.wantCnt)
			}
		})
	}
}
