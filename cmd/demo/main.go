// Command demo checks every semantic guarantee of the replication
// subsystem and prints one OK/FAIL line per guarantee.
package main

import (
	"fmt"
	"reflect"
	"sync"
	"time"

	"ontology/cluster"
	"ontology/quorum"
	"ontology/replica"
)

var fails int

func check(name string, ok bool) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		fails++
	}
	fmt.Printf("[%s] %s\n", verdict, name)
}

func main() {
	now := func() time.Time { return time.Unix(1700000000, 0) }

	// 1. Committed entries survive later faults and proposals.
	c1 := cluster.New(3, now)
	_, ok, _ := c1.Propose(1, "durable")
	c1.SetFault(1, cluster.FaultDrop, 0)
	c1.SetFault(2, cluster.FaultReject, 0)
	c1.Propose(2, "stuck")
	s1 := ok && c1.Commit() == 1 && reflect.DeepEqual(c1.Snapshot(0), []string{"durable"})
	check("1 committed entry never lost", s1)

	// 2. Majority thresholds and commit point.
	m := true
	for n, want := range []int{0, 1, 2, 2, 3, 3, 4, 4} {
		m = m && quorum.Majority(n) == want
	}
	m = m && quorum.CommitIndex([]uint64{9, 2, 2}, 3) == 2
	check("2 majority thresholds and commit index", m)

	// 3. No majority, no commit.
	c3 := cluster.New(5, now)
	for _, id := range []int{2, 3, 4} {
		c3.SetFault(id, cluster.FaultDrop, 0)
	}
	_, ok3, _ := c3.Propose(1, "x")
	empty := true
	for id := 0; id < 5; id++ {
		empty = empty && len(c3.Snapshot(id)) == 0
	}
	check("3 no commit without majority", !ok3 && c3.Commit() == 0 && empty)

	// 4. Index contiguity, truncate, match consistency.
	r := replica.New(0)
	bad := r.Append(replica.Entry{Index: 2, Term: 1, Data: "gap"})
	r.Append(replica.Entry{Index: 1, Term: 1, Data: "a"})
	r.Append(replica.Entry{Index: 2, Term: 1, Data: "b"})
	r.Truncate(2)
	err4 := r.Append(replica.Entry{Index: 2, Term: 2, Data: "b2"})
	_, gone := r.Get(3)
	check("4 contiguous append and truncate", bad != nil && err4 == nil && r.Match() == 2 && !gone)

	// 5. Lagging replica catches up, identical snapshots.
	c5 := cluster.New(3, now)
	c5.SetFault(2, cluster.FaultLag, 2)
	for i := 0; i < 4; i++ {
		c5.Propose(1, "e")
	}
	c5.SetFault(2, cluster.FaultNone, 0)
	c5.Propose(1, "e")
	eq := reflect.DeepEqual(c5.Snapshot(0), c5.Snapshot(2)) && len(c5.Snapshot(2)) == 5
	check("5 lagging replica catches up", eq)

	// 6. Consistent read requires a reachable majority.
	ri, err6 := c5.ReadIndex()
	c5.SetFault(0, cluster.FaultDrop, 0)
	c5.SetFault(1, cluster.FaultReject, 0)
	_, err6b := c5.ReadIndex()
	check("6 read index needs quorum", err6 == nil && ri >= 5 && err6b != nil)

	// 7. No forks: conflict, truncate, re-replicate, prefix property.
	a, b := replica.New(0), replica.New(1)
	for i := uint64(1); i <= 4; i++ {
		a.Append(replica.Entry{Index: i, Term: 1, Data: "a"})
	}
	for i := uint64(1); i <= 3; i++ {
		b.Append(replica.Entry{Index: i, Term: 1, Data: "a"})
	}
	b.Append(replica.Entry{Index: 4, Term: 2, Data: "fork"})
	b.Truncate(4)
	a4, _ := a.Get(4)
	b.Append(a4)
	same := true
	for i := uint64(1); i <= 4; i++ {
		ea, _ := a.Get(i)
		eb, _ := b.Get(i)
		same = same && ea == eb
	}
	check("7 no fork after truncate and re-replication", same)

	// 8. Concurrent proposes: dense indexes, monotonic commit.
	c8 := cluster.New(5, now)
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[uint64]bool{}
	dense, mono := true, true
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var last uint64
			for i := 0; i < 20; i++ {
				idx, ok, _ := c8.Propose(1, "p")
				cm := c8.Commit()
				mu.Lock()
				dense = dense && ok && !seen[idx]
				seen[idx] = true
				mono = mono && cm >= last
				last = cm
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	for i := uint64(1); i <= 160; i++ {
		dense = dense && seen[i]
	}
	check("8 concurrent propose is dense and monotonic", dense && mono && c8.Commit() == 160)

	fmt.Printf("summary: %d check(s) failed\n", fails)
}
