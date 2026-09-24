// Command demo exercises the ISO 8601 week-date conversion end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/cal"
	"ontology/isoweek"
)

var ok = true

func check(name string, good bool) {
	status := "OK"
	if !good {
		status = "FAIL"
		ok = false
	}
	fmt.Println(status, name)
}

func main() {
	check("cal: 2026-01-01 is Thursday, 2020 is a 366-day leap year",
		cal.DayOfWeek(2026, 1, 1) == 4 && cal.DaysInYear(2020) == 366)
	samples := [][6]int{
		{2020, 12, 31, 2020, 53, 4}, {2021, 1, 1, 2020, 53, 5}, {2021, 1, 4, 2021, 1, 1},
		{2026, 1, 1, 2026, 1, 4}, {2026, 12, 31, 2026, 53, 4}, {2027, 1, 3, 2026, 53, 7},
	}
	good := true
	for _, s := range samples {
		iy, w, wd, err := api.ToISO(s[0], s[1], s[2])
		good = good && err == nil && iy == s[3] && w == s[4] && wd == s[5]
	}
	check("six derived sample dates", good)
	check("WeeksInYear 2020=53 2026=53", api.WeeksInYear(2020) == 53 && api.WeeksInYear(2026) == 53)
	iy, w, _, _ := api.ToISO(2021, 1, 1)
	jy, jw, _, _ := api.ToISO(2019, 12, 30)
	check("cross-year: 2021-01-01->2020-W53, 2019-12-30->2020-W01",
		iy == 2020 && w == 53 && jy == 2020 && jw == 1)
	check("round trip every day of 1900..2100 (SelfCheck)", api.SelfCheck(1900, 2100) == nil)
	_, _, _, e1 := api.ToISO(2021, 2, 30)
	_, _, _, e2 := api.FromISO(2021, 53, 1)
	_, _, _, e3 := api.FromISO(2021, 1, 8)
	check("distinct sentinels: bad date / bad week / bad weekday",
		errors.Is(e1, isoweek.ErrDate) && errors.Is(e2, isoweek.ErrWeek) &&
			errors.Is(e3, isoweek.ErrWeekday) && e1 != e2 && e2 != e3)
	check("self-check still passes after rejections", api.SelfCheck(2020, 2026) == nil)
	good = true
	for i := 0; i < 10000; i++ {
		_, _, _, err := api.ToISO(1900+i%201, i%12+1, i%28+1)
		good = good && err == nil
	}
	check("10000 sampled ToISO calls (weekday-lookup bound <=3 pinned by test)", good)
	dates := [][3]int{{2020, 12, 31}, {2021, 1, 1}, {2026, 12, 31}, {2027, 1, 3}}
	want := make([][3]int, len(dates))
	for i, dt := range dates {
		iy, w, wd, _ := api.ToISO(dt[0], dt[1], dt[2])
		want[i] = [3]int{iy, w, wd}
	}
	var allGood atomic.Bool
	allGood.Store(true)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, dt := range dates {
				iy, w, wd, err := api.ToISO(dt[0], dt[1], dt[2])
				if err != nil || [3]int{iy, w, wd} != want[i] {
					allGood.Store(false)
				}
			}
		}()
	}
	wg.Wait()
	check("16 goroutines x 4 dates, field-identical results", allGood.Load())
	if !ok {
		os.Exit(1)
	}
}
