package netcode

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentAccess hammers the server from many goroutines (run with
// -race). The exact interleaving is nondeterministic, but the final state
// must equal some serial execution: every accepted input is processed
// exactly once and final positions match a naive sequential computation.
func TestConcurrentAccess(t *testing.T) {
	cfg := Config{W: 1_000_000, K: 5, M: 10, P: 64}
	s, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const per = 250
	ids := []string{"g0", "g1", "g2", "g3"}
	for _, id := range ids {
		if err := s.Register(id); err != nil {
			t.Fatal(err)
		}
	}
	deltaFor := func(seq int64) int64 {
		d := seq%7 - 3
		if d == 0 {
			d = 1
		}
		return d
	}

	var senders, background sync.WaitGroup
	var nowCtr atomic.Int64
	var stop atomic.Bool

	// Tickers: concurrent Tick calls with unique, monotonically allocated
	// timestamps. Some calls lose the race and see clock rollback; that is
	// a legal outcome and must be harmless.
	for g := 0; g < 2; g++ {
		background.Add(1)
		go func() {
			defer background.Done()
			for !stop.Load() {
				_, _ = s.Tick(nowCtr.Add(1))
			}
		}()
	}
	// Query readers.
	for g := 0; g < 2; g++ {
		background.Add(1)
		go func() {
			defer background.Done()
			for !stop.Load() {
				for _, id := range ids {
					_, _, _ = s.Authoritative(id)
					_, _ = s.PendingLen(id)
				}
			}
		}()
	}
	// Duplicate spammer: idempotent resends must never corrupt state.
	background.Add(1)
	go func() {
		defer background.Done()
		for !stop.Load() {
			_, _ = s.Receive(ids[0], Move{Seq: 1, Delta: deltaFor(1)})
		}
	}()
	// One sender per player: sequential seqs, retrying on backlog full.
	for _, id := range ids {
		id := id
		senders.Add(1)
		go func() {
			defer senders.Done()
			for seq := int64(1); seq <= per; {
				mv := Move{Seq: seq, Delta: deltaFor(seq)}
				r, err := s.Receive(id, mv)
				if err != nil {
					t.Errorf("Receive(%s, %+v): %v", id, mv, err)
					return
				}
				switch r.Status {
				case StatusAccepted, StatusDuplicate:
					// Duplicate can only happen for seq 1 of g0, where
					// the spammer races an identical move; the effect
					// is the same as if this call had been accepted.
					seq++
				case StatusBacklogFull:
					// backlog drained by tickers; retry the same seq
				default:
					t.Errorf("Receive(%s, %+v): unexpected %v", id, mv, r.Status)
					return
				}
			}
		}()
	}

	senders.Wait()
	stop.Store(true)
	background.Wait()

	// Drain deterministically, then compare against the naive model.
	for {
		total := 0
		for _, id := range ids {
			n, _ := s.PendingLen(id)
			total += n
		}
		if total == 0 {
			break
		}
		if _, err := s.Tick(nowCtr.Add(1)); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		pos, seq, err := s.Authoritative(id)
		if err != nil {
			t.Fatal(err)
		}
		if seq != per {
			t.Fatalf("%s: processed seq = %d, want %d", id, seq, per)
		}
		want := int64(0)
		for i := int64(1); i <= per; i++ {
			want, _ = mStep(want, deltaFor(i), cfg.W, cfg.M)
		}
		if pos != want {
			t.Fatalf("%s: pos = %d, want %d", id, pos, want)
		}
	}
}
