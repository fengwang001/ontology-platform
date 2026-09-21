package ledger

import (
	"sync"
	"testing"
)

// TestCrashPointsRecoverFully covers semantics 3, 4 and 4b: every crash
// point must be recoverable to exactly the no-crash state, the crashed
// Transfer must return an error, and a second Recover must replay 0.
func TestCrashPointsRecoverFully(t *testing.T) {
	cases := []struct {
		name         string
		point        CrashPoint
		wantReplayed int
	}{
		{"after append", CrashAfterAppend, 2},
		{"after debit", CrashAfterDebit, 1},
		{"before meta", CrashBeforeMeta, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, _, s, _ := newLedger(2, 100)
			g.CrashAt(c.point)
			if err := g.Transfer(0, 1, 30); err == nil {
				t.Fatal("crashed Transfer must return an error")
			}
			replayed, err := g.Recover()
			if err != nil {
				t.Fatal(err)
			}
			if replayed != c.wantReplayed {
				t.Fatalf("replayed = %d, want %d", replayed, c.wantReplayed)
			}
			if s.Balance(0) != 70 || s.Balance(1) != 130 {
				t.Fatalf("balances %d/%d, want 70/130",
					s.Balance(0), s.Balance(1))
			}
			if s.Total() != 200 {
				t.Fatalf("Total = %d, want 200", s.Total())
			}
			// Idempotent replay: second Recover applies nothing.
			again, err := g.Recover()
			if err != nil {
				t.Fatal(err)
			}
			if again != 0 {
				t.Fatalf("second Recover replayed %d, want 0", again)
			}
			if s.Balance(0) != 70 || s.Balance(1) != 130 {
				t.Fatal("second Recover changed state")
			}
			// Ledger keeps working after recovery.
			if err := g.Transfer(1, 0, 10); err != nil {
				t.Fatal(err)
			}
			if s.Balance(0) != 80 || s.Balance(1) != 120 {
				t.Fatalf("post-recovery transfer wrong: %d/%d",
					s.Balance(0), s.Balance(1))
			}
		})
	}
}

// TestConcurrentTransfers exercises semantics 2 and 7 under -race:
// total is conserved and Seq is strictly increasing with no gaps, no
// duplicates, and each txn's record pair adjacent in the WAL.
func TestConcurrentTransfers(t *testing.T) {
	const shards = 6
	const initial = 1_000_000
	const goroutines = 32
	const perG = 100
	g, l, s, _ := newLedger(shards, initial)
	var wg sync.WaitGroup
	for k := 0; k < goroutines; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				from := (k + i) % shards
				to := (k + i + 1) % shards
				if err := g.Transfer(from, to, 1); err != nil {
					t.Error(err)
				}
			}
		}(k)
	}
	wg.Wait()
	if got := s.Total(); got != shards*initial {
		t.Fatalf("Total = %d, want %d", got, shards*initial)
	}
	recs, err := l.Replay(0)
	if err != nil {
		t.Fatal(err)
	}
	want := goroutines * perG * 2
	if len(recs) != want {
		t.Fatalf("want %d WAL records, got %d", want, len(recs))
	}
	for i, r := range recs {
		if r.Seq != uint64(i+1) {
			t.Fatalf("Seq gap/dup at index %d: %d", i, r.Seq)
		}
		if i%2 == 1 && recs[i].Txn != recs[i-1].Txn {
			t.Fatalf("txn pair not adjacent at index %d", i)
		}
	}
	if l.LastSeq() != uint64(want) {
		t.Fatalf("LastSeq = %d, want %d", l.LastSeq(), want)
	}
}

// TestRecoverConcurrentWithTransfers exercises semantic 8: Recover
// running alongside transfers must not break any invariant. Recover is
// serialized with Transfer by the ledger mutex (see Ledger doc).
func TestRecoverConcurrentWithTransfers(t *testing.T) {
	const shards = 4
	const initial = 100_000
	g, _, s, _ := newLedger(shards, initial)
	var wg sync.WaitGroup
	for k := 0; k < 16; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_ = g.Transfer((k+i)%shards, (k+i+1)%shards, 1)
			}
		}(k)
	}
	for k := 0; k < 4; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, err := g.Recover(); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if _, err := g.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := s.Total(); got != shards*initial {
		t.Fatalf("Total = %d, want %d", got, shards*initial)
	}
}
