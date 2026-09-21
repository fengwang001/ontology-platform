package bizday

import (
	"errors"
	"testing"
)

func TestCountBusinessDays(t *testing.T) {
	cases := []struct {
		name     string
		from, to int
		want     int
	}{
		{"空区间恒为0", 20260921, 20260921, 0},
		{"单天工作日", 20260921, 20260922, 1},
		{"to当日不计入", 20260921, 20260926, 5},
		{"整周含周末", 20260921, 20260928, 5},
		{"纯周末为0", 20260926, 20260928, 0},
		{"跨月", 20260930, 20261003, 3},
		{"跨年", 20261231, 20270102, 2},
		{"跨闰年2月29日", 20240228, 20240302, 3},
	}
	for _, tc := range cases {
		got, err := plainCalendar.CountBusinessDays(tc.from, tc.to)
		if err != nil {
			t.Fatalf("%s: err = %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: CountBusinessDays(%d, %d) = %d, want %d",
				tc.name, tc.from, tc.to, got, tc.want)
		}
	}
}

func TestCountBusinessDays_WithCalendar(t *testing.T) {
	// 20260921(一)✓ 22(二)✓ 23(三,节假日)✗ 24(四)✓ 25(五)✓
	// 26(六,重叠按节假日)✗ 27(日,调休)✓ → 共 5 天。
	got, err := testCalendar.CountBusinessDays(20260921, 20260928)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != 5 {
		t.Errorf("CountBusinessDays = %d, want 5", got)
	}
}

func TestCountBusinessDays_Errors(t *testing.T) {
	if _, err := plainCalendar.CountBusinessDays(20260928, 20260921); !errors.Is(err, ErrInvalidRange) {
		t.Errorf("from > to = %v, want ErrInvalidRange", err)
	}
	if _, err := plainCalendar.CountBusinessDays(20261301, 20261001); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("from 非法 = %v, want ErrInvalidDate", err)
	}
	if _, err := plainCalendar.CountBusinessDays(20260901, 20260931); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("to 非法 = %v, want ErrInvalidDate", err)
	}
}
