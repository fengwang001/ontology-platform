package main

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"ontology"
)

func at(n int64) time.Time { return time.Unix(n, 0) }

func sec(t time.Time) string {
	if t.IsZero() {
		return "inf"
	}
	return strconv.FormatInt(t.Unix(), 10)
}

func main() {
	var pass, total int
	check := func(name string, ok bool, detail string) {
		total++
		tag := "OK  "
		if !ok {
			tag = "FAIL"
		} else {
			pass++
		}
		fmt.Printf("%s %-22s %s\n", tag, name, detail)
	}

	// 1. Half-open boundaries: From inclusive, To exclusive.
	s := ontology.NewStore()
	_ = s.Put("car", "color", "v", at(10), at(20), at(1))
	fromHit := s.AsOf("car", "color", at(10), at(5)).Status == ontology.StatusFound
	toMiss := s.AsOf("car", "color", at(20), at(5)).Status == ontology.StatusOutsideValidity
	beforeMiss := s.AsOf("car", "color", at(9), at(5)).Status == ontology.StatusOutsideValidity
	check("boundaries", fromHit && toMiss && beforeMiss, "t=10 hit, t=9/20 miss")

	// 2. Empty vs reversed intervals are distinct error classes.
	errEmpty := errors.Is(s.Put("x", "a", "v", at(5), at(5), at(1)), ontology.ErrEmptyInterval)
	errRev := errors.Is(s.Put("x", "a", "v", at(6), at(5), at(1)), ontology.ErrReversedInterval)
	check("interval errors", errEmpty && errRev, "empty != reversed")

	// 3. Middle overlap splits the old fact into two boundary-exact residuals.
	s2 := ontology.NewStore()
	_ = s2.Put("doc", "body", "old", at(0), at(100), at(1))
	_ = s2.Put("doc", "body", "new", at(30), at(70), at(2))
	cur := s2.CurrentRecords("doc", "body")
	splitOK := len(cur) == 3 &&
		cur[0].Value == "old" && sec(cur[0].Valid.From) == "0" && sec(cur[0].Valid.To) == "30" &&
		cur[2].Value == "old" && sec(cur[2].Valid.From) == "70" && sec(cur[2].Valid.To) == "100"
	check("middle split", splitOK,
		"old["+sec(cur[0].Valid.From)+","+sec(cur[0].Valid.To)+") + new + old["+
			func() string {
				if len(cur) == 3 {
					return sec(cur[2].Valid.From) + "," + sec(cur[2].Valid.To)
				}
				return "?"
			}()+")")

	// 4. Correction trajectory for one validAt across three txAt values.
	s3 := ontology.NewStore()
	_ = s3.Put("p", "lvl", "red", at(0), at(100), at(10))
	_ = s3.Put("p", "lvl", "blue", at(30), at(70), at(20))
	_ = s3.Put("p", "lvl", "green", at(30), at(70), at(30))
	q1 := s3.AsOf("p", "lvl", at(50), at(15)).Value
	q2 := s3.AsOf("p", "lvl", at(50), at(25)).Value
	q3 := s3.AsOf("p", "lvl", at(50), at(35)).Value
	check("trajectory", q1 == "red" && q2 == "blue" && q3 == "green",
		"tx15="+q1+" tx25="+q2+" tx35="+q3)

	// 5. The three distinguishable "not found" cases.
	nf := s3.AsOf("p", "nope", at(50), at(99)).Status == ontology.StatusNoFacts
	out := s3.AsOf("p", "lvl", at(100), at(99)).Status == ontology.StatusOutsideValidity
	nyk := s3.AsOf("p", "lvl", at(50), at(5)).Status == ontology.StatusNotYetKnown
	check("not found x3", nf && out && nyk, "noFacts / outside / notYetKnown")

	// 6. Transaction-time regression is rejected and changes nothing.
	s4 := ontology.NewStore()
	_ = s4.Put("e", "a", "first", at(0), at(10), at(10))
	regressed := errors.Is(s4.Put("e", "a", "late", at(0), at(10), at(9)), ontology.ErrTxNotAdvancing)
	unchanged := s4.AsOf("e", "a", at(5), at(99)).Value == "first"
	check("tx regression", regressed && unchanged, "tx=9 rejected after tx=10")

	// 7. Concurrent writers on one entity produce strictly increasing tx times.
	s5 := ontology.NewStore()
	_ = s5.Put("e", "a", "v0", at(0), at(100), at(1))
	var clock int64 = 1
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 25; k++ {
				for {
					n := atomic.AddInt64(&clock, 1)
					err := s5.Put("e", "a", "v"+strconv.FormatInt(n, 10), at(0), at(100), at(n))
					if err == nil {
						break
					}
					if !errors.Is(err, ontology.ErrTxNotAdvancing) {
						panic(err)
					}
				}
			}
		}()
	}
	wg.Wait()
	tr := s5.Trajectory("e", "a", at(50))
	mono := len(tr) == 201
	for i := 1; mono && i < len(tr); i++ {
		mono = tr[i].Tx.From.After(tr[i-1].Tx.From)
	}
	check("concurrent tx", mono, strconv.Itoa(len(tr))+" versions strictly increasing")

	fmt.Printf("TOTAL %d/%d OK\n", pass, total)
}
