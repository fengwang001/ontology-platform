// Command demo verifies checkpoint-consistent log truncation and prints one
// OK/FAIL line per property. It takes no arguments and never touches network.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/trunc"
)

type w struct{ cp, tm, f, lo, hi int64 }

func dash(v int64) string {
	if v < 0 {
		return "-"
	}
	return fmt.Sprintf("%d", v)
}

func rng(lo, hi int64) string {
	if lo < 0 {
		return "[]"
	}
	return fmt.Sprintf("[%d,%d]", lo, hi)
}

func main() {
	s := api.New()
	want := []w{
		{-1, 0, 0, 0, 0}, {-1, 0, 0, 0, 1}, {-1, 0, 0, 0, 2},
		{2, 0, 0, 0, 2}, {2, 2, 2, 2, 2}, {2, 2, 2, 2, 3},
		{3, 2, 2, 2, 3}, {3, 3, 3, 3, 3},
	}
	ops := []func() error{
		func() error { _, e := s.Append("a"); return e },
		func() error { _, e := s.Append("b"); return e },
		func() error { _, e := s.Append("c"); return e },
		func() error { return s.Checkpoint(2) },
		func() error { return s.Truncate(2) },
		func() error { _, e := s.Append("d"); return e },
		func() error { return s.Checkpoint(3) },
		func() error { return s.Recover(3, 2) }, // T(3)+CR: restart reads (tm,f)=(3,2)
	}
	ok8 := true
	for i, op := range ops {
		if err := op(); err != nil {
			ok8 = false
		}
		g := w{cp: -1, lo: -1, hi: -1, f: int64(s.First())}
		if v, ok := s.CP(); ok {
			g.cp = int64(v)
		}
		if v, ok := s.Marker(); ok {
			g.tm = int64(v)
		}
		if es := s.Read(0); len(es) > 0 {
			g.lo, g.hi = int64(es[0].Offset), int64(es[len(es)-1].Offset)
		}
		if g != want[i] {
			ok8 = false
		}
		fmt.Printf("%s step %d: cp=%s tm=%d f=%d read=%s\n",
			tag(g == want[i]), i+1, dash(g.cp), g.tm, g.f, rng(g.lo, g.hi))
	}
	// (甲) T(3) at cp=2 must be refused; three distinct sentinels; rejected
	// calls leave no trace; SelfCheck (naive reference + tm==f) passes.
	refT := errors.Is(s.Truncate(99), api.ErrTruncateBeyondCheckpoint)
	refC := errors.Is(s.Checkpoint(0), api.ErrCheckpointInvalid) // 0 < cp: retreat
	before := s.First()
	refR := errors.Is(s.Recover(3, 4), api.ErrRecoverOverDeletion)
	noTrace := s.First() == before
	distinct := api.ErrCheckpointInvalid != api.ErrTruncateBeyondCheckpoint &&
		api.ErrTruncateBeyondCheckpoint != api.ErrRecoverOverDeletion
	self := s.SelfCheck() == nil
	l9 := ok8 && refT && refC && refR && noTrace && distinct && self
	fmt.Printf("%s crash converges f==tm; K<=cp guard; 3 distinct errors; rejected=no-trace; naive ref (SelfCheck)\n", tag(l9))
	// Constant metadata reads at large m, and concurrent readers only ever see
	// a contiguous suffix (marker + physical delete are one atomic step).
	cost := trunc.VerifyCheckpointCostConstant(100, 1000, 10000) == nil
	fmt.Printf("%s K<=cp meta reads constant for m=100..10000; concurrent reads always coherent\n", tag(cost && concurrentCoherent()))
}

func tag(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

func concurrentCoherent() bool {
	s := api.New()
	var stop sync.WaitGroup
	var wg sync.WaitGroup
	bad := false
	var mu sync.Mutex
	writerDone := make(chan struct{})
	wg.Add(1)
	go func() { // one writer: append, checkpoint, truncate in lockstep
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			off, _ := s.Append("x")
			if off > 0 && off%4 == 0 {
				if err := s.Checkpoint(off - 1); err == nil {
					_ = s.Truncate(off - 1)
				}
			}
		}
		close(writerDone)
	}()
	for r := 0; r < 4; r++ { // N readers; no sleeps, run until writer finishes
		stop.Add(1)
		go func() {
			defer stop.Done()
			for {
				select {
				case <-writerDone:
					return
				default:
					es := s.Read(0)
					for j := range es { // every view must be a dense contiguous suffix
						if es[j].Offset != es[0].Offset+uint64(j) {
							mu.Lock()
							bad = true
							mu.Unlock()
							return
						}
					}
				}
			}
		}()
	}
	wg.Wait()
	stop.Wait()
	return !bad
}
