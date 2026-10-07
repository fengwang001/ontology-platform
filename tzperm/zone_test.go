package tzperm

import "testing"

func dayUTC(y, mo, d, hh, mm, ss int) int64 {
	// Days since 1970-01-01 (UTC), via a small civil-date converter, then
	// add the in-day seconds.
	secs := civilToUnix(int64(y), int64(mo), int64(d), int64(hh), int64(mm), int64(ss))
	return secs
}

// civilToUnix converts UTC civil time to Unix seconds without external
// dependencies. Algorithm: Howard Hinnant's days_from_civil.
func civilToUnix(y, m, d, hh, mm, ss int64) int64 {
	y2 := y
	if m <= 2 {
		m += 9
		y2--
	} else {
		m -= 3
	}
	era := floorDiv(y2, 400)
	yoe := y2 - era*400
	days := era*146097 +
		(153*m+2)/5 + d - 1 +
		yoe*365 + yoe/4 - yoe/100
	return days*86400 + hh*3600 + mm*60 + ss
}

func TestOffsetAtSegments(t *testing.T) {
	t0 := dayUTC(2024, 3, 10, 0, 0, 0)
	spring := dayUTC(2024, 3, 10, 2, 0, 0) // 02:00 UTC, US-style jump
	z := &ZoneRules{
		Name: "X",
		Transitions: []Transition{
			{At: t0 - 86400, Offset: -5 * 3600},
			{At: spring, Offset: -4 * 3600},
		},
	}
	if got := z.OffsetAt(spring - 1); got != -5*3600 {
		t.Fatalf("before transition offset = %d", got)
	}
	if got := z.OffsetAt(spring); got != -4*3600 {
		t.Fatalf("at/after transition offset = %d", got)
	}
}

func TestResolveWallSpringForwardGap(t *testing.T) {
	// A zone jumping 01:59:59 standard -> 03:00 daylight at instant
	// T = 06:30 UTC (standard offset -4.5h). The local readings
	// [02:00, 03:00) never occur.
	oldOff := OffsetSec(-4*3600 - 1800)
	newOff := OffsetSec(-3*3600 - 1800)
	tAt := int64(6*3600 + 30*60)
	z := &ZoneRules{
		Name: "G",
		Transitions: []Transition{
			{At: 0, Offset: oldOff},
			{At: tAt, Offset: newOff},
		},
	}
	// wall 02:30 is inside the gap [T+oldOff, T+newOff) = [02:00,03:00).
	wall := tAt + int64(oldOff) + 1800
	got, status := z.ResolveWall(wall)
	if status != WallGap {
		t.Fatalf("status = %v, want gap", status)
	}
	if want := wall - int64(newOff); got != want {
		t.Fatalf("gap instant = %d, want %d", got, want)
	}
}

func TestResolveWallFallBackOverlap(t *testing.T) {
	// At T=06:00 UTC offset changes -3.5h -> -4.5h: local clocks go
	// 02:30 daylight -> 01:30 standard. Readings in [01:30,02:30) repeat.
	// The earlier instant must be chosen deterministically.
	summerOff := OffsetSec(-3*3600 - 1800)
	winterOff := OffsetSec(-4*3600 - 1800)
	tAt := int64(6 * 3600)
	z := &ZoneRules{
		Name: "O",
		Transitions: []Transition{
			{At: 0, Offset: summerOff},
			{At: tAt, Offset: winterOff},
		},
	}
	wall := tAt + int64(winterOff) + 1800 // local 02:00, in [01:30,02:30)
	got, status := z.ResolveWall(wall)
	if status != WallOverlap {
		t.Fatalf("status = %v, want overlap", status)
	}
	earlier := wall - int64(summerOff)
	later := wall - int64(winterOff)
	if earlier >= later {
		t.Fatalf("test setup: earlier %d !< later %d", earlier, later)
	}
	if got != earlier {
		t.Fatalf("overlap instant = %d, want earlier %d", got, earlier)
	}
}

func TestSecondsOfDayNegative(t *testing.T) {
	// One second before the Unix epoch is 23:59:59 of the previous day.
	sod, dayStart := SecondsOfDay(-1)
	if sod != 86399 || dayStart != -86400 {
		t.Fatalf("sod=%d dayStart=%d", sod, dayStart)
	}
}

func TestWindowHalfOpenBoundaries(t *testing.T) {
	w := WindowRules{StartSec: 9 * 3600, EndSec: 17 * 3600}
	if !w.Contains(9 * 3600) {
		t.Fatal("start boundary must be inside")
	}
	if w.Contains(17 * 3600) {
		t.Fatal("end boundary must be outside")
	}
	wrap := WindowRules{StartSec: 22 * 3600, EndSec: 6 * 3600}
	if !wrap.Contains(0) {
		t.Fatal("midnight inside a wrap window")
	}
	if wrap.Contains(22*3600) == false {
		t.Fatal("wrap start inside")
	}
	if wrap.Contains(6 * 3600) {
		t.Fatal("wrap end excluded")
	}
	allDay := WindowRules{StartSec: 12 * 3600, EndSec: 12 * 3600}
	if !allDay.Contains(0) || !allDay.Contains(86399) {
		t.Fatal("equal endpoints means whole day")
	}
}

func TestWindowInvalidEndpoints(t *testing.T) {
	if (WindowRules{StartSec: -1, EndSec: 10}).Valid() {
		t.Fatal("negative start invalid")
	}
	if (WindowRules{StartSec: 0, EndSec: 86400}).Valid() {
		t.Fatal("end == 86400 invalid")
	}
}
