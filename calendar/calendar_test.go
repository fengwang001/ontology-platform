package calendar

import (
	"errors"
	"testing"
)

func TestNewRejectsBadParams(t *testing.T) {
	cases := []struct {
		name       string
		tz, cs, ce int64
	}{
		{"tz too low", -43201, 0, 0},
		{"tz too high", 50401, 0, 0},
		{"cs low", 0, -1, 0},
		{"cs high", 0, 86400, 0},
		{"ce low", 0, 0, -1},
		{"ce high", 0, 0, 86400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.tz, tc.cs, tc.ce); !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("New(%d,%d,%d) err=%v, want ErrInvalidParam", tc.tz, tc.cs, tc.ce, err)
			}
		})
	}
	if _, err := New(-43200, 0, 0); err != nil {
		t.Fatalf("tz boundary: %v", err)
	}
	if _, err := New(50400, 86399, 86399); err != nil {
		t.Fatalf("upper boundary: %v", err)
	}
}

func TestDayAndSec(t *testing.T) {
	c, _ := New(-28800, 0, 0)
	cases := []struct {
		t        int64
		day      int64
		sec      int64
		dayStart int64
	}{
		{100, -1, 57700, -57600}, // 题给时区例
		{0, -1, 57600, -57600},
		{-57600, -1, 0, -57600},
		{-57601, -2, 86399, -144000},
	}
	for _, tc := range cases {
		if d := c.Day(tc.t); d != tc.day {
			t.Errorf("Day(%d)=%d want %d", tc.t, d, tc.day)
		}
		if s := c.SecOfDay(tc.t); s != tc.sec {
			t.Errorf("SecOfDay(%d)=%d want %d", tc.t, s, tc.sec)
		}
		if s := c.DayStart(tc.day); s != tc.dayStart {
			t.Errorf("DayStart(%d)=%d want %d", tc.day, s, tc.dayStart)
		}
	}

	g, _ := New(0, 0, 0)
	if d := g.Day(86399); d != 0 || g.Day(86400) != 1 || g.Day(-1) != -1 {
		t.Fatalf("tz0 day boundaries wrong: %d %d %d", g.Day(86399), g.Day(86400), g.Day(-1))
	}
}

func TestCurfew(t *testing.T) {
	cases := []struct {
		name       string
		cs, ce     int64
		t          int64
		want       bool
		nextStart  int64
		nextExists bool
	}{
		{"none any time", 0, 0, 3600, false, 0, false},
		{"within day inside", 79200, 28800, 80000, true, 165600, true},
		{"wrap morning inside", 79200, 28800, 100, true, 79200, true},
		{"wrap daytime outside", 79200, 28800, 40000, false, 79200, true},
		{"start inclusive", 100, 200, 100, true, 86500, true},
		{"end exclusive", 100, 200, 200, false, 86500, true},
		{"wrap cs inclusive", 200, 100, 200, true, 86600, true},
		{"wrap ce exclusive", 200, 100, 86400 + 100, false, 86600, true},
		{"wrap late night inside", 200, 100, 86399, true, 86600, true},
		{"just before wrap start", 200, 100, 199, false, 200, true},
		{"normal next after cs", 100, 200, 150, true, 86500, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := New(0, tc.cs, tc.ce)
			if got := c.CurfewAt(tc.t); got != tc.want {
				t.Errorf("CurfewAt(%d)=%v want %v [cs=%d ce=%d]", tc.t, got, tc.want, tc.cs, tc.ce)
			}
			s, ok := c.NextCurfewStart(tc.t)
			if ok != tc.nextExists || (ok && s != tc.nextStart) {
				t.Errorf("NextCurfewStart(%d)=(%d,%v) want (%d,%v)", tc.t, s, ok, tc.nextStart, tc.nextExists)
			}
		})
	}
}

func TestSetHoliday(t *testing.T) {
	c, _ := New(0, 0, 0)
	if err := c.SetHoliday(86399, 1, true); err != nil {
		t.Fatalf("set future day: %v", err)
	}
	if !c.IsHoliday(1) {
		t.Fatal("day 1 should be holiday")
	}
	if err := c.SetHoliday(86400, 1, true); !errors.Is(err, ErrTooLate) {
		t.Fatalf("day start == now: err=%v want ErrTooLate", err)
	}
	if err := c.SetHoliday(0, 0, true); !errors.Is(err, ErrTooLate) {
		t.Fatalf("current day: err=%v want ErrTooLate", err)
	}
	if err := c.SetHoliday(-1, 5, true); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("negative now: err=%v want ErrInvalidParam", err)
	}
	if err := c.SetHoliday(0, -10, true); !errors.Is(err, ErrTooLate) {
		t.Fatalf("negative day at now=0 already started: err=%v want ErrTooLate", err)
	}
	if err := c.SetHoliday(0, 1, false); err != nil {
		t.Fatalf("clear holiday: %v", err)
	}
	if c.IsHoliday(1) {
		t.Fatal("day 1 holiday should be cleared")
	}
}
