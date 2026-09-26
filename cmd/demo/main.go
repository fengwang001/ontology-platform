// Command demo exercises the priority-inheritance mutex end to end.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/lock"
)

var failed bool

func report(name string, err error) {
	if err != nil {
		failed = true
		fmt.Printf("FAIL %s: %v\n", name, err)
	} else {
		fmt.Printf("OK %s\n", name)
	}
}

// checkLock: three distinct sentinel errors; rejects leave no trace.
func checkLock() error {
	l := lock.New()
	if err := l.Acquire(1, 5); err != nil {
		return err
	}
	if err := l.Acquire(2, 7); err != nil {
		return err
	}
	bh, _ := l.Holder()
	be := l.Effective() // snapshot before the rejected calls
	if !errors.Is(l.Acquire(3, 0), lock.ErrInvalidPriority) {
		return errors.New("prio<=0 not ErrInvalidPriority")
	}
	if !errors.Is(l.Acquire(2, 3), lock.ErrDuplicateAcquire) {
		return errors.New("dup acquire not ErrDuplicateAcquire")
	}
	if !errors.Is(l.Release(9), lock.ErrNotHolder) {
		return errors.New("release non-holder not ErrNotHolder")
	}
	if h, _ := l.Holder(); h != bh || l.Effective() != be {
		return errors.New("rejected calls changed state")
	}
	if err := l.Release(1); err != nil { // still usable afterwards
		return err
	}
	if h, _ := l.Holder(); h != 2 {
		return fmt.Errorf("after rejects holder=%d want 2", h)
	}
	return nil
}

// checkEightSteps replays the NOTES.md derivation (A=1,B=2,C=3,D=4):
// step 3 inherits to 9, step 4 hands off to C, not the FIFO head B.
func checkEightSteps() error {
	m := api.New()
	type step struct {
		acq                    bool
		id, prio, wantH, wantE int
	}
	steps := []step{
		{true, 1, 1, 1, 1}, {true, 2, 2, 1, 2}, {true, 3, 9, 1, 9},
		{false, 1, 0, 3, 9}, {true, 4, 5, 3, 9}, {false, 3, 0, 4, 5},
		{false, 4, 0, 2, 2}, {false, 2, 0, 0, 0},
	}
	for i, s := range steps {
		var err error
		if s.acq {
			err = m.Acquire(s.id, s.prio)
		} else {
			err = m.Release(s.id)
		}
		if err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		h, has := m.Holder()
		if !has {
			h = 0
		}
		if h != s.wantH || m.Effective() != s.wantE {
			return fmt.Errorf("step %d: holder=%d eff=%d want %d/%d",
				i+1, h, m.Effective(), s.wantH, s.wantE)
		}
	}
	return nil
}

// checkLargeM: one Release hands to the highest static among m waiters
// (heap-located; the comparison-count bound is pip's unit test).
func checkLargeM() error {
	for _, n := range []int{100, 1000, 10000} {
		m := api.New()
		_ = m.Acquire(0, 1)
		for i := 1; i <= n; i++ {
			_ = m.Acquire(i, i+1)
		}
		_ = m.Release(0)
		if h, _ := m.Holder(); h != n {
			return fmt.Errorf("m=%d: holder=%d want %d", n, h, n)
		}
	}
	return nil
}

// checkConcurrent: N goroutines acquire distinct ids on one mutex.
func checkConcurrent() error {
	const n = 256
	m := api.New()
	var wg sync.WaitGroup
	start, errs := make(chan struct{}), make(chan error, n)
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			if err := m.Acquire(id, id); err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	if _, ok := m.Holder(); !ok {
		return errors.New("no holder after concurrent acquire")
	}
	if got := m.Effective(); got != n {
		return fmt.Errorf("effective=%d want %d", got, n)
	}
	return nil
}

func main() {
	report("lock errors+no-trace", checkLock())
	report("eight-step holders/effective", checkEightSteps())
	report("selfcheck invariants 1-4", api.New().SelfCheck())
	report("large-m handoff", checkLargeM())
	report("concurrent acquire", checkConcurrent())
	if failed {
		os.Exit(1)
	}
}
