package stride

import "testing"

func TestAlignUp(t *testing.T) {
	cases := []struct {
		x, c, m int64
		want    int64
	}{
		{5, 0, 4, 8},   // 差 1 越过类点，向上进位
		{8, 0, 4, 8},   // 已对齐，原地不动
		{2, 3, 4, 3},   // 同一周期内前进 1
		{4, 0, 4, 4},   // 已对齐
		{4, 1, 4, 5},   // 恰等于类点 x=4 余 0，进到余 1
		{13, 2, 4, 14}, // 规格例一：Join(3) 的起点
		{14, 1, 4, 17}, // 规格例三：Observe(2,13) 后
		{21, 1, 4, 21}, // 规格例三：Observe(2,20) 后已对齐
		{1, 0, 4, 4},   // 下界附近
		{1, 1, 4, 1},   // 下界已对齐
		{0, 0, 4, 0},
		{3, 3, 16, 3},
		{17, 15, 16, 31},
	}
	for _, tc := range cases {
		if got := AlignUp(tc.x, tc.c, tc.m); got != tc.want {
			t.Errorf("AlignUp(%d, %d, %d) = %d, want %d", tc.x, tc.c, tc.m, got, tc.want)
		}
	}
}

func TestMode(t *testing.T) {
	if got := Mode(1, 4); got != 1 {
		t.Errorf("Mode(1, 4) = %d, want 1", got)
	}
	if got := Mode(2, 4); got != 4 {
		t.Errorf("Mode(2, 4) = %d, want 4", got)
	}
	if got := Mode(8, 16); got != 16 {
		t.Errorf("Mode(8, 16) = %d, want 16", got)
	}
}
