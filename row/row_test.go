package row

import (
	"slices"
	"testing"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		name string
		a, b Row
		want int
	}{
		{"key 决定先后", Row{1, "b"}, Row{2, "a"}, -1},
		{"key 相同比 ID", Row{2, "a"}, Row{2, "b"}, -1},
		{"key 相同 ID 大者靠后", Row{2, "b"}, Row{2, "a"}, 1},
		{"完全相等", Row{2, "a"}, Row{2, "a"}, 0},
		{"负 key", Row{-1, "z"}, Row{0, "a"}, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compare(tt.a, tt.b)
			if got != tt.want && (got < 0) != (tt.want < 0) || got == 0 != (tt.want == 0) {
				t.Fatalf("Compare(%v, %v) = %d, want sign of %d", tt.a, tt.b, got, tt.want)
			}
			if got := Less(tt.a, tt.b); got != (tt.want < 0) {
				t.Fatalf("Less(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want < 0)
			}
		})
	}
}

// TestTotalOrder 验证 (Key, ID) 在 Key 大量重复时仍是严格全序：
// 对任意两行 a<b 必有 b>a 且 a!=b。
func TestTotalOrder(t *testing.T) {
	rows := []Row{
		{2, "a"}, {2, "b"}, {2, "c"}, {2, "d"}, {2, "e"}, {2, "f"},
		{1, "z"}, {3, "a"},
	}
	slices.SortFunc(rows, Compare)
	for i, a := range rows {
		for j, b := range rows {
			got := Compare(a, b)
			switch {
			case i == j && got != 0:
				t.Fatalf("Compare(%v,%v)=%d, want 0", a, b, got)
			case i < j && got >= 0:
				t.Fatalf("Compare(%v,%v)=%d, want <0", a, b, got)
			case i > j && got <= 0:
				t.Fatalf("Compare(%v,%v)=%d, want >0", a, b, got)
			}
		}
	}
}
