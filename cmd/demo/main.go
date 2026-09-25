package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ontology/api"
	"ontology/tzone"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func main() {
	svc, err := api.New("America/New_York")
	if err != nil {
		check("api.New", false)
	}
	z, _ := tzone.Load("America/New_York")
	loc := z.Location()

	five := []api.Event{
		{EventTime: 1768451400, Key: "e1"}, {EventTime: 1784089800, Key: "e2"},
		{EventTime: 1772944200, Key: "e3"}, {EventTime: 1793507400, Key: "e4"},
		{EventTime: 1773030600, Key: "e5"},
	}
	want := []string{"2026-01-14", "2026-07-15", "2026-03-07", "2026-11-01", "2026-03-09"}
	for i := 0; i < 5; i += 2 {
		line := ""
		ok := true
		for j := i; j < i+2 && j < 5; j++ {
			d, err := svc.Bucket(five[j].EventTime)
			local := time.Unix(five[j].EventTime, 0).In(loc).Format("01-02 15:04 MST")
			line += fmt.Sprintf("e%d %s->%s  ", j+1, local, d)
			ok = ok && err == nil && d == want[j]
		}
		check(line, ok)
	}

	dayLen := func(y int, m time.Month, d int) int64 {
		return time.Date(y, m, d+1, 0, 0, 0, 0, loc).Unix() - time.Date(y, m, d, 0, 0, 0, 0, loc).Unix()
	}
	check("DST 23h/25h days", dayLen(2026, 3, 8) == 82800 && dayLen(2026, 11, 1) == 90000 && dayLen(2026, 6, 15) == 86400)

	midOK := true
	for _, day := range []time.Time{time.Date(2026, 3, 8, 0, 0, 0, 0, loc), time.Date(2026, 11, 1, 0, 0, 0, 0, loc), time.Date(2026, 6, 15, 0, 0, 0, 0, loc)} {
		m := day.Unix()
		d0, _ := svc.Bucket(m)
		d1, _ := svc.Bucket(m - 1)
		midOK = midOK && d0 == day.Format("2006-01-02") && d1 == day.AddDate(0, 0, -1).Format("2006-01-02")
	}
	check("local midnight boundary", midOK)

	_, errBad := api.New("Not/AZone")
	_, errN := svc.Bucket(-1)
	errF := svc.Feed([]api.Event{{EventTime: -1, Key: "x"}})
	errK := svc.Feed([]api.Event{{EventTime: 1768451400, Key: ""}})
	check("three distinct sentinel errors", errBad == api.ErrBadZone && errN == api.ErrNegativeTS &&
		errF == api.ErrNegativeTS && errK == api.ErrEmptyKey &&
		errBad != errN && errN != errK && errBad != errK)

	check("rejected ops leave no trace", svc.Feed(five) == nil && svc.Count("2026-01-14") == 1 &&
		svc.Feed([]api.Event{{EventTime: -1, Key: "y"}, {EventTime: 1768451400, Key: "z"}}) == api.ErrNegativeTS &&
		svc.Count("2026-01-14") == 1)

	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		s2, _ := api.New("America/New_York")
		evs := make([]api.Event, m)
		wantN := 0
		for i := range evs {
			evs[i] = api.Event{EventTime: 1767225600 + int64(i%200)*86400, Key: "k"}
			if i%200 == 2 {
				wantN++
			}
		}
		bigOK = bigOK && s2.Feed(evs) == nil && s2.Count("2026-01-02") == wantN
	}
	check("count correct at m=100..10000", bigOK)

	var bad atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if d, _ := svc.Bucket(five[i%5].EventTime); d != want[i%5] {
					bad.Add(1)
				}
				if svc.Count("2026-01-14") != 1 {
					bad.Add(1)
				}
				if g == 0 && i == 250 {
					_ = svc.Feed([]api.Event{{EventTime: 1773030600, Key: "late"}})
				}
			}
		}(g)
	}
	wg.Wait()
	check("concurrent reads consistent + SelfCheck", bad.Load() == 0 && svc.Count("2026-03-09") == 2 && svc.SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}
