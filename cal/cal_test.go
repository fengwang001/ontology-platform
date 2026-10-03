package cal

import (
	"errors"
	"testing"
)

func newTestCal(t *testing.T) *Calendar {
	t.Helper()
	c, err := New(540, 1080, 0b0011111)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestWorkBasicAndWindowEndpoints(t *testing.T) {
	c := newTestCal(t)
	cases := []struct {
		t1, t2, want int64
		reason       string
	}{
		{540, 1080, 540, "整窗 [open,close)"},
		{600, 1080, 480, "题面：今天剩余窗口"},
		{1080, 1980, 0, "窗口外到次日开窗前"},
		{1079, 1080, 1, "左闭：1079 这一分钟计"},
		{1080, 1081, 0, "右开：恰在末尾不计"},
		{540, 540, 0, "t1==t2 为 0"},
		{600, 2100, 480 + 120, "跨夜：今天尾 480 + 次日 120"},
		{1080, 1980 + 540, 540, "次日整窗"},
		{0, 7 * 1440, 5 * 540, "跨整周：5 个工作日"},
		{0, 3 * 7 * 1440, 15 * 540, "跨三周"},
		{5 * 1440, 7 * 1440, 0, "周六周日整日不工作"},
	}
	for _, tc := range cases {
		got, err := c.Work(tc.t1, tc.t2)
		if err != nil || got != tc.want {
			t.Errorf("Work(%d,%d) %s = %d,%v want %d", tc.t1, tc.t2, tc.reason, got, err, tc.want)
		}
	}
}

func TestWorkIllegalArgs(t *testing.T) {
	c := newTestCal(t)
	for _, tc := range [][2]int64{{-1, 10}, {0, -1}, {5, 4}, {0, MaxTime + 1}} {
		if _, err := c.Work(tc[0], tc[1]); !errors.Is(err, ErrArgument) {
			t.Errorf("Work(%d,%d) err=%v want ErrArgument", tc[0], tc[1], err)
		}
	}
	for _, tc := range []struct {
		o, cl int64
		m     uint8
	}{
		{-1, 10, 1}, {0, 1441, 1}, {540, 540, 1}, {900, 100, 1}, {10, 20, 0}, {10, 20, 128},
	} {
		if _, err := New(tc.o, tc.cl, tc.m); !errors.Is(err, ErrArgument) {
			t.Errorf("New(%d,%d,%d) err=%v want ErrArgument", tc.o, tc.cl, tc.m, err)
		}
	}
}

func TestAddHolidayRules(t *testing.T) {
	c := newTestCal(t)
	if err := c.AddHoliday(0, 1000); !errors.Is(err, ErrPast) {
		t.Fatalf("AddHoliday(0,1000)=%v want ErrPast", err)
	}
	if err := c.AddHoliday(1, 1000); err != nil {
		t.Fatalf("AddHoliday(1,1000)=%v", err)
	}
	if err := c.AddHoliday(1, 2000); err != nil {
		t.Fatalf("重复添加未来日应成功无操作: %v", err)
	}
	if err := c.AddHoliday(2, 2880); !errors.Is(err, ErrPast) {
		t.Fatalf("day==floor(now/1440) 应 ErrPast, got %v", err)
	}
	if err := c.AddHoliday(2, 1500); !errors.Is(err, ErrClock) {
		t.Fatalf("时钟倒退应 ErrClock, got %v", err)
	}
	w, _ := c.Work(1980, 2520)
	if w != 0 {
		t.Fatalf("节假日整日不工作, got %d", w)
	}
}

func TestAdvance(t *testing.T) {
	c := newTestCal(t)
	cases := []struct {
		from, need, at int64
	}{
		{600, 300, 900},
		{600, 480, 1080},
		{600, 481, 1981},
		{600, 600, 2100},
		{1080, 540, 1980 + 540},
		{540, 5 * 540, 4*1440 + 1080},
		{540, 10 * 540, 11*1440 + 1080},
	}
	for _, tc := range cases {
		at, ok := c.Advance(tc.from, tc.need)
		if !ok || at != tc.at {
			t.Errorf("Advance(%d,%d)=(%d,%v) want %d", tc.from, tc.need, at, ok, tc.at)
		}
		w, _ := c.Work(tc.from, at)
		if w != tc.need {
			t.Errorf("Work(from,at)=%d need %d", w, tc.need)
		}
	}
}

func TestAdvanceWithHoliday(t *testing.T) {
	c := newTestCal(t)
	if err := c.AddHoliday(1, 1000); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		from, need, at int64
	}{
		{2000, 1, 1440*2 + 541},
		{2000, 540, 1440*2 + 1080},
		{540, 6 * 540, 8*1440 + 1080},
	}
	for _, tc := range cases {
		at, ok := c.Advance(tc.from, tc.need)
		if !ok || at != tc.at {
			t.Errorf("Advance(%d,%d)=(%d,%v) want %d", tc.from, tc.need, at, ok, tc.at)
		}
	}
}
