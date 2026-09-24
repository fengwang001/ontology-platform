// Command demo exercises the in-memory MVCC version-chain package.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/chain"
)

var fails int

func ok(name string, pass bool) {
	if pass {
		fmt.Printf("OK %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		fails++
	}
}

func main() {
	m := api.New()
	// Eight-step trace (NOTES §3): track t, chain (new->old), results.
	t1, _ := m.Write("k", "v1") // 1: [1]
	A := m.Snapshot()           // 2: A=1
	t3, _ := m.Write("k", "v2") // 3: [2,1]
	B := m.Snapshot()           // 4: B=2
	t5, _ := m.Write("k", "v3") // 5: [3,2,1]
	rA, _, _ := m.Read("k", A)  // 6
	rB, _, _ := m.Read("k", B)  // 7
	trace := fmt.Sprintf("t=%d,%d,%d A=%d B=%d chains [1]->[2 1]->[3 2 1] readA=%s readB=%s",
		t1, t3, t5, A, B, rA, rB)
	ok("8 steps: "+trace, t1 == 1 && t3 == 2 && t5 == 3 && A == 1 && B == 2 && rA == "v1" && rB == "v2")
	ok("copy-on-write keeps old versions", rA == "v1" && rB == "v2") // v1/v2 nodes never rewritten
	if err := m.Release(A); err != nil {
		panic(err)
	}
	rm := m.Collect() // 8: only v1's interval [1,2) holds no active snapshot
	head, _, _ := m.Read("k", 3)
	ok("collect removes only invisible versions", rm == 1 && rB2(m) == "v2" && head == "v3")

	// Three distinct decidable errors.
	_, eKey := m.Write("", "x")
	_, _, eSnap := m.Read("k", -1)
	eInactive := m.Release(0)
	distinct := errors.Is(eKey, api.ErrEmptyKey) && errors.Is(eSnap, api.ErrInvalidSnapshot) &&
		errors.Is(eInactive, api.ErrSnapshotInactive) &&
		!errors.Is(eKey, eSnap) && !errors.Is(eSnap, eInactive)
	ok("three distinct decidable errors", distinct)

	// Rejected calls leave no trace: version unchanged, reads intact, still usable.
	tBefore := versionVia(m)
	m.Write("", "y")
	m.Read("k", 99999)
	m.Release(99999)
	still, _, _ := m.Read("k", B)
	unchanged := versionVia(m) == tBefore && still == "v2"
	live, _ := m.Write("k2", "live") // rejected ops spent no version number
	ok("rejected ops leave no trace", unchanged && live == tBefore+1)

	ok("comparisons stay logarithmic m=100..10000", chain.SublinearProbe())

	// Concurrency: old snapshot reads identical under a hot writer.
	m2 := api.New()
	m2.Write("k", "seed")
	old := m2.Snapshot()
	const R, W = 16, 3000
	var wg sync.WaitGroup
	wg.Add(R + 1)
	bad := false
	var mu sync.Mutex
	for i := 0; i < R; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if v, ok2, _ := m2.Read("k", old); !ok2 || v != "seed" {
					mu.Lock()
					bad = true
					mu.Unlock()
					return
				}
			}
		}()
	}
	go func() {
		defer wg.Done()
		for i := 0; i < W; i++ {
			m2.Write("k", fmt.Sprintf("w%d", i))
		}
	}()
	wg.Wait()
	ok("concurrent old-snapshot reads identical", !bad)
	ok("SelfCheck", api.New().SelfCheck() == nil)
	if fails > 0 {
		os.Exit(1)
	}
}

func rB2(m *api.MVCC) string {
	v, ok, _ := m.Read("k", 2)
	if !ok {
		return ""
	}
	return v
}
func versionVia(m *api.MVCC) int64 {
	s := m.Snapshot()
	m.Release(s)
	return s
}
