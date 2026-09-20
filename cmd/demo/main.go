// Command demo exercises every documented semantic of the seqwin
// sliding-window replay detector and prints one OK/FAIL line each.
// It exits 0 only when all checks pass.
package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/seqwin"
)

var failures int

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%-4s %s\n", verdict, name)
}

func main() {
	// 1. Sequence numbers start at 1; 0 is Invalid and changes nothing.
	w := seqwin.New(8)
	check("zero is Invalid, state untouched",
		w.Accept(0) == seqwin.Invalid && w.Highest() == 0 && !w.Seen(0))

	// 2. Window slides right; evicted numbers become TooOld.
	w = seqwin.New(2)
	ok := w.Accept(1) == seqwin.Fresh && w.Accept(2) == seqwin.Fresh &&
		w.Accept(3) == seqwin.Fresh
	check("window slides right, evicted is TooOld",
		ok && w.Accept(1) == seqwin.TooOld && w.Highest() == 3)

	// 3. Out-of-order but in-window: Fresh once, then Duplicate.
	w = seqwin.New(8)
	w.Accept(10)
	check("out-of-order in-window Fresh then Duplicate",
		w.Accept(7) == seqwin.Fresh && w.Accept(7) == seqwin.Duplicate)

	// 4. Exact boundary: size=8, Highest=100 -> window is [93,100].
	w = seqwin.New(8)
	w.Accept(100)
	check("boundary exact: 93 in-window, 92 TooOld",
		w.Accept(93) == seqwin.Fresh && w.Accept(92) == seqwin.TooOld)

	// 5. Huge jump clears stale state but keeps the new edge.
	w = seqwin.New(8)
	w.Accept(10)
	w.Accept(10000)
	check("big jump: 9999 Fresh, 10 TooOld, 10000 kept",
		w.Accept(9999) == seqwin.Fresh && w.Accept(10) == seqwin.TooOld &&
			w.Seen(10000))

	// 6. Re-accepting Highest is Duplicate and does not move the edge.
	w = seqwin.New(8)
	w.Accept(5)
	check("duplicate Highest, edge unchanged",
		w.Accept(5) == seqwin.Duplicate && w.Highest() == 5)

	// 7. Memory stays proportional to size, not to packets seen.
	w = seqwin.New(64)
	for seq := uint64(1); seq <= 100000; seq++ {
		w.Accept(seq)
	}
	check("100k packets, verdicts still correct (O(size) bitmap)",
		w.Highest() == 100000 && w.Accept(100000) == seqwin.Duplicate &&
			w.Accept(99937) == seqwin.Duplicate &&
			w.Accept(99936) == seqwin.TooOld)

	// 8. Concurrent Accept: each seq Fresh exactly once overall.
	w = seqwin.New(4096)
	const maxSeq = 4096
	fresh := make([]atomic.Int64, maxSeq+1)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for seq := uint64(1); seq <= maxSeq; seq++ {
				if w.Accept(seq) == seqwin.Fresh {
					fresh[seq].Add(1)
				}
			}
		}()
	}
	wg.Wait()
	ok = w.Highest() == maxSeq
	for seq := uint64(1); seq <= maxSeq && ok; seq++ {
		ok = fresh[seq].Load() == 1
	}
	check("concurrent: each seq Fresh exactly once", ok)

	if failures > 0 {
		fmt.Printf("%d check(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("all checks passed")
}
