package cron

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// naiveNextFire is the specification reference: it scans every minute
// after t in chronological order and applies the day/week OR/AND rule
// directly. It exists only for differential testing.
func naiveNextFire(s *Spec, t int) (int, bool) {
	for m := t + 1; m <= maxMinute && m-t <= 4000*1440; m++ {
		dt, err := FromMinute(m)
		if err != nil {
			return 0, false
		}
		if !s.months[dt.Month-1] {
			continue
		}
		dayOK := s.days[dt.Day-1]
		weekOK := s.weekdays[dt.Weekday]
		var dayMatch bool
		if !s.dayWild && !s.weekWild {
			dayMatch = dayOK || weekOK
		} else {
			dayMatch = dayOK && weekOK
		}
		if dayMatch && s.hours[dt.Hour] && s.minutes[dt.Minute] {
			return m, true
		}
	}
	return 0, false
}

// randomItem builds one comma-list item for field min..max.
func randomItem(r *rand.Rand, min, max int) string {
	count := max - min + 1
	switch r.Intn(6) {
	case 0:
		return "*"
	case 1:
		return itoa(min + r.Intn(count))
	case 2:
		a := min + r.Intn(count)
		b := a + r.Intn(max-a+1)
		return itoa(a) + "-" + itoa(b)
	case 3:
		return "*/" + itoa(1+r.Intn(count))
	case 4:
		// a/s : a up to field maximum.
		return itoa(min+r.Intn(count)) + "/" + itoa(1+r.Intn(count))
	default:
		a := min + r.Intn(count)
		b := a + r.Intn(max-a+1)
		return itoa(a) + "-" + itoa(b) + "/" + itoa(1+r.Intn(count))
	}
}

func randomField(r *rand.Rand, min, max int) string {
	n := 1 + r.Intn(3)
	text := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			text += ","
		}
		text += randomItem(r, min, max)
	}
	return text
}

// randomDiffSpec generates an expression guaranteed to fire within a
// few hundred days, so the naive scan stays cheap while the parser and
// matching logic still get highly varied inputs.
func randomDiffSpec(r *rand.Rand) string {
	minute := randomField(r, 0, 59)
	hour := randomField(r, 0, 23)

	// Keep months dense (at least 9 of 12) and day-of-month values at
	// most 28 so every selected month can always match.
	months := randomDenseField(r, 1, 12, 9)
	days := randomBoundedDayField(r)

	// Weekdays: either a dense non-wildcard list, a wildcard form, or a
	// single value. With OR semantics a single value already guarantees
	// weekly firings.
	var week string
	switch r.Intn(3) {
	case 0:
		week = "*"
	case 1:
		week = "*/" + itoa(1+r.Intn(7))
	default:
		n := 1 + r.Intn(3)
		for i := 0; i < n; i++ {
			if i > 0 {
				week += ","
			}
			week += itoa(r.Intn(7))
		}
	}
	return minute + " " + hour + " " + days + " " + months + " " + week
}

// randomDenseField returns a wildcard-ish or comma field containing at
// least minCount of the available values (values may repeat).
func randomDenseField(r *rand.Rand, min, max, minCount int) string {
	if r.Intn(2) == 0 {
		return "*/" + itoa(1+r.Intn((max-min+1)/2))
	}
	n := minCount + r.Intn(max-min-minCount+2)
	text := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			text += ","
		}
		text += itoa(min + r.Intn(max-min+1))
	}
	return text
}

// randomBoundedDayField yields a day field whose explicit values are
// all within 1..28; wildcard forms (*/s) still span 1..31 but that is
// safe because months dense in the short-month range still contain days
// 29-31 somewhere, and bounded values guarantee a match otherwise.
func randomBoundedDayField(r *rand.Rand) string {
	switch r.Intn(3) {
	case 0:
		return "*/" + itoa(1+r.Intn(14))
	default:
		n := 1 + r.Intn(3)
		text := ""
		for i := 0; i < n; i++ {
			if i > 0 {
				text += ","
			}
			a := 1 + r.Intn(28)
			if r.Intn(2) == 0 {
				text += itoa(a)
			} else {
				b := a + r.Intn(28-a+1)
				if r.Intn(3) == 0 {
					text += itoa(a) + "-" + itoa(b) + "/" + itoa(1+r.Intn(7))
				} else {
					text += itoa(a) + "-" + itoa(b)
				}
			}
		}
		return text
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func TestDifferentialRandom2000(t *testing.T) {
	const N = 2000
	r := rand.New(rand.NewSource(20261002))

	logFile, err := os.Create("diff_test.log")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	w := bufio.NewWriter(logFile)
	defer w.Flush()

	threeYears := 3 * 366 * 1440
	for i := 0; i < N; i++ {
		text := randomDiffSpec(r)
		start := r.Intn(threeYears + 1)
		s, perr := Parse(text)
		if perr != nil {
			t.Fatalf("generated spec should parse: %q: %v", text, perr)
		}
		got, gerr := NextFire(s, start)
		want, ok := naiveNextFire(s, start)

		rule := "AND (day/week combined)"
		if !s.dayWild && !s.weekWild {
			rule = "OR (neither day nor week wildcard)"
		}
		if ok {
			if gerr != nil || got != want {
				t.Fatalf("case %d mismatch\n spec=%q\n start=%s\n got=%v (%s)\n naive=%s\n rule=%s\n daySteps=%d",
					i, text, dt(start), gerr, dt(got), dt(want), rule, s.DaySteps())
			}
			fmt.Fprintf(w, "case %04d spec=%-28s start=%s => %s  [%s, daySteps=%d]\n",
				i, text, dt(start), dt(got), rule, s.DaySteps())
		} else {
			if gerr == nil {
				t.Fatalf("case %d: naive found none but NextFire returned %d for %q", i, got, text)
			}
			fmt.Fprintf(w, "case %04d spec=%-28s start=%s => NO NEXT FIRE  [%s]\n",
				i, text, dt(start), rule)
		}
	}
}
