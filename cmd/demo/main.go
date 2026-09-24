package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/cal"
	"ontology/isoweek"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

var samples = []struct {
	y, m, d int
	want    api.Date
}{
	{2020, 12, 31, api.Date{Year: 2020, Week: 53, Day: 4}},
	{2021, 1, 1, api.Date{Year: 2020, Week: 53, Day: 5}},
	{2021, 1, 4, api.Date{Year: 2021, Week: 1, Day: 1}},
	{2026, 1, 1, api.Date{Year: 2026, Week: 1, Day: 4}},
	{2026, 12, 31, api.Date{Year: 2026, Week: 53, Day: 4}},
	{2027, 1, 3, api.Date{Year: 2026, Week: 53, Day: 7}},
}

func main() {
	ok := true
	for _, s := range samples {
		ok = ok && cal.Weekday(s.y, s.m, s.d) == s.want.Day
	}
	check("cal: weekdays of six samples", ok)
	ok = true
	for _, s := range samples {
		got, err := api.ToISO(s.y, s.m, s.d)
		ok = ok && err == nil && got == s.want
	}
	check("six sample dates", ok)
	check("WeeksInYear 2020=53 2026=53", api.WeeksInYear(2020) == 53 && api.WeeksInYear(2026) == 53)
	check("cross-year 2021-01-01 -> 2020-W53-5", roundTrip(2021, 1, 1, api.Date{Year: 2020, Week: 53, Day: 5}))
	check("cross-year 2027-01-03 -> 2026-W53-7", roundTrip(2027, 1, 3, api.Date{Year: 2026, Week: 53, Day: 7}))
	check("roundtrip 2024 every day", api.SelfCheck(2024, 2024) == nil)
	_, errDate := api.ToISO(2021, 2, 30)
	_, _, _, errWeek := api.FromISO(2021, 53, 1)
	_, _, _, errDay := api.FromISO(2020, 1, 8)
	check("three distinct sentinel errors", errors.Is(errDate, cal.ErrDate) &&
		errors.Is(errWeek, isoweek.ErrWeek) && errors.Is(errDay, isoweek.ErrWeekday) &&
		errDate != errWeek && errWeek != errDay)
	check("self-check passes after rejections", api.SelfCheck(2020, 2021) == nil)
	check("10000 samples weekday calls<=3", isoweek.WeekdayBudgetOK(10000, 3))
	check("concurrent results identical", concurrentOK())
	if failed {
		os.Exit(1)
	}
}

func roundTrip(y, m, d int, want api.Date) bool {
	got, err := api.ToISO(y, m, d)
	if err != nil || got != want {
		return false
	}
	ry, rm, rd, err := api.FromISO(got.Year, got.Week, got.Day)
	return err == nil && ry == y && rm == m && rd == d
}

func concurrentOK() bool {
	const g = 8
	got := make([][]api.Date, g)
	var wg sync.WaitGroup
	for k := range got {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			got[k] = make([]api.Date, len(samples))
			for i, s := range samples {
				got[k][i], _ = api.ToISO(s.y, s.m, s.d)
			}
		}(k)
	}
	wg.Wait()
	for k := range got {
		for i, s := range samples {
			if got[k][i] != s.want {
				return false
			}
		}
	}
	return true
}
