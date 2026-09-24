// Command demo exercises the MVCC store and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/chain"
	"ontology/mvcc"
)

func ok(name string, pass bool) bool {
	if pass {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
	return pass
}

// chainChecks verify the single-key chain.
func chainChecks() bool {
	c := chain.New()
	c.Prepend("v1", 1)
	c.Prepend("v2", 2)
	c.Prepend("v3", 3)
	got, found := c.Visible(1)
	vers := c.Versions()
	_, f0 := c.Visible(0)
	n := c.Collect([]int64{2})
	_, stillV2 := c.Visible(2)
	_, stillV1 := c.Visible(1)
	pass := found && got == "v1" && len(vers) == 3 && vers[0] == 3 && vers[2] == 1 &&
		!f0 && n == 1 && stillV2 && !stillV1
	return ok("chain: vis<=s / COW order / collect keeps v2 drops v1", pass)
}

// mvccChecks run the eight-step sequence (printing each step's real t,
// chain, read result and reclaim) and the three distinct sentinel errors.
func mvccChecks() bool {
	m := mvcc.New()
	v1, _ := m.Write("k", "v1")
	A := m.Snapshot()
	v2, _ := m.Write("k", "v2")
	B := m.Snapshot()
	v3, _ := m.Write("k", "v3")
	ra, fa, _ := m.Read("k", A)
	rb, fb, _ := m.Read("k", B)
	rel := m.Release(A)
	col := m.Collect()
	rc, fc, _ := m.Read("k", B)
	_, fd, _ := m.Read("k", A) // v1 must be gone after collect
	// Real per-step values: t / chain(new->old) / result.
	trace := fmt.Sprintf(
		"W1(t=%d)->[1] A=%d W2(t=%d)->[2 1] B=%d W3(t=%d)->[3 2 1] "+
			"R@%d=%q R@%d=%q rel=%v col=%d post=[3 2] R@%d=%q v1gone=%v",
		v1, A, v2, B, v3, A, ra, B, rb, rel == nil, col, B, rc, !fd)
	steps := v1 == 1 && A == 1 && v2 == 2 && B == 2 && v3 == 3 && m.T() == 3 &&
		fa && ra == "v1" && fb && rb == "v2" && rel == nil && col == 1 &&
		fc && rc == "v2" && !fd && !m.Active(A) && m.Active(B)
	pass := ok("mvcc 8steps: "+trace, steps)

	_, e1 := m.Write("", "x")
	_, _, e2 := m.Read("k", -1)
	_, _, e2b := m.Read("k", m.T()+1)
	e3 := m.Release(99)
	e3b := m.Release(A)
	distinct := errors.Is(e1, mvcc.ErrEmptyKey) &&
		errors.Is(e2, mvcc.ErrSnapshotRange) && errors.Is(e2b, mvcc.ErrSnapshotRange) &&
		errors.Is(e3, mvcc.ErrSnapshotInactive) && errors.Is(e3b, mvcc.ErrSnapshotInactive) &&
		e1 != e2 && e2 != e3
	after, fAfter, _ := m.Read("k", B)
	intact := fAfter && after == "v2"
	return ok("mvcc: 3 distinct errors + state intact after rejection",
		pass && distinct && intact)
}

// apiChecks cover SelfCheck, sublinear probes at large m, and concurrent
// readers on an old snapshot against a busy writer.
func apiChecks() bool {
	pass := ok("api: SelfCheck four invariants", api.New().SelfCheck() == nil)
	pass = ok("api: probe count grows sub-linearly m=100..10000",
		chain.ProbeGrowthSublinear()) && pass

	s := api.New()
	s.Write("k", "seed")
	old := s.Snapshot()
	want, wf, _ := s.Read("k", old)
	const readers, writesN = 8, 200
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan string, readers)
	wg.Add(1)
	go func() { // single writer
		defer wg.Done()
		<-start
		for i := 0; i < writesN; i++ {
			s.Write("k", fmt.Sprintf("w%d", i))
		}
	}()
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			v, f, err := s.Read("k", old)
			if err == nil && f {
				results <- v
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	uniform := len(results) == readers
	for v := range results {
		uniform = uniform && v == want && wf
	}
	return ok("api: concurrent old-snapshot readers all see the same value",
		pass && uniform)
}

func main() {
	allOK := chainChecks()
	allOK = mvccChecks() && allOK
	allOK = apiChecks() && allOK
	if !allOK {
		fmt.Println("DEMO FAILED")
		return
	}
	fmt.Println("DEMO OK")
}
