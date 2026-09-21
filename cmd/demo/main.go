// Demo prints an OK/FAIL verdict for each ledger semantic.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/ledger"
	"ontology/shard"
	"ontology/wal"
)

type sink struct{ fail bool }

func (s *sink) Write(p []byte) (int, error) {
	if s.fail {
		return 0, errors.New("boom")
	}
	return len(p), nil
}

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
	}
	fmt.Printf("%-22s %s\n", name, verdict)
}

func fresh(n int, initial int64) (*ledger.Ledger, *wal.Log, *shard.Set, *sink) {
	sk := &sink{}
	l := wal.New(sk)
	s := shard.New(n, initial)
	return ledger.New(l, s), l, s, sk
}

func main() {
	// 1. WAL-first: sink failure leaves shards untouched, burns no Seq.
	g, l, s, sk := fresh(2, 100)
	sk.fail = true
	err1 := g.Transfer(0, 1, 10)
	check("1 wal-first-atomic", err1 != nil && s.Total() == 200 && l.LastSeq() == 0)

	// 2. Total conserved across transfers.
	sk.fail = false
	_ = g.Transfer(0, 1, 40)
	_ = g.Transfer(1, 0, 15)
	check("2 total-conserved", s.Total() == 200)

	// 3. Idempotent replay: second Recover replays 0.
	r1, _ := g.Recover()
	r2, _ := g.Recover()
	check("3 idempotent-replay", r1 == 0 && r2 == 0 && s.Total() == 200)

	// 4. All three crash points recover to the no-crash state.
	ok := true
	for _, p := range []ledger.CrashPoint{
		ledger.CrashAfterAppend, ledger.CrashAfterDebit, ledger.CrashBeforeMeta,
	} {
		g2, _, s2, _ := fresh(2, 100)
		g2.CrashAt(p)
		if err := g2.Transfer(0, 1, 30); err == nil {
			ok = false // crashed transfer must report an error
		}
		g2.Recover()
		ok = ok && s2.Balance(0) == 70 && s2.Balance(1) == 130 && s2.Total() == 200
	}
	check("4 crash-points-recover", ok)

	// 5. Checkpoint truncates applied prefix only; post-truncate crash recovers.
	g3, l3, s3, _ := fresh(2, 100)
	_ = g3.Transfer(0, 1, 10)
	_ = g3.Checkpoint()
	recs, _ := l3.Replay(0)
	g3.CrashAt(ledger.CrashAfterAppend)
	_ = g3.Transfer(0, 1, 20)
	_ = g3.Checkpoint()
	recs2, _ := l3.Replay(0)
	g3.Recover()
	check("5 checkpoint-truncate", len(recs) == 0 && len(recs2) == 2 &&
		s3.Balance(0) == 70 && s3.Total() == 200)

	// 6. Validation: bad args and insufficient funds write nothing.
	g4, l4, s4, _ := fresh(2, 100)
	bad := g4.Transfer(0, 1, 0) == nil || g4.Transfer(1, 1, 5) == nil ||
		g4.Transfer(0, 9, 5) == nil || g4.Transfer(0, 1, 101) == nil
	check("6 validation", !bad && l4.LastSeq() == 0 && s4.Total() == 200)

	// 7+8. Concurrent transfers with Recover in the middle.
	g5, l5, s5, _ := fresh(4, 100000)
	var wg sync.WaitGroup
	for k := 0; k < 16; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = g5.Transfer((k+i)%4, (k+i+1)%4, 1)
			}
		}(k)
	}
	g5.Recover()
	wg.Wait()
	g5.Recover()
	all, _ := l5.Replay(0)
	adjacent := len(all) == 1600
	for i := 1; i < len(all); i += 2 {
		adjacent = adjacent && all[i].Txn == all[i-1].Txn
	}
	check("7 concurrent-conserved", s5.Total() == 400000 && l5.LastSeq() == 1600 && adjacent)
	check("8 recover-concurrent", s5.Total() == 400000)
}
