package isoweek

import (
	"errors"
	"sync"
	"testing"

	"ontology/cal"
)

func TestSampleDates(t *testing.T) { // six derived dates, both directions (invariants 1, 3)
	cases := [][6]int{
		{2020, 12, 31, 2020, 53, 4}, {2021, 1, 1, 2020, 53, 5}, {2021, 1, 4, 2021, 1, 1},
		{2026, 1, 1, 2026, 1, 4}, {2026, 12, 31, 2026, 53, 4}, {2027, 1, 3, 2026, 53, 7},
	}
	for _, c := range cases {
		iy, w, wd, err := ToISO(c[0], c[1], c[2])
		y, m, d, err2 := FromISO(c[3], c[4], c[5])
		if err != nil || err2 != nil || iy != c[3] || w != c[4] || wd != c[5] || y != c[0] || m != c[1] || d != c[2] {
			t.Errorf("case %v: got %d-W%02d-%d and %d-%02d-%02d", c, iy, w, wd, y, m, d)
		}
	}
}
func TestWeeksInYear(t *testing.T) { // pins invariant 2's 52/53 rule
	for y, want := range map[int]int{1900: 52, 2000: 52, 2020: 53, 2021: 52, 2025: 52, 2026: 53} {
		if got := WeeksInYear(y); got != want {
			t.Errorf("WeeksInYear(%d) = %d, want %d", y, got, want)
		}
	}
}
func TestRoundTrip(t *testing.T) { // pins invariants 1, 2, 3 over 1900..2100
	for y := 1900; y <= 2100; y++ {
		for m := 1; m <= 12; m++ {
			for d := 1; d <= cal.DaysInMonth(y, m); d++ {
				iy, w, wd, err := ToISO(y, m, d)
				yy, mm, dd, e2 := FromISO(iy, w, wd)
				if err != nil || e2 != nil || w < 1 || w > WeeksInYear(iy) || yy != y || mm != m || dd != d {
					t.Fatalf("day %d-%02d-%02d -> %d-W%02d-%d -> %d-%02d-%02d", y, m, d, iy, w, wd, yy, mm, dd)
				}
			}
		}
	}
	for iy := 1900; iy <= 2100; iy++ {
		for w := 1; w <= WeeksInYear(iy); w++ {
			for wd := 1; wd <= 7; wd++ {
				y, m, d, err := FromISO(iy, w, wd)
				jy, jw, jd, _ := ToISO(y, m, d)
				if err != nil || jy != iy || jw != w || jd != wd {
					t.Fatalf("week %d-W%02d-%d -> %d-%02d-%02d -> %d-W%02d-%d", iy, w, wd, y, m, d, jy, jw, jd)
				}
			}
		}
	}
}
func TestRejectedInputs(t *testing.T) { // pins invariant 4: distinct sentinels, no partial results
	toISO := func(y, m, d int) error { _, _, _, e := ToISO(y, m, d); return e }
	fromISO := func(y, w, d int) error { _, _, _, e := FromISO(y, w, d); return e }
	bad := []struct {
		err     error
		fn      func(int, int, int) error
		a, b, c int
	}{
		{ErrDate, toISO, 2021, 2, 30}, {ErrDate, toISO, 2021, 13, 1},
		{ErrWeek, fromISO, 2020, 0, 1}, {ErrWeek, fromISO, 2020, 54, 1}, {ErrWeek, fromISO, 2021, 53, 1},
		{ErrWeekday, fromISO, 2020, 1, 0}, {ErrWeekday, fromISO, 2020, 1, 8},
	}
	for _, b := range bad {
		if err := b.fn(b.a, b.b, b.c); !errors.Is(err, b.err) {
			t.Errorf("%v: got %v, want %v", b, err, b.err)
		}
	}
	if ErrDate == ErrWeek || ErrWeek == ErrWeekday || ErrDate == ErrWeekday || toISO(2021, 1, 4) != nil {
		t.Error("sentinels not distinct, or converter unusable after rejections")
	}
}
func TestWeekdayCallBound(t *testing.T) { // pins O(1): direct formula, no day-by-day accumulation
	for i := 0; i < 10000; i++ {
		_, _, _, err := ToISO(1900+i%201, i%12+1, i%28+1)
		if err != nil || dowCalls.Load() > 3 {
			t.Fatalf("sample %d: err=%v, weekday lookups=%d, want <= 3", i, err, dowCalls.Load())
		}
	}
}
func TestConcurrent(t *testing.T) { // pins goroutine safety, field-identical results
	dates := [][3]int{{2020, 12, 31}, {2021, 1, 1}, {2021, 1, 4}, {2026, 1, 1}, {2026, 12, 31}, {2027, 1, 3}}
	want := [][3]int{{2020, 53, 4}, {2020, 53, 5}, {2021, 1, 1}, {2026, 1, 4}, {2026, 53, 4}, {2026, 53, 7}}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, dt := range dates {
				if iy, w, wd, err := ToISO(dt[0], dt[1], dt[2]); err != nil || [3]int{iy, w, wd} != want[i] {
					t.Errorf("%v mismatch", dt)
				}
			}
		}()
	}
	wg.Wait()
}
