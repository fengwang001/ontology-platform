// Demo exercises the hierarchical timing-wheel scheduler end to end and
// prints one OK/FAIL line per check. It takes no arguments, uses no
// network, and exits 0 when every check passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"ontology/scheduler"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%-42s %s\n", name, status)
}

func newRec() (*scheduler.Scheduler, *[]uint64) {
	ids := &[]uint64{}
	var s *scheduler.Scheduler
	cfg := scheduler.Config{OnFire: func(ev scheduler.Event) { *ids = append(*ids, ev.ID) }}
	s, _ = scheduler.New(0, cfg)
	return s, ids
}

// touched reads the scheduler's unexported per-Advance counters; they are
// deliberately not part of the public API, so the demo uses reflection.
func touched(s *scheduler.Scheduler) (int64, int64) {
	get := func(name string) int64 {
		f := reflect.ValueOf(s).Elem().FieldByName(name)
		return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Int()
	}
	return get("touchedSlots"), get("touchedTimers")
}

func main() {
	// 1. delay 0 fires on the first Advance, not at Add.
	s, ids := newRec()
	s.Add(0, nil)
	notYet := len(*ids) == 0
	s.Advance(1)
	check("delay-0 fires on first Advance, not at Add", notYet && len(*ids) == 1)

	// 2. never fires early.
	s, ids = newRec()
	s.Add(5, nil)
	s.Advance(4)
	early := len(*ids)
	s.Advance(1)
	check("no early fire; fires exactly at delay", early == 0 && len(*ids) == 1)

	// 3. Advance(100) equals 100x Advance(1).
	build := func(chunk int64) []uint64 {
		sc, got := newRec()
		for i := 0; i < 50; i++ {
			sc.Add(int64(i*7%100)+1, nil)
		}
		for left := int64(100); left > 0; left -= chunk {
			sc.Advance(chunk)
		}
		return *got
	}
	a, b := build(100), build(1)
	same := len(a) == len(b)
	for i := range a {
		same = same && i < len(b) && a[i] == b[i]
	}
	check("Advance(100) == 100x Advance(1) sequence", same)

	// 4. same-tick firing order follows Add order across levels.
	s, ids = newRec()
	first, _ := s.Add(10, nil)
	s.Advance(5)
	second, _ := s.Add(5, nil)
	s.Advance(5)
	check("same-tick order = Add order (level-independent)",
		len(*ids) == 2 && (*ids)[0] == first && (*ids)[1] == second)

	// 5. cancel inside the firing window suppresses the fire.
	var sib uint64
	cfg := scheduler.Config{}
	cfg.OnFire = func(ev scheduler.Event) {
		if ev.ID != sib {
			s.Cancel(sib)
		}
	}
	s, _ = scheduler.New(0, cfg)
	s.Add(2, nil)
	sib, _ = s.Add(2, nil)
	n, _ := s.Advance(2)
	check("cancelled mid-batch timer does not fire", n == 1)

	// 6. handle idempotency results are distinct and decidable.
	s, _ = newRec()
	id, _ := s.Add(1, nil)
	s.Advance(1)
	errFired := s.Cancel(id)
	id2, _ := s.Add(5, nil)
	s.Cancel(id2)
	errAgain := s.Cancel(id2)
	errReset := s.Reset(id2, 3)
	check("idempotent results distinct (fired/cancelled/reset)",
		errors.Is(errFired, scheduler.ErrTimerFired) &&
			errors.Is(errAgain, scheduler.ErrTimerCancelled) &&
			errors.Is(errReset, scheduler.ErrTimerCancelled) &&
			!errors.Is(errFired, scheduler.ErrTimerCancelled))

	// 7. three limits reject, then the scheduler keeps working.
	s, _ = newRec()
	_, e1 := s.Add(1<<40, nil)
	s2, _ := scheduler.New(0, scheduler.Config{MaxAdvance: 5, MaxTimers: 1})
	_, e2 := s2.Advance(6)
	s2.Add(1, nil)
	_, e3 := s2.Add(1, nil)
	s2.Advance(1) // fires the live timer, freeing the only slot
	_, err := s2.Add(1, nil)
	works := err == nil
	if _, err := s.Advance(1); err != nil {
		works = false
	}
	check("max delay/advance/timers rejected, still usable",
		errors.Is(e1, scheduler.ErrDelayTooLarge) &&
			errors.Is(e2, scheduler.ErrAdvanceTooLarge) &&
			errors.Is(e3, scheduler.ErrTooManyTimers) && works)

	// 8. concurrent Add/Cancel/Advance leaves a consistent scheduler.
	s, _ = newRec()
	var stop atomic.Bool
	var advWG sync.WaitGroup
	advWG.Add(1)
	go func() {
		defer advWG.Done()
		for !stop.Load() {
			s.Advance(1)
		}
	}()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tid, err := s.Add(int64(i%40)+1, nil)
				if err == nil && i%2 == 0 {
					s.Cancel(tid)
				}
			}
		}(w)
	}
	wg.Wait()
	stop.Store(true)
	advWG.Wait()
	s.Advance(100)
	check("concurrent add/cancel/advance, SelfCheck clean", s.SelfCheck() == nil)

	// 9. touched counters do not grow with N.
	var tiers [2][2]int64
	for i, n := range []int{1000, 100000} {
		s, _ = newRec()
		for j := 0; j < n; j++ {
			s.Add(1000000, nil)
		}
		s.Advance(1)
		tiers[i][0], tiers[i][1] = touched(s)
	}
	fmt.Printf("touched N=1000: %v, N=100000: %v\n", tiers[0], tiers[1])
	check("touched slots/timers independent of N", tiers[0] == tiers[1])

	if failed {
		os.Exit(1)
	}
}
