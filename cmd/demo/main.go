package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/ver"
)

type rec struct {
	v      string
	ev, in int64
}

// naiveAt is the reference scan: winner among Ev <= t (or Ev < t if strict),
// ties broken by max In; "" when none qualifies.
func naiveAt(rs []rec, t int64, strict bool) string {
	best, found := rec{}, false
	for _, r := range rs {
		inRange := r.ev < t
		if !strict {
			inRange = r.ev <= t
		}
		if inRange && (!found || r.ev > best.ev || (r.ev == best.ev && r.in > best.in)) {
			best, found = r, true
		}
	}
	return best.v
}

func main() {
	ok := func(name string, cond bool) {
		if !cond {
			fmt.Println("FAIL", name)
			os.Exit(1)
		}
		fmt.Println("OK  ", name)
	}
	s, _ := api.New(10)
	steps := []rec{{"A", 15, 1}, {"B", 25, 2}, {"C", 5, 3}, {"D", 20, 4}}
	wantLE, wantLI := []string{"A", "B", "B", "B"}, []string{"A", "B", "C", "D"}
	stepOK := true
	for i, st := range steps {
		stepOK = stepOK && s.Apply("K", st.v, st.ev, st.in) == nil
		le, e1 := s.LatestEvent("K")
		li, e2 := s.LatestIngest("K")
		stepOK = stepOK && e1 == nil && e2 == nil && le.Value == wantLE[i] && li.Value == wantLI[i]
	}
	le, _ := s.LatestEvent("K")
	li, _ := s.LatestIngest("K")
	ok("step table A,B,C,D; final LatestEvent=B@25 LatestIngest=D@In4",
		stepOK && le.Value == "B" && le.Ev == 25 && li.Value == "D" && li.In == 4)
	wrong, _ := s.LatestIngest("K") // (甲) ingest-latest mistaken for event-latest
	ok("(甲) ingest-as-event returns D, correct is B@25", wrong.Value == "D" && le.Value == "B")
	ae, _ := s.AtEvent("K", 20) // (乙) strict Ev<T drops D itself
	ok("(乙) strict< AtEvent(K,20) returns A, correct is D@20", naiveAt(steps, 20, true) == "A" && ae.Value == "D")
	_ = s.Apply("T", "X", 10, 1)
	_ = s.Apply("T", "Y", 10, 2)
	ty, _ := s.LatestEvent("T")
	ok("(丙) same-Ev first-arrived would return X, correct is Y", ty.Value == "Y")
	s2, _ := api.New(300)
	var got []rec
	seed, mono, consist := int64(3), true, true
	var prevEv int64
	for i := 0; i < 200; i++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		r := rec{fmt.Sprintf("v%d", i), (seed >> 33) % 60, int64(i + 1)}
		consist = consist && s2.Apply("k", r.v, r.ev, r.in) == nil
		got = append(got, r)
		le, _ := s2.LatestEvent("k")
		li, _ := s2.LatestIngest("k")
		ae, _ := s2.AtEvent("k", r.ev)
		ai, _ := s2.AtIngest("k", r.in)
		consist = consist && le.Value == naiveAt(got, 1<<62, false) && ae.Value == naiveAt(got, r.ev, false) &&
			li.Value == r.v && ai.Value == got[i].v
		mono = mono && (i == 0 || le.Ev >= prevEv)
		prevEv = le.Ev
	}
	ok("naive-scan consistency + Ev monotone over 200 shuffled applies", consist && mono)
	_, e0 := api.New(0)
	s3, _ := api.New(1)
	_ = s3.Apply("a", "v", 1, 1)
	b1, _ := s3.LatestEvent("a")
	errs := []error{e0, s3.Apply("", "x", 1, 2), s3.Apply("a", "x", 1, 1), s3.Apply("a", "x", 2, 2)}
	want := []error{api.ErrBadConfig, api.ErrEmptyKey, api.ErrNonMonotonicIngest, api.ErrCapacity}
	distinct := true
	for i, e := range errs {
		for j, w := range want {
			distinct = distinct && errors.Is(e, w) == (i == j)
		}
	}
	a1, _ := s3.LatestEvent("a")
	ok("four distinct sentinel errors; rejections leave no trace, store usable",
		distinct && b1 == a1 && s3.Apply("b", "w", 5, 5) == nil)
	s4, _ := api.New(20000)
	var big []rec
	for m := 1; m <= 10000; m++ {
		seed = seed*6364136223846793005 + 1442695040888963407
		r := rec{fmt.Sprintf("b%d", m), seed >> 33, int64(m)}
		_ = s4.Apply("big", r.v, r.ev, r.in)
		big = append(big, r)
	}
	le4, e4 := s4.LatestEvent("big")
	ok("LatestEvent O(1) at m=10000 (scan count asserted by ver.SelfCheck)",
		e4 == nil && le4.Value == naiveAt(big, 1<<62, false) && ver.SelfCheck() == nil)
	ok("concurrent applies + queries correct", concurrent())
	ok("api.SelfCheck passes", s.SelfCheck() == nil)
}

// concurrent: 8 keys x1 version, one shared key with In=1..32, readers live throughout.
func concurrent() bool {
	s, _ := api.New(64)
	var done atomic.Bool
	var readers, writers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !done.Load() {
				_, _ = s.LatestEvent("shared")
				_, _ = s.AtIngest("k0", 5)
			}
		}()
	}
	for i := 0; i < 8; i++ {
		writers.Add(1)
		go func(i int) { defer writers.Done(); _ = s.Apply(fmt.Sprintf("k%d", i), "v", 1, 1) }(i)
	}
	for in := int64(1); in <= 32; in++ {
		writers.Add(1)
		go func(in int64) {
			defer writers.Done()
			_ = s.Apply("shared", fmt.Sprintf("s%d", in), in, in)
		}(in)
	}
	writers.Wait()
	done.Store(true)
	readers.Wait()
	lsh, err := s.LatestIngest("shared")
	good := err == nil && lsh.In == 32
	for i := 0; i < 8; i++ {
		_, err := s.LatestIngest(fmt.Sprintf("k%d", i))
		good = good && err == nil
	}
	return good
}
