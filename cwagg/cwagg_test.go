package cwagg

import "testing"

func key26(k int) string { return string(rune('a'+k%26)) + string(rune('A'+k/26)) }

// 触发判定的窗口检查数不随开放窗口数 m 增长：恒为 1（floor(Pos/size) 直接定位）。
func TestCheckedWindowsConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		a := New(5, 2)
		for k := 0; k < m; k++ { // m 个 Key 各喂 size-1 个元素，全部未触发
			for p := int64(0); p < 4; p++ {
				a.Add(key26(k), p, 1)
			}
		}
		if _, fired := a.Add(key26(m/2), 4, 1); !fired { // 往其中恰好 1 个窗口喂第 size 个元素
			t.Fatalf("m=%d: expected fire", m)
		}
		if a.checked != 1 {
			t.Fatalf("m=%d: checked windows = %d, want 1", m, a.checked)
		}
	}
}

// 水位单调不回退（含被丢弃元素），cnt 恒 <= size 且触发后不再增长。
func TestWatermarkCount(t *testing.T) {
	a := New(5, 2)
	seq := []struct {
		pos, val int64
	}{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {4, 5}, {9, 9}, {7, 7}, {6, 6}, {8, 8}, {3, 30}}
	prev := int64(-1)
	for _, s := range seq {
		a.Add("k", s.pos, s.val)
		got := a.keys["k"].wm
		if got < prev {
			t.Fatalf("watermark regressed: %d -> %d", prev, got)
		}
		prev = got
		for w, win := range a.keys["k"].wins {
			if win.cnt > 5 {
				t.Fatalf("window %d cnt=%d exceeds size", w, win.cnt)
			}
			if win.fired && win.cnt != 5 {
				t.Fatalf("fired window %d cnt changed to %d", w, win.cnt)
			}
		}
	}
	if a.keys["k"].wm != 9 {
		t.Fatalf("final wm = %d, want 9", a.keys["k"].wm)
	}
	if a.dropped != 1 { // 仅 (6,6) 超出 lateness 被丢弃；(3,30) 是重复投递，幂等忽略不计入丢弃
		t.Fatalf("dropped = %d, want 1", a.dropped)
	}
}
