package bizday

import (
	"errors"
	"testing"
)

// 基准日历：20260922(周二) 是节假日；20260919(周六) 调休上班；
// 20260921(周一) 同时在两个列表中，按非工作日处理。
func newTestCalendar(t *testing.T) *Calendar {
	t.Helper()
	c, err := New(
		[]int{20260922, 20260921},
		[]int{20260919, 20260921},
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestIsBusinessDay(t *testing.T) {
	c := newTestCalendar(t)
	cases := []struct {
		d    int
		want bool
	}{
		{20260921, false}, // 同时在 holidays 和 workdays，节假日优先
		{20260922, false}, // 周二是节假日
		{20260919, true},  // 周六调休上班
		{20260920, false}, // 普通周日
		{20260918, true},  // 普通周五
		{20260916, true},  // 普通周三
	}
	for _, tc := range cases {
		got, err := c.IsBusinessDay(tc.d)
		if err != nil {
			t.Fatalf("IsBusinessDay(%d): %v", tc.d, err)
		}
		if got != tc.want {
			t.Errorf("IsBusinessDay(%d) = %v, want %v", tc.d, got, tc.want)
		}
	}
}

func TestNewRejectsInvalidDate(t *testing.T) {
	if _, err := New([]int{20260230}, nil); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("New(holidays) err = %v, want ErrInvalidDate", err)
	}
	if _, err := New(nil, []int{30000101}); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("New(workdays) err = %v, want ErrInvalidDate", err)
	}
}

func TestIsBusinessDayInvalidDate(t *testing.T) {
	c := newTestCalendar(t)
	if _, err := c.IsBusinessDay(20261301); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("err = %v, want ErrInvalidDate", err)
	}
}

func TestAddBusinessDaysForward(t *testing.T) {
	c := newTestCalendar(t)
	cases := []struct {
		d    int
		n    int
		want int
	}{
		{20260918, 1, 20260919}, // 周五+1：周六调休上班
		{20260919, 1, 20260923}, // 周六+1：跳过周日、节假日周一/周二
		{20260916, 2, 20260918}, // 周三+2：周四、周五
		{20260930, 1, 20261001}, // 跨月
		{20261231, 1, 20270101}, // 跨年
		{20280228, 1, 20280229}, // 跨闰年 2 月 29 日
		{20280227, 2, 20280229}, // 周日起数，跨过闰日
	}
	for _, tc := range cases {
		got, err := c.AddBusinessDays(tc.d, tc.n)
		if err != nil {
			t.Fatalf("AddBusinessDays(%d, %d): %v", tc.d, tc.n, err)
		}
		if got != tc.want {
			t.Errorf("AddBusinessDays(%d, %d) = %d, want %d", tc.d, tc.n, got, tc.want)
		}
	}
}

func TestAddBusinessDaysBackward(t *testing.T) {
	c := newTestCalendar(t)
	// 周一(节假日)往前数 1 个工作日：跳过周日、周六(调休上班) → 周六
	got, err := c.AddBusinessDays(20260921, -1)
	if err != nil {
		t.Fatalf("AddBusinessDays: %v", err)
	}
	if got != 20260919 {
		t.Errorf("got %d, want 20260919", got)
	}
	// 跨月往前
	got, err = c.AddBusinessDays(20261001, -1)
	if err != nil {
		t.Fatalf("AddBusinessDays: %v", err)
	}
	if got != 20260930 {
		t.Errorf("got %d, want 20260930", got)
	}
}

func TestAddBusinessDaysZero(t *testing.T) {
	c := newTestCalendar(t)
	got, err := c.AddBusinessDays(20260916, 0) // 周三是工作日
	if err != nil || got != 20260916 {
		t.Errorf("AddBusinessDays(20260916, 0) = %d, %v", got, err)
	}
	if _, err := c.AddBusinessDays(20260920, 0); !errors.Is(err, ErrNotBusinessDay) {
		t.Errorf("周日 n=0 err = %v, want ErrNotBusinessDay", err)
	}
	if _, err := c.AddBusinessDays(20260922, 0); !errors.Is(err, ErrNotBusinessDay) {
		t.Errorf("节假日 n=0 err = %v, want ErrNotBusinessDay", err)
	}
	if _, err := c.AddBusinessDays(20260230, 0); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("非法日期 n=0 err = %v, want ErrInvalidDate", err)
	}
}

func TestCountBusinessDays(t *testing.T) {
	c := newTestCalendar(t)
	// [周一, 下周一)：周一/周二节假日 → 周三至周五 = 3
	got, err := c.CountBusinessDays(20260921, 20260928)
	if err != nil {
		t.Fatalf("CountBusinessDays: %v", err)
	}
	if got != 3 {
		t.Errorf("got %d, want 3", got)
	}
	// 空区间恒为 0
	got, err = c.CountBusinessDays(20260921, 20260921)
	if err != nil || got != 0 {
		t.Errorf("CountBusinessDays(d, d) = %d, %v", got, err)
	}
	// to 不计入：[周五, 周六) 只含周五
	got, _ = c.CountBusinessDays(20260918, 20260919)
	if got != 1 {
		t.Errorf("got %d, want 1", got)
	}
	// 跨闰年 2 月：[20280227(周日), 20280302) → 28/29/1 三个工作日
	got, _ = c.CountBusinessDays(20280227, 20280302)
	if got != 3 {
		t.Errorf("got %d, want 3", got)
	}
}

func TestCountBusinessDaysErrors(t *testing.T) {
	c := newTestCalendar(t)
	if _, err := c.CountBusinessDays(20260922, 20260921); !errors.Is(err, ErrInvalidRange) {
		t.Errorf("from > to err = %v, want ErrInvalidRange", err)
	}
	if _, err := c.CountBusinessDays(20260229, 20260301); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("非法 from err = %v, want ErrInvalidDate", err)
	}
	if _, err := c.CountBusinessDays(20260921, 20260931); !errors.Is(err, ErrInvalidDate) {
		t.Errorf("非法 to err = %v, want ErrInvalidDate", err)
	}
}
