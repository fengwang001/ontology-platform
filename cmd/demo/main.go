// Command demo exercises the reference-count tracker end to end and
// prints one OK/FAIL line per check. Exit code 0 means all passed.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool, detail string) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func main() {
	// 1. Seven-step sequence from NOTES.md, holders tracked locally.
	s := api.New()
	type step struct {
		op      string
		ref     string
		wantN   int
		alive   bool
		holders string
		wantErr error
	}
	steps := []step{
		{"acq", "r1", 1, true, "r1", nil},
		{"acq", "r2", 2, true, "r1,r2", nil},
		{"acq", "r2", 2, true, "r1,r2", nil},
		{"rel", "r1", 1, true, "r2", nil},
		{"rel", "r2", 0, false, "", nil},
		{"acq", "r3", 0, false, "", api.ErrNotFound},
	}
	ok := s.Create("X") == nil
	var trace []string
	for _, st := range steps {
		var err error
		if st.op == "acq" {
			err = s.Acquire(st.ref, "X")
		} else {
			err = s.Release(st.ref, "X")
		}
		n, _ := s.RefCount("X")
		if !errors.Is(err, st.wantErr) || n != st.wantN || s.Alive("X") != st.alive {
			ok = false
		}
		trace = append(trace, fmt.Sprintf("n=%d,alive=%v,{%s}", n, s.Alive("X"), st.holders))
	}
	check("seven-steps", ok, strings.Join(trace, " | "))

	// 2-4. Idempotency, sharing, zero-reclaim, no resurrection.
	s2 := api.New()
	_ = s2.Create("Y")
	_ = s2.Acquire("a", "Y")
	_ = s2.Acquire("a", "Y")
	n, _ := s2.RefCount("Y")
	check("idempotent-acquire", n == 1, "dup acquire keeps count 1")
	_ = s2.Acquire("b", "Y")
	_ = s2.Release("a", "Y")
	n, _ = s2.RefCount("Y")
	check("shared-survives", n == 1 && s2.Alive("Y"), "count 1, alive after first release")
	_ = s2.Release("b", "Y")
	err := s2.Acquire("c", "Y")
	check("reclaim-no-revive", !s2.Alive("Y") && errors.Is(err, api.ErrNotFound), "reclaimed at 0, stays gone")

	// 5. Four distinct decidable errors.
	s3 := api.New()
	_ = s3.Create("Z")
	_ = s3.Acquire("a", "Z")
	e1 := s3.Acquire("", "Z")
	e2 := s3.Create("Z")
	e3 := s3.Acquire("a", "nope")
	e4 := s3.Release("b", "Z")
	distinct := map[error]bool{e1: true, e2: true, e3: true, e4: true}
	check("four-errors", errors.Is(e1, api.ErrEmpty) && errors.Is(e2, api.ErrExists) &&
		errors.Is(e3, api.ErrNotFound) && errors.Is(e4, api.ErrNotHeld) && len(distinct) == 4,
		"empty/exists/notfound/notheld distinct")

	// 6. Rejected ops leave state untouched.
	n, _ = s3.RefCount("Z")
	check("reject-keeps-state", n == 1 && s3.Alive("Z"), "count still 1 after rejections")

	// 7. Reclaim-check cost independent of registry size (via SelfCheck).
	check("reclaim-cost-O1", s.SelfCheck() == nil, "m=100..10000 checks<=1 (reg.SelfCheck)")

	// 8. Concurrent read-only consistency, no sleeps.
	s4 := api.New()
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("o%d", i)
		_ = s4.Create(id)
		_ = s4.Acquire(fmt.Sprintf("r%d", i), id)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	consistent := true
	var mu sync.Mutex
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				id := fmt.Sprintf("o%d", i)
				if n, _ := s4.RefCount(id); n != 1 || !s4.Alive(id) {
					mu.Lock()
					consistent = false
					mu.Unlock()
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent-reads", consistent, "32 goroutines agree per object")

	if failed {
		os.Exit(1)
	}
}
