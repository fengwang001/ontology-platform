package cron

import (
	"fmt"
	"testing"
)

func mustT(t *testing.T, y, mo, d, h, mi int) int {
	t.Helper()
	v, err := ToMinute(y, mo, d, h, mi)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func fireSeq(t *testing.T, spec string, start int, n int) []int {
	t.Helper()
	s, err := Parse(spec)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]int, 0, n)
	cur := start
	for i := 0; i < n; i++ {
		f, err := NextFire(s, cur)
		if err != nil {
			t.Fatalf("NextFire #%d: %v", i, err)
		}
		out = append(out, f)
		cur = f
	}
	return out
}

func TestDayOrWeekOrRule(t *testing.T) {
	// 0 0 13 * 5: day 13 OR Friday.
	got := fireSeq(t, "0 0 13 * 5", mustT(t, 2000, 1, 1, 0, 0), 3)
	want := []int{
		mustT(t, 2000, 1, 7, 0, 0),
		mustT(t, 2000, 1, 13, 0, 0),
		mustT(t, 2000, 1, 14, 0, 0),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("OR rule #%d: got %d want %d", i, got[i], want[i])
		}
	}
}

func TestDayOrWeekAndRule(t *testing.T) {
	// 0 0 */2 * 5: odd days AND Friday.
	got := fireSeq(t, "0 0 */2 * 5", mustT(t, 2000, 1, 1, 0, 0), 2)
	want := []int{
		mustT(t, 2000, 1, 7, 0, 0),
		mustT(t, 2000, 1, 21, 0, 0), // Jan 14 is even -> excluded
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AND rule #%d: got %d want %d", i, got[i], want[i])
		}
	}

	// Bare numbers on both fields: OR even if both single values.
	got = fireSeq(t, "0 0 15 * 1", mustT(t, 2000, 1, 1, 0, 0), 3)
	// Jan 3 (Mon), Jan 10 (Mon), Jan 15 (Sat, day 15).
	want = []int{
		mustT(t, 2000, 1, 3, 0, 0),
		mustT(t, 2000, 1, 10, 0, 0),
		mustT(t, 2000, 1, 15, 0, 0),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("single-value OR #%d: got %d want %d", i, got[i], want[i])
		}
	}
}

func TestWeekdayZeroIsSunday(t *testing.T) {
	got := fireSeq(t, "0 0 * * 0", mustT(t, 2000, 1, 1, 0, 0), 2)
	want := []int{
		mustT(t, 2000, 1, 2, 0, 0),
		mustT(t, 2000, 1, 9, 0, 0),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Sunday #%d: got %d want %d", i, got[i], want[i])
		}
	}
}

func TestFeb29CenturyRule(t *testing.T) {
	s, _ := Parse("0 0 29 2 *")
	f, err := NextFire(s, mustT(t, 2096, 2, 29, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if f != mustT(t, 2104, 2, 29, 0, 0) {
		t.Fatalf("got %d", f)
	}
	// 2096 itself, then 2104: 2100 is not a leap year.
	seq := fireSeq(t, "0 0 29 2 *", mustT(t, 2095, 1, 1, 0, 0), 2)
	if seq[0] != mustT(t, 2096, 2, 29, 0, 0) || seq[1] != mustT(t, 2104, 2, 29, 0, 0) {
		t.Fatalf("got %v", seq)
	}
}

func TestDay31SkipsShortMonths(t *testing.T) {
	// 0 0 31 * * : Jan 31, Mar 31 (Feb skipped), May 31 (Apr skipped).
	got := fireSeq(t, "0 0 31 * *", mustT(t, 2000, 1, 1, 0, 0), 3)
	want := []int{
		mustT(t, 2000, 1, 31, 0, 0),
		mustT(t, 2000, 3, 31, 0, 0),
		mustT(t, 2000, 5, 31, 0, 0),
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("31st #%d: got %d want %d", i, got[i], want[i])
		}
	}
}

func TestNoNextFire(t *testing.T) {
	s, err := Parse("0 0 31 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NextFire(s, 0); err != ErrNoNextFire {
		t.Fatalf("Feb 31 must have no fire, got %v", err)
	}
	// Day steps bounded even when nothing matches.
	if s.DaySteps() > searchDays {
		t.Fatalf("day steps %d exceed %d", s.DaySteps(), searchDays)
	}
	if s.DaySteps() != 4001 {
		t.Fatalf("impossible spec should examine exactly 4001 days, got %d", s.DaySteps())
	}

	// Fire beyond 2199: 0 0 1 1 * next after 2199-01-01 would be 2200.
	s2, _ := Parse("0 0 1 1 *")
	if _, err := NextFire(s2, mustT(t, 2199, 1, 1, 0, 0)); err != ErrNoNextFire {
		t.Fatalf("got %v", err)
	}
}

func TestNextFireStrictlyGreater(t *testing.T) {
	s, _ := Parse("0 * * * *")
	match := 60
	f, err := NextFire(s, match)
	if err != nil {
		t.Fatal(err)
	}
	if f != 120 {
		t.Fatalf("at a match must return the next one, got %d", f)
	}
	f, err = NextFire(s, match-1)
	if err != nil || f != match {
		t.Fatalf("just before match should return match, got %d %v", f, err)
	}
}

func TestNextFireInvalidTime(t *testing.T) {
	s, _ := Parse("* * * * *")
	if _, err := NextFire(s, -1); err != ErrInvalidTime {
		t.Fatalf("got %v", err)
	}
	if _, err := NextFire(s, maxMinute+1); err != ErrInvalidTime {
		t.Fatalf("got %v", err)
	}
	if _, err := NextFire(nil, 0); err != ErrInvalidArgument {
		t.Fatalf("nil spec: got %v", err)
	}
}

func TestDayStepsBounded(t *testing.T) {
	// Worst case within a 4000-day guarantee: a rare pattern.
	s, _ := Parse("0 0 29 2 *")
	_, _ = NextFire(s, 0)
	if s.DaySteps() > searchDays {
		t.Fatalf("steps %d > %d", s.DaySteps(), searchDays)
	}
	// Within-day jump: starting late on a matching day.
	s2, _ := Parse("59 23 * * *")
	f, _ := NextFire(s2, 0)
	if f != 23*60+59 {
		t.Fatalf("got %d", f)
	}
	if s2.DaySteps() != 1 {
		t.Fatalf("expected 1 examined day, got %d", s2.DaySteps())
	}
}

func dt(t int) string {
	d, _ := FromMinute(t)
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d", d.Year, d.Month, d.Day, d.Hour, d.Minute)
}
