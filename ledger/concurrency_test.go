package ledger

import (
	"errors"
	"sync"
	"testing"
)

func TestCheckpointTruncatesOnlyApplied(t *testing.T) {
	g, l, s, _ := newLedger(2, 100)
	if err := g.Transfer(0, 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := g.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	recs, _ := l.Replay(0)
	if len(recs) != 0 {
		t.Fatalf("checkpoint left %d records, want 0", len(recs))
	}
	// 截断后崩溃并恢复：不变量仍成立。
	if _, err := g.Recover(); err != nil {
		t.Fatal(err)
	}
	if s.Total() != 200 {
		t.Fatalf("Total = %d, want 200", s.Total())
	}
}

func TestCheckpointKeepsUnappliedRecords(t *testing.T) {
	g, l, s, _ := newLedger(2, 100)
	if err := g.Transfer(0, 1, 10); err != nil {
		t.Fatal(err)
	}
	// 第二笔在 Apply 前崩溃：记录未应用，Checkpoint 不得截掉它。
	g.CrashAt(CrashAfterAppend)
	if err := g.Transfer(0, 1, 20); !errors.Is(err, ErrCrashed) {
		t.Fatalf("want ErrCrashed, got %v", err)
	}
	if err := g.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	recs, _ := l.Replay(0)
	if len(recs) != 2 {
		t.Fatalf("checkpoint truncated unapplied records: %d left, want 2", len(recs))
	}
	// 截断后立刻崩溃并恢复：未应用的 Txn 必须完整生效。
	if _, err := g.Recover(); err != nil {
		t.Fatal(err)
	}
	if s.Balance(0) != 70 || s.Balance(1) != 130 {
		t.Fatalf("balances = %d,%d, want 70,130", s.Balance(0), s.Balance(1))
	}
}

func TestConcurrentTransfers(t *testing.T) {
	const shards, initial = 6, 100000
	g, l, s, _ := newLedger(shards, initial)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				from := (w + i) % shards
				to := (w + i + 1) % shards
				if err := g.Transfer(from, to, 1); err != nil {
					t.Errorf("transfer: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if got, want := s.Total(), int64(shards*initial); got != want {
		t.Fatalf("Total = %d, want %d", got, want)
	}
	// Seq 严格递增无空洞无重号，同一 Txn 的两条记录相邻。
	recs, _ := l.Replay(0)
	if len(recs)%2 != 0 {
		t.Fatalf("odd record count %d", len(recs))
	}
	for i, r := range recs {
		if r.Seq != uint64(i+1) {
			t.Fatalf("Seq gap/dup at %d: Seq = %d", i, r.Seq)
		}
		if i%2 == 1 && recs[i-1].Txn != r.Txn {
			t.Fatalf("txn %d records not adjacent at %d", r.Txn, i)
		}
	}
}

func TestRecoverConcurrentWithTransfers(t *testing.T) {
	const shards, initial = 4, 100000
	g, _, s, _ := newLedger(shards, initial)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = g.Transfer((w+i)%shards, (w+i+1)%shards, 1)
			}
		}(w)
	}
	for i := 0; i < 50; i++ {
		if _, err := g.Recover(); err != nil {
			t.Errorf("recover: %v", err)
		}
		if err := g.Checkpoint(); err != nil {
			t.Errorf("checkpoint: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	if _, err := g.Recover(); err != nil {
		t.Fatal(err)
	}
	if got, want := s.Total(), int64(shards*initial); got != want {
		t.Fatalf("Total = %d, want %d", got, want)
	}
}
