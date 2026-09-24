package isoweek

import (
	"errors"
	"ontology/cal"
	"sync"
	"testing"
)

type kc struct{ y, m, d, wY, wW, wD int }

var knownCases = []kc{
	{2020, 12, 31, 2020, 53, 4}, {2021, 1, 1, 2020, 53, 5},
	{2021, 1, 4, 2021, 1, 1}, {2026, 1, 1, 2026, 1, 4},
	{2026, 12, 31, 2026, 53, 4}, {2027, 1, 3, 2026, 53, 7},
}

func TestKnownRoundTrip(t *testing.T) {
	for _, c := range knownCases {
		want := Date{c.wY, c.wW, c.wD}
		if got, err := ToISO(c.y, c.m, c.d); err != nil || got != want {
			t.Errorf("ToISO(%d-%d-%d)=%v,%v want %v", c.y, c.m, c.d, got, err, want)
		}
		if y, m, d, err := FromISO(c.wY, c.wW, c.wD); err != nil || y != c.y || m != c.m || d != c.d {
			t.Errorf("FromISO(%v)=%d-%d-%d,%v want %d-%d-%d", want, y, m, d, err, c.y, c.m, c.d)
		}
	}
}

func TestRoundTripAndRange(t *testing.T) {
	if err := SelfCheck(1900, 2100); err != nil {
		t.Fatal(err)
	}
}

func TestWeeksInYear(t *testing.T) {
	for y, want := range map[int]int{2020: 53, 2026: 53, 2015: 53, 2021: 52, 2027: 52, 1900: 52, 2000: 52} {
		if got := WeeksInYear(y); got != want {
			t.Errorf("WeeksInYear(%d)=%d want %d", y, got, want)
		}
	}
}

func TestErrors(t *testing.T) {
	for _, c := range [][3]int{{2021, 2, 30}, {2021, 13, 1}} {
		if got, err := ToISO(c[0], c[1], c[2]); !errors.Is(err, cal.ErrDate) || got != (Date{}) {
			t.Errorf("ToISO(%v)=%v,%v", c, got, err)
		}
	}
	for _, c := range [][2]int{{2021, 0}, {2021, 54}, {2021, 53}} {
		if y, m, d, err := FromISO(c[0], c[1], 1); !errors.Is(err, ErrWeek) || y+m+d != 0 {
			t.Errorf("FromISO(%v,1)=%d-%d-%d,%v", c, y, m, d, err)
		}
	}
	for _, day := range []int{0, 8} {
		if y, m, d, err := FromISO(2020, 1, day); !errors.Is(err, ErrWeekday) || y+m+d != 0 {
			t.Errorf("FromISO(2020,1,%d)=%d-%d-%d,%v", day, y, m, d, err)
		}
	}
	if ErrWeek == ErrWeekday || errors.Is(ErrWeek, cal.ErrDate) || errors.Is(ErrWeekday, cal.ErrDate) {
		t.Error("sentinel errors must be distinct")
	}
	if _, err := ToISO(2024, 6, 15); err != nil {
		t.Error("converter broken after rejections:", err)
	}
}

func TestWeekdayBudget(t *testing.T) {
	lo, hi := cal.Days(1900, 1, 1), cal.Days(2100, 12, 31)
	for i := 0; i < 10000; i++ {
		y, m, d := cal.FromDays(lo + (hi-lo)*i/10000)
		_, err := ToISO(y, m, d)
		if n := weekdayCallsLast.Load(); err != nil || n > 3 {
			t.Fatalf("ToISO(%d-%d-%d): err=%v calls=%d; limit 3", y, m, d, err, n)
		}
	}
}

func TestConcurrent(t *testing.T) {
	got := make([][]Date, 8)
	var wg sync.WaitGroup
	for k := range got {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for _, c := range knownCases {
				d, _ := ToISO(c.y, c.m, c.d)
				got[k] = append(got[k], d)
			}
		}(k)
	}
	wg.Wait()
	for k := range got {
		for i, c := range knownCases {
			if got[k][i] != (Date{c.wY, c.wW, c.wD}) {
				t.Fatalf("goroutine %d case %d: %v", k, i, got[k][i])
			}
		}
	}
}
