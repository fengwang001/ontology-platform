package cron

import "testing"

func TestToFromMinuteRoundTrip(t *testing.T) {
	cases := []struct {
		y, mo, d, h, mi int
		t               int
		w               int
	}{
		{2000, 1, 1, 0, 0, 0, 6},
		{2000, 1, 2, 0, 0, 1440, 0}, // Sunday
		{2000, 1, 7, 0, 0, 6 * 1440, 5},
		{2000, 3, 1, 0, 0, 60 * 1440, 3},
		{2000, 12, 31, 23, 59, 366*1440 - 1, 0},
		{2001, 1, 1, 0, 0, 366 * 1440, 1},
		// 100 years 2000..2099 contain 25 leap years (incl. 2000).
		{2100, 3, 1, 0, 0, (100*365 + 25 + 31 + 28) * 1440, 1},
		{2199, 12, 31, 23, 59, maxMinute, 2},
	}
	for _, c := range cases {
		got, err := ToMinute(c.y, c.mo, c.d, c.h, c.mi)
		if err != nil || got != c.t {
			t.Fatalf("ToMinute(%04d-%02d-%02d %02d:%02d) = %d,%v want %d",
				c.y, c.mo, c.d, c.h, c.mi, got, err, c.t)
		}
		dt, err := FromMinute(c.t)
		if err != nil {
			t.Fatalf("FromMinute(%d): %v", c.t, err)
		}
		if dt.Year != c.y || dt.Month != c.mo || dt.Day != c.d ||
			dt.Hour != c.h || dt.Minute != c.mi || dt.Weekday != c.w {
			t.Fatalf("FromMinute(%d) = %+v", c.t, dt)
		}
	}
}

func TestLeapRules(t *testing.T) {
	leaps := map[int]bool{2000: true, 2004: true, 2096: true, 2100: false, 2104: true, 2196: true, 2199: false}
	for y, want := range leaps {
		if isLeap(y) != want {
			t.Fatalf("isLeap(%d) = %v want %v", y, !want, want)
		}
	}
	if daysInMonth(2000, 2) != 29 || daysInMonth(2100, 2) != 28 {
		t.Fatal("February length wrong")
	}
	if _, err := ToMinute(2100, 2, 29, 0, 0); err != ErrInvalidTime {
		t.Fatalf("2100-02-29 should be illegal, got %v", err)
	}
	if _, err := ToMinute(2000, 2, 30, 0, 0); err != ErrInvalidTime {
		t.Fatal("2000-02-30 should be illegal")
	}
	if _, err := ToMinute(2000, 13, 1, 0, 0); err != ErrInvalidTime {
		t.Fatal("month 13 should be illegal")
	}
	if _, err := FromMinute(-1); err != ErrInvalidTime {
		t.Fatal("-1 should be illegal")
	}
	if _, err := FromMinute(maxMinute + 1); err != ErrInvalidTime {
		t.Fatal("maxMinute+1 should be illegal")
	}
}
