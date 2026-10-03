package cal

import (
	"errors"
	"math/rand/v2"
	"testing"
)

func TestNewValidation(t *testing.T) {
	for _, tc := range []struct {
		o, c int64
		m    uint8
		ok   bool
	}{
		{540, 1080, 0b0011111, true}, {0, 1440, 1, true},
		{-1, 10, 1, false}, {10, 10, 1, false}, {10, 9, 1, false},
		{0, 1441, 1, false}, {540, 1080, 0, false}, {540, 1080, 128, false},
	} {
		got := New(tc.o, tc.c, tc.m)
		t.Logf("New(%d,%d,%b) => nil=%v want ok=%v", tc.o, tc.c, tc.m, got == nil, tc.ok)
		if (got != nil) != tc.ok {
			t.Fatalf("New mismatch ok=%v", tc.ok)
		}
	}
}

func TestWorkBasic(t *testing.T) {
	c := New(540, 1080, 0b0011111)
	cases := []struct {
		t1, t2, want int64
		why          string
	}{
		{600, 900, 300, "10:00-15:00 today"},
		{600, 1080, 480, "up to window end, right-open"},
		{1080, 1980, 0, "closed evening to next open"},
		{540, 540, 0, "empty interval"},
		{1079, 1081, 1, "one minute straddling window end"},
		{1980, 2100, 120, "day1 window start"},
		{600, 2100, 600, "480 today + 120 next day, skips night"},
		{600, 3420, 1020, "480 day0 + 540 day1, day2 not started at open"},
	}
	for _, tc := range cases {
		got := c.Work(tc.t1, tc.t2)
		t.Logf("Work(%d,%d)=%d want %d (%s)", tc.t1, tc.t2, got, tc.want, tc.why)
		if got != tc.want {
			t.Fatalf("Work(%d,%d)=%d want %d", tc.t1, tc.t2, got, tc.want)
		}
	}
	if v := c.Work(-1, 1); v != -1 {
		t.Fatalf("invalid args want -1 got %d", v)
	}
}

func TestWorkCrossWeeks(t *testing.T) {
	c := New(0, 1440, 0b0011111) // all-day weekdays
	if got := c.Work(0, 1440*7); got != 5*1440 {
		t.Fatalf("one week=%d want %d", got, 5*1440)
	}
	// 从 day2 到 day21 共 19 天：14 个完整工作日 + 余下 5 天含 3 个工作日
	if got := c.Work(1440*2, 1440*21); got != 13*1440 {
		t.Fatalf("19-day span=%d want %d", got, 13*1440)
	}
	t.Logf("cross-multi-week Work verified")
}

func TestAddHoliday(t *testing.T) {
	c := New(540, 1080, 0b0011111)
	err := c.AddHoliday(0, 1000)
	if !errors.Is(err, ErrPast) {
		t.Fatalf("day0 at now1000 want ErrPast got %v", err)
	}
	t.Logf("AddHoliday(0,1000) => %v (ErrPast boundary)", err)
	err = c.AddHoliday(1, 1000)
	if err != nil {
		t.Fatalf("add day1: %v", err)
	}
	err = c.AddHoliday(1, 1200)
	if err != nil {
		t.Fatalf("duplicate add while still future should be no-op, got %v", err)
	}
	err = c.AddHoliday(2, 500)
	if !errors.Is(err, ErrClock) {
		t.Fatalf("backwards now want ErrClock got %v", err)
	}
	err = c.AddHoliday(1, 2000)
	if !errors.Is(err, ErrPast) {
		t.Fatalf("re-adding once-future day after it starts want ErrPast got %v", err)
	}
	if c.Clock() != 1200 {
		t.Fatalf("clock=%d want 1200", c.Clock())
	}
	if got := c.Work(1980, 2520); got != 0 {
		t.Fatalf("holiday day1 work=%d want 0", got)
	}
	if r, ok := c.advanceLocked(1200, 1); r != 3421 || !ok {
		t.Fatalf("advance after holiday=%d,%v want 3421", r, ok)
	}
	if r, ok := c.advanceLocked(3420, 1); r != 3421 || !ok {
		t.Fatalf("advance at window open=%d,%v want 3421", r, ok)
	}
	t.Logf("advance with holiday day1 => next work minute at day2 window")
}

func naiveWork(c *Calendar, t1, t2 int64) int64 {
	var n int64
	for t := t1; t < t2; t++ {
		d := t / 1440
		if c.mask>>(d%7)&1 == 1 && !c.isHolidayLocked(d) {
			x := t - d*1440
			if x >= c.open && x < c.close {
				n++
			}
		}
	}
	return n
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 7))
	for iter := 0; iter < 300; iter++ {
		open := rng.Int64N(10)
		closeV := open + 1 + rng.Int64N(1440-open)
		mask := uint8(1 + rng.IntN(127))
		c := New(open, closeV, mask)
		for k := rng.IntN(8); k > 0; k-- {
			d := rng.Int64N(60)
			c.holiday = append(c.holiday, d)
		}
		sortHolidays(c)
		base := rng.Int64N(20) * 1440
		t1 := base + rng.Int64N(1440)
		t2 := t1 + rng.Int64N(60*1440)
		if t2 > MaxTime {
			t2 = MaxTime
		}
		want := naiveWork(c, t1, t2)
		got := c.workLocked(t1, t2)
		if got != want {
			t.Fatalf("iter %d Work(%d,%d)=%d want %d params %d,%d,%b hol=%v",
				iter, t1, t2, got, want, open, closeV, mask, c.holiday)
		}
		if got > 0 {
			n := rng.Int64N(got) + 1
			r, ok := c.advanceLocked(t1, n)
			if !ok || c.workLocked(t1, r) != n || (r > t1 && c.workLocked(t1, r-1) == n) {
				t.Fatalf("iter %d advance t1=%d n=%d r=%d ok=%v w(r)=%d",
					iter, t1, n, r, ok, c.workLocked(t1, r))
			}
		}
	}
	t.Logf("300 random calendars match naive minute simulation (Work + advance)")
}

func sortHolidays(c *Calendar) {
	for i := 1; i < len(c.holiday); i++ {
		for j := i; j > 0 && c.holiday[j-1] > c.holiday[j]; j-- {
			c.holiday[j-1], c.holiday[j] = c.holiday[j], c.holiday[j-1]
		}
	}
	uniq := c.holiday[:0]
	for i, d := range c.holiday {
		if i == 0 || d != c.holiday[i-1] {
			uniq = append(uniq, d)
		}
	}
	c.holiday = uniq
}
