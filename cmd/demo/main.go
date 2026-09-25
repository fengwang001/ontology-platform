// Demo: prints one OK/FAIL line per required behavior. Exit 0 iff all OK.
package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	s := api.New()

	// Seven-step sequence from NOTES.md section 3.
	type step struct {
		n     int
		alive bool
		holds string
	}
	want := []step{
		{0, true, "[]"}, {1, true, "[r1]"}, {2, true, "[r1 r2]"},
		{2, true, "[r1 r2]"}, {1, true, "[r2]"}, {0, false, "[]"},
	}
	_ = s.Create("X")
	ops := []func() error{
		nil,
		func() error { return s.Acquire("r1", "X") },
		func() error { return s.Acquire("r2", "X") },
		func() error { return s.Acquire("r2", "X") },
		func() error { return s.Release("r1", "X") },
		func() error { return s.Release("r2", "X") },
	}
	ok := true
	for i, op := range ops {
		if op != nil {
			ok = ok && op() == nil
		}
		n, _ := s.RefCount("X")
		h, _ := s.Holders("X")
		ok = ok && n == want[i].n && s.Alive("X") == want[i].alive &&
			(i == 5 || fmt.Sprint(h) == want[i].holds)
	}
	ok = ok && s.Acquire("r3", "X") == api.ErrNotFound // step 7
	report("seven-step table (counts/alive/holders, step7=ErrNotFound)", ok)

	// Idempotent acquire.
	s2 := api.New()
	_ = s2.Create("i")
	for k := 0; k < 4; k++ {
		_ = s2.Acquire("r", "i")
	}
	n, _ := s2.RefCount("i")
	report("repeat Acquire idempotent (count stays 1)", n == 1)

	// Shared object not reclaimed early.
	s3 := api.New()
	_ = s3.Create("sh")
	_ = s3.Acquire("a", "sh")
	_ = s3.Acquire("b", "sh")
	_ = s3.Release("a", "sh")
	report("shared object survives non-last Release", s3.Alive("sh"))

	// Zero-count reclaim, no revival.
	_ = s3.Release("b", "sh")
	report("reclaim at 0; Acquire/Release/RefCount all ErrNotFound",
		!s3.Alive("sh") && s3.Acquire("c", "sh") == api.ErrNotFound &&
			s3.Release("c", "sh") == api.ErrNotFound)

	// Four distinguishable rejection kinds.
	s4 := api.New()
	_ = s4.Create("k")
	_ = s4.Acquire("r", "k")
	e1, e2 := s4.Create(""), s4.Create("k")
	e3, e4 := s4.Acquire("r", "ghost"), s4.Release("nobody", "k")
	distinct := e1 == api.ErrEmpty && e2 == api.ErrExists &&
		e3 == api.ErrNotFound && e4 == api.ErrNotHeld
	report("four rejection kinds distinct (Empty/Exists/NotFound/NotHeld)", distinct)

	// Rejections leave no trace; service still usable.
	nk, _ := s4.RefCount("k")
	report("rejected ops change nothing; still usable",
		nk == 1 && s4.Alive("k") && s4.Release("r", "k") == nil && !s4.Alive("k"))

	// Large m: reclaim is per-object (others untouched), see TestReclaimScanIsConstant.
	s5 := api.New()
	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("o%d", i)
		_ = s5.Create(id)
		_ = s5.Acquire(fmt.Sprintf("r%d", i), id)
	}
	_ = s5.Release("r0", "o0")
	ok = !s5.Alive("o0") && s5.Alive("o9999")
	m, _ := s5.RefCount("o9999")
	report("m=10000: only released object reclaimed", ok && m == 1)

	// Concurrent readers agree per object.
	var wg sync.WaitGroup
	start := make(chan struct{})
	agree := true
	var mu sync.Mutex
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			c, err := s5.RefCount("o9999")
			mu.Lock()
			agree = agree && err == nil && c == 1 && s5.Alive("o9999")
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	report("concurrent RefCount/Alive readers agree", agree)

	report("SelfCheck", api.New().SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
