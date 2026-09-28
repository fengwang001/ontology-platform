package counter

import "testing"

func TestFloorDiv(t *testing.T) {
	cases := []struct {
		a, b, want int64
	}{
		{0, 10, 0},
		{3, 10, 0},
		{10, 10, 1},
		{23, 10, 2},
		{-1, 10, -1}, // 向零整除会得 0，floor 必须为 -1
		{-3, 10, -1},
		{-10, 10, -1},
		{-13, 10, -2},
		{-20, 10, -2},
	}
	for _, tc := range cases {
		if got := floorDiv(tc.a, tc.b); got != tc.want {
			t.Errorf("floorDiv(%d,%d)=%d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestWindowStartEnd(t *testing.T) {
	const size int64 = 10
	cases := []struct {
		ts        int64
		wantStart int64
		wantEnd   int64
	}{
		{0, 0, 10},
		{9, 0, 10},
		{10, 10, 20},  // 恰好落在大窗口边界：归入右侧窗口
		{-1, -10, 0},  // 负时间戳归入左侧（更负）的窗口
		{-10, -10, 0}, // 边界本身属于右侧窗口 [-10,0)
		{-11, -20, -10},
		{20, 20, 30},
		{-20, -20, -10},
	}
	for _, tc := range cases {
		gotS := windowStart(tc.ts, size)
		gotE := windowEnd(tc.ts, size)
		if gotS != tc.wantStart || gotE != tc.wantEnd {
			t.Errorf("ts=%d window=[%d,%d), want [%d,%d)",
				tc.ts, gotS, gotE, tc.wantStart, tc.wantEnd)
		}
		// 归属唯一性：起点必须不大于 ts，终点必须严格大于 ts。
		if !(gotS <= tc.ts && tc.ts < gotE) {
			t.Errorf("ts=%d 不落在自己的窗口 [%d,%d) 内", tc.ts, gotS, gotE)
		}
	}
}

func TestFirstSubEnd(t *testing.T) {
	const size, step int64 = 10, 5
	cases := []struct {
		ts       int64
		wantEnd  int64 // 最小子窗口终点
		wantWin0 int64
	}{
		{0, 5, 0},
		{4, 5, 0},
		{5, 10, 0}, // 恰好落在子窗口终点：该终点左闭右开不含它，取下一个
		{9, 10, 0},
		{10, 15, 10}, // 大窗口边界：新窗口的第一个终点
		{-1, 0, -10}, // 负时间戳：终点 -5 早于 -1，唯一能计入的终点是 0
		{-5, 0, -10}, // 恰好落在子窗口终点 -5：该终点左闭右开不含它，取下一个 0
		{-6, -5, -10},
		{-10, -5, -10},
	}
	for _, tc := range cases {
		start := windowStart(tc.ts, size)
		if start != tc.wantWin0 {
			t.Errorf("ts=%d start=%d, want %d", tc.ts, start, tc.wantWin0)
		}
		if got := firstSubEnd(tc.ts, start, step); got != tc.wantEnd {
			t.Errorf("ts=%d firstSubEnd=%d, want %d", tc.ts, got, tc.wantEnd)
		}
	}
}
