package adapt

import (
	"testing"

	"ontology/wmline"
)

// TestSettleCheckedConstant 证明结算是 O(1)：喂 m 个事件触发一次结算后，
// 结算时检查的事件条数是不随 m 增长的小常数，而不是回扫整个窗口。
func TestSettleCheckedConstant(t *testing.T) {
	for _, m := range []int64{100, 500, 1000, 5000, 10000} {
		c := New(wmline.New(0, 1<<40), 1, m, m, 0)
		for i := int64(0); i < m; i++ {
			c.Feed(i) // 全部正序，无迟到，触发一次 lc<=lo 的结算
		}
		if c.n != 0 {
			t.Fatalf("m=%d: 结算后 n 应复位为 0，实际 %d", m, c.n)
		}
		const smallConst = 1
		if c.checked > smallConst {
			t.Fatalf("m=%d: 结算检查了 %d 条，随窗口线性增长，疑似回扫", m, c.checked)
		}
	}
}

// TestSettleRules 表驱动核验三向结算规则与计数复位。
func TestSettleRules(t *testing.T) {
	cases := []struct {
		name      string
		lates     int64 // 窗口内迟到的条数
		wantDelay int64
	}{
		{"lc>=hi 上调", 3, 4},
		{"lc<=lo 下调", 0, 2}, // minDelay=2 托底
		{"lo<lc<hi 不变", 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(wmline.New(2, 10), 2, 3, 2, 0)
			// 先喂一条大 TS 确立 wm，再按 lates 混入迟到事件凑满窗口
			c.Feed(100) // wm=98
			fed, late := int64(1), int64(0)
			for fed < 3 {
				if late < tc.lates {
					c.Feed(100 - 10 - late) // 远小于 wm，必迟到
					late++
				} else {
					c.Feed(100 + fed) // 大于 wm，必正常
				}
				fed++
			}
			// 第一窗口含首条，共 3 条已结算一次；再喂满一个窗口看稳定结果
			for i := int64(0); i < 3; i++ {
				if late < tc.lates {
					c.Feed(1) // 必迟到
					late++
				} else {
					c.Feed(200 + i) // 必正常
				}
			}
			if got := c.Delay(); got != tc.wantDelay {
				t.Fatalf("delay=%d, 期望 %d", got, tc.wantDelay)
			}
			if c.lateCount != 0 || c.n != 0 {
				t.Fatalf("结算后计数未复位: n=%d lateCount=%d", c.n, c.lateCount)
			}
		})
	}
}
