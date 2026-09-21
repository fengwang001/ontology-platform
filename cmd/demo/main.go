// Command demo checks each semantic of the replication subsystem and
// prints one OK/FAIL line per semantic.
package main

import (
	"fmt"
	"sync"

	"ontology/cluster"
	"ontology/quorum"
	"ontology/replica"
)

func report(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	// 1. Committed entries survive any later faults and proposals.
	c1 := cluster.New(5, nil)
	_, ok, _ := c1.Propose(1, "durable")
	for id := 0; id < 3; id++ {
		c1.SetFault(id, cluster.FaultReject, 0)
	}
	_, _, _ = c1.Propose(2, "later")
	kept := ok && c1.Commit() == 1
	for id := 0; id < 5 && kept; id++ {
		s := c1.Snapshot(id)
		kept = len(s) >= 1 && s[0] == "durable"
	}
	report("1 committed-never-lost", kept)

	// 2. Majority thresholds and commit-point rule.
	m := true
	want := []int{1, 2, 2, 3, 3, 4, 4}
	for n := 1; n <= 7; n++ {
		m = m && quorum.Majority(n) == want[n-1]
	}
	m = m && quorum.CommitIndex([]uint64{9, 1, 1}, 3) == 1
	m = m && quorum.CommitIndex([]uint64{7, 7, 2}, 3) == 7
	report("2 majority-threshold", m)

	// 3. No commit without a majority of acks.
	c3 := cluster.New(5, nil)
	for id := 2; id < 5; id++ {
		c3.SetFault(id, cluster.FaultDrop, 0)
	}
	_, cm, _ := c3.Propose(1, "x")
	nc := !cm && c3.Commit() == 0
	for id := 0; id < 5 && nc; id++ {
		nc = len(c3.Snapshot(id)) == 0
	}
	report("3 no-majority-no-commit", nc)

	// 4. Index continuity, term rule, truncate + re-append.
	r := replica.New(0)
	cont := r.Append(replica.Entry{Index: 1, Term: 1, Data: "a"}) == nil
	cont = cont && r.Append(replica.Entry{Index: 3, Term: 1, Data: "g"}) != nil
	cont = cont && r.Append(replica.Entry{Index: 2, Term: 0, Data: "t"}) != nil
	_ = r.Append(replica.Entry{Index: 2, Term: 2, Data: "b"})
	r.Truncate(2)
	cont = cont && r.Match() == 1
	cont = cont && r.Append(replica.Entry{Index: 2, Term: 3, Data: "b2"}) == nil
	e, got := r.Get(2)
	cont = cont && got && e.Data == "b2" && r.Match() == 2
	report("4 index-continuity", cont)

	// 5. Lagging replica is backfilled and ends up identical.
	c5 := cluster.New(3, nil)
	c5.SetFault(2, cluster.FaultLag, 2)
	lag := true
	for i := 0; i < 6 && lag; i++ {
		_, cm, _ = c5.Propose(1, "e")
		lag = cm
	}
	c5.SetFault(2, cluster.FaultNone, 0)
	_, _, _ = c5.Propose(1, "e")
	a, b := c5.Snapshot(0), c5.Snapshot(2)
	lag = lag && len(a) == len(b) && len(a) == 7
	for i := range a {
		lag = lag && a[i] == b[i]
	}
	report("5 lag-catch-up", lag)

	// 6. ReadIndex needs a reachable majority.
	c6 := cluster.New(3, nil)
	_, _, _ = c6.Propose(1, "x")
	ri, err := c6.ReadIndex()
	rd := err == nil && ri >= c6.Commit()
	c6.SetFault(1, cluster.FaultDrop, 0)
	c6.SetFault(2, cluster.FaultReject, 0)
	_, err = c6.ReadIndex()
	report("6 consistent-read", rd && err != nil)

	// 7. Snapshots never fork: shorter is a prefix of longer, even
	// across a conflict -> truncate -> re-replicate cycle.
	r1, r2 := replica.New(1), replica.New(2)
	for i := uint64(1); i <= 4; i++ {
		_ = r1.Append(replica.Entry{Index: i, Term: i, Data: fmt.Sprint("v", i)})
		_ = r2.Append(replica.Entry{Index: i, Term: i, Data: fmt.Sprint("v", i)})
	}
	r2.Truncate(3) // conflict rollback
	_ = r2.Append(replica.Entry{Index: 3, Term: 5, Data: "w3"})
	_ = r2.Append(replica.Entry{Index: 4, Term: 5, Data: "w4"})
	_ = r2.Append(replica.Entry{Index: 5, Term: 5, Data: "w5"})
	prefix := true
	for i := uint64(1); i <= 2 && prefix; i++ {
		x, _ := r1.Get(i)
		y, _ := r2.Get(i)
		prefix = x == y
	}
	c7 := cluster.New(5, nil)
	c7.SetFault(4, cluster.FaultLag, 2)
	for i := 0; i < 5; i++ {
		_, _, _ = c7.Propose(1, "d")
	}
	short, long := c7.Snapshot(4), c7.Snapshot(0)
	for i := range short {
		prefix = prefix && short[i] == long[i]
	}
	report("7 no-fork-prefix", prefix)

	// 8. Concurrent proposals: unique gap-free indices, monotone commit.
	c8 := cluster.New(5, nil)
	var mu sync.Mutex
	seen := map[uint64]bool{}
	dup := false
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			last := uint64(0)
			for i := 0; i < 25; i++ {
				idx, cm, err := c8.Propose(1, "p")
				ci := c8.Commit()
				mu.Lock()
				dup = dup || seen[idx] || !cm || err != nil || ci < last
				seen[idx] = true
				mu.Unlock()
				last = ci
			}
		}()
	}
	wg.Wait()
	conc := !dup && c8.Commit() == 200 && len(seen) == 200
	report("8 concurrent-propose", conc)
}
