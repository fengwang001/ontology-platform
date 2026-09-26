// Command demo exercises the cuckoo hash and prints at most ten OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"ontology/api"
	"ontology/cuckoo"
)

var failed bool

func report(ok bool, msg string) {
	if ok {
		fmt.Println("OK " + msg)
	} else {
		failed = true
		fmt.Println("FAIL " + msg)
	}
}

func main() {
	// 1. Eight stepwise inserts; print the actual T1/T2 after each.
	q, _ := cuckoo.New(4, 8)
	want := [][2][4]int{
		{{0, -1, -1, -1}, {-1, -1, -1, -1}}, {{0, 1, -1, -1}, {-1, -1, -1, -1}},
		{{0, 1, 2, -1}, {-1, -1, -1, -1}}, {{0, 1, 2, 3}, {-1, -1, -1, -1}},
		{{4, 1, 2, 3}, {-1, 0, -1, -1}}, {{4, 5, 2, 3}, {1, 0, -1, -1}},
		{{4, 5, 6, 3}, {1, 0, -1, 2}}, {{4, 5, 6, 7}, {1, 0, 3, 2}}}
	var states []string
	stepOK := true
	for i, x := range []int{0, 1, 2, 3, 4, 5, 6, 7} {
		if err := q.Insert(x); err != nil || q.Dump4() != want[i] {
			stepOK = false
		}
		states = append(states, fmt.Sprintf("%d:%v", x, q.Dump4()))
	}
	report(stepOK, "stepwise T1/T2 "+strings.Join(states, " "))

	// 2. Insert 8 must fail ErrTableFull and leave both tables untouched.
	before := q.Dump4()
	errFull := q.Insert(8)
	report(errors.Is(errFull, cuckoo.ErrTableFull) && q.Dump4() == before,
		"insert 8 -> ErrTableFull, tables unchanged")

	// 3. Lookups: 0..7 present, 8 absent.
	lookOK := true
	for x := 0; x <= 7; x++ {
		ok, err := q.Lookup(x)
		if !ok || err != nil {
			lookOK = false
		}
	}
	if ok, err := q.Lookup(8); ok || !errors.Is(err, cuckoo.ErrNotFound) {
		lookOK = false
	}
	report(lookOK, "lookup keys 0..7 found, key 8 ErrNotFound")

	// 4. Four mutually distinct, decidable sentinel errors.
	_, errN := cuckoo.New(0, 1)
	errs := []error{q.Insert(4), q.Delete(99), errFull, errN}
	wantE := []error{cuckoo.ErrDuplicate, cuckoo.ErrNotFound, cuckoo.ErrTableFull, cuckoo.ErrInvalidN}
	distinct := true
	for i := range errs {
		if !errors.Is(errs[i], wantE[i]) {
			distinct = false
		}
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], wantE[j]) {
				distinct = false
			}
		}
	}
	report(distinct, "four distinct errors dup/notfound/full/invalid-n")

	// 5. Rejected ops leave no trace and the table stays usable.
	u, _ := cuckoo.New(8, 8)
	for _, x := range []int{0, 1} {
		_ = u.Insert(x)
	}
	ub := u.Dump4()
	traceOK := u.Insert(1) != nil && u.Delete(99) != nil && u.Dump4() == ub &&
		u.Insert(2) == nil && u.Len() == 3
	report(traceOK, "rejections leave no trace, table still usable")

	// 6. O(1) probes==2 at m=100/1000/10000 is asserted inside SelfCheck
	// (the unexported counter is never read through a public method).
	probe, _ := cuckoo.New(16, 8)
	report(probe.SelfCheck() == nil, "probes==2 at m=100/1000/10000 (via SelfCheck)")

	// 7. Concurrent read-only lookups agree with the fixed key set.
	big, _ := cuckoo.New(4096, 64)
	for i := 0; i < 500; i++ {
		_ = big.Insert(i)
	}
	var wg sync.WaitGroup
	concOK := true
	var mu sync.Mutex
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < 2000; r++ {
				x := (g*7 + r) % 1000
				ok, err := big.Lookup(x)
				if ok != (x < 500) || ((x < 500) != (err == nil)) {
					mu.Lock()
					concOK = false
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()
	report(concOK, "16 goroutines concurrent read-only lookups consistent")

	// 8. The public api facade delegates correctly.
	a, err := api.New(8, 8)
	if err != nil || a.Insert(1) != nil || a.Insert(1) == nil {
		report(false, "api facade delegates")
	} else if ok, _ := a.Lookup(1); !ok || a.Len() != 1 || a.Delete(1) != nil || a.Len() != 0 || a.SelfCheck() != nil {
		report(false, "api facade delegates")
	} else {
		report(true, "api facade delegates Insert/Lookup/Delete/Len/SelfCheck")
	}

	if failed {
		os.Exit(1)
	}
}
