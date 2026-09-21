package bizday

import (
	"errors"
	"testing"
)

// 2026-09-21 周一 ... 2026-09-27 周日。
var testCalendar = mustNew(
	[]int{20260923, 20260926},           // 周三节假日；周六虽在 workdays 但节假日优先
	[]int{20260919, 20260926, 20260927}, // 周六、周日调休上班
)

func mustNew(holidays, workdays []int) *Calendar {
	c, err := New(holidays, workdays)
	if err != nil {
		panic(err)
	}
	return c
}

func TestNew_InvalidDate(t *testing.T) {
	if _, err := New([]int{20261301}, nil); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("New(holidays 非法) = %v, want ErrInvalidDate", err)
	}
	if _, err := New(nil, []int{20230229}); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("New(workdays 非法) = %v, want ErrInvalidDate", err)
	}
}

func TestNew_OverlapAllowed(t *testing.T) {
	// holidays 与 workdays 重叠不报错、不去重。
	if _, err := New([]int{20260926}, []int{20260926}); err != nil {
		t.Errorf("New(重叠) = %v, want nil", err)
	}
}

func TestIsBusinessDay(t *testing.T) {
	cases := []struct {
		name string
		d    int
		want bool
	}{
		{"普通周一", 20260921, true},
		{"普通周五", 20260925, true},
		{"普通周六", 20261003, false},
		{"普通周日", 20261004, false},
		{"节假日周三", 20260923, false},
		{"调休上班周六", 20260919, true},
		{"调休上班周日", 20260927, true},
		{"同时在两者中按节假日", 20260926, false},
	}
	for _, tc := range cases {
		got, err := testCalendar.IsBusinessDay(tc.d)
		if err != nil {
			t.Fatalf("IsBusinessDay(%d) err = %v", tc.d, err)
		}
		if got != tc.want {
			t.Errorf("%s: IsBusinessDay(%d) = %v, want %v", tc.name, tc.d, got, tc.want)
		}
	}
}

func TestIsBusinessDay_InvalidDate(t *testing.T) {
	if _, err := testCalendar.IsBusinessDay(20260931); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("IsBusinessDay(非法) = %v, want ErrInvalidDate", err)
	}
}
