package stride

import "testing"

func TestAlignUp(t *testing.T) {
	cases := []struct {
		name    string
		x, c, m int64
		want    int64
	}{
		{"x恰等于类值且已对齐", 3, 3, 4, 3},
		{"已对齐的大数倍", 8, 0, 4, 8},
		{"差1向上对齐到0类", 5, 0, 4, 8},
		{"差1向上对齐到3类", 2, 3, 4, 3},
		{"跨过多个类", 13, 2, 4, 14},
		{"类1差3", 14, 1, 4, 17},
		{"已对齐类1", 21, 1, 4, 21},
		{"x为1类0", 1, 0, 4, 4},
		{"x为1类1", 1, 1, 4, 1},
		{"大模数已对齐", 15, 15, 16, 15},
		{"大模数16对齐到类15", 16, 15, 16, 31},
		{"大模数差1", 16, 0, 16, 16},
		{"大模数跨类", 17, 15, 16, 31},
	}
	for _, tc := range cases {
		if got := AlignUp(tc.x, tc.c, tc.m); got != tc.want {
			t.Errorf("%s: AlignUp(%d,%d,%d)=%d, want %d",
				tc.name, tc.x, tc.c, tc.m, got, tc.want)
		}
	}
}

func TestOf(t *testing.T) {
	if got := Of(1, 4); got != 1 {
		t.Errorf("Of(1,4)=%d, want 1", got)
	}
	if got := Of(2, 4); got != 4 {
		t.Errorf("Of(2,4)=%d, want 4", got)
	}
	if got := Of(8, 16); got != 16 {
		t.Errorf("Of(8,16)=%d, want 16", got)
	}
}

func TestClass(t *testing.T) {
	for s := 1; s <= 8; s++ {
		if got := Class(s); got != int64(s-1) {
			t.Errorf("Class(%d)=%d, want %d", s, got, s-1)
		}
	}
}
