package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/fire"
	"ontology/period"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
	} else {
		fmt.Println("OK", name)
	}
}

func main() {
	check("fire rate 锚定/delay 漂移", fire.NextRate(25, 10) == 30 && fire.NextDelay(28, 10) == 38)

	durs := []int64{3, 15, 2, 4}
	rateWant := [][2]int64{{0, 3}, {10, 25}, {30, 32}, {40, 44}}
	delayWant := [][2]int64{{0, 3}, {13, 28}, {38, 40}, {50, 54}}
	rt, dt := period.New(fire.Rate, 10), period.New(fire.Delay, 10)
	rateOK, delayOK := true, true
	for i, d := range durs {
		s, e := rt.Run(d)
		rateOK = rateOK && s == rateWant[i][0] && e == rateWant[i][1]
		s, e = dt.Run(d)
		delayOK = delayOK && s == delayWant[i][0] && e == delayWant[i][1]
	}
	check("第三节 rate 0,10,30,40 / delay 0,13,38,50", rateOK && delayOK)

	s := api.New()
	check("SelfCheck 四不变量", s.SelfCheck() == nil)

	s.AddTask("t", api.Rate, 10)
	s.Run("t", 3)
	errs := []error{
		s.AddTask("", api.Rate, 1), s.AddTask("t", api.Rate, 1),
		s.AddTask("u", api.Mode(9), 1), s.AddTask("v", api.Rate, 0),
	}
	_, _, runErr := s.Run("t", -1)
	errs = append(errs, runErr)
	distinct := true
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == nil || errs[j] == nil || errors.Is(errs[i], errs[j]) {
				distinct = false
			}
		}
	}
	check("五类可判定错误互不相同", distinct && len(errs) == 5)
	st, _, err := s.Run("t", 0)
	check("被拒后状态不变仍可用", err == nil && st == 10)

	bigOK := true
	for _, m := range []int64{100, 1000, 10000} {
		bigOK = bigOK && fire.NextRate(m*10+5, 10) == (m+1)*10
	}
	check("大 m 一次除法定位正确", bigOK)

	conc := api.New()
	var wg sync.WaitGroup
	var concBad atomic.Bool
	for g := 0; g < 8; g++ {
		mode := api.Rate
		if g%2 == 1 {
			mode = api.Delay
		}
		id := fmt.Sprintf("c%d", g)
		conc.AddTask(id, mode, 10)
		wg.Add(1)
		go func() {
			defer wg.Done()
			prevEnd := int64(-1)
			for i := 0; i < 50; i++ {
				st, en, err := conc.Run(id, int64(i%7))
				bad := err != nil || en != st+int64(i%7)
				if mode == api.Rate {
					bad = bad || st%10 != 0
				} else if i > 0 {
					bad = bad || st != prevEnd+10
				}
				if bad {
					concBad.Store(true)
				}
				prevEnd = en
			}
		}()
	}
	wg.Wait()
	check("并发下各自不变量成立", !concBad.Load())

	if failed {
		os.Exit(1)
	}
}
