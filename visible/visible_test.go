package visible

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/snapshot"
	"ontology/txn"
)

func mkSnap(t *testing.T, n int, hi uint64, self txn.ID, active []txn.ID, post ...func(*txn.Table)) *snapshot.Snapshot {
	tab := txn.NewTable()
	for id := txn.ID(1); id <= txn.ID(n); id++ {
		tab.Begin(id)
		_ = tab.Commit(id, uint64(id))
	}
	for _, f := range post {
		f(tab)
	}
	s, err := snapshot.New(tab, hi, self, active)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestCheck(t *testing.T) {
	released := mkSnap(t, 3, 10, 0, nil)
	released.Release()
	cases := []struct {
		name    string
		snap    *snapshot.Snapshot
		id      txn.ID
		want    bool
		wantErr error
	}{
		{"counterexample", mkSnap(t, 3, 10, 0, []txn.ID{1}), 1, false, nil},
		{"commit-eq-hiwater", mkSnap(t, 3, 10, 0, nil, func(tab *txn.Table) { _ = tab.Commit(1, 10) }), 1, false, nil},
		{"own-write", mkSnap(t, 3, 10, 1, []txn.ID{1}, func(tab *txn.Table) { tab.Begin(1) }), 1, true, nil},
		{"aborted", mkSnap(t, 3, 10, 0, nil, func(tab *txn.Table) { _ = tab.Abort(1) }), 1, false, nil},
		{"zero-hiwater", mkSnap(t, 3, 0, 0, nil), 1, false, nil},
		{"empty-active", mkSnap(t, 3, 10, 0, nil), 1, true, nil},
		{"unknown-txn", mkSnap(t, 3, 10, 0, nil), 99, false, txn.ErrUnknownTxn},
		{"released", released, 1, false, snapshot.ErrSnapshotReleased},
		{"corrupt-chain", mkSnap(t, 3, 10, 0, nil, func(tab *txn.Table) { _ = tab.Commit(2, 0) }), 1, false, ErrCorruptChain},
	}
	for _, c := range cases {
		var ch Checker
		got, err := ch.Check(c.snap, c.id)
		if !errors.Is(err, c.wantErr) || got != c.want {
			t.Errorf("%s: got (%v, %v), want (%v, %v)", c.name, got, err, c.want, c.wantErr)
		}
	}
	if e, _ := cases[0].snap.Table().Status(1); e.CommitSeq >= 10 {
		t.Fatal("naive rule (seq < hi-water) must call the counterexample visible")
	}
	if _, err := snapshot.New(txn.NewTable(), 1, 0, make([]txn.ID, snapshot.MaxActive+1)); !errors.Is(err, snapshot.ErrTooManyActive) {
		t.Fatal("want ErrTooManyActive")
	}
}
func TestConcurrentAndRandom(t *testing.T) {
	active := make([]txn.ID, 1000)
	for i := range active {
		active[i] = txn.ID(i + 1)
	}
	snap := mkSnap(t, 10000, 5000, 0, active)
	if snap.ActiveItems() != 1000 {
		t.Fatal("snapshot memory items must equal active set size, not txn count")
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Go(func() {
			var ch Checker
			for id := txn.ID(1); id <= 10000; id += 50 {
				v, _ := ch.Check(snap, id)
				if nv, _ := NaiveCheck(snap, id); v != nv || ch.Lookups() > 2 {
					t.Error("concurrent check mismatch or lookup overflow")
				}
			}
		})
	}
	wg.Wait()
	rng := rand.New(rand.NewSource(1))
	var ch Checker
	for i := 0; i < 10000; i++ {
		active := make([]txn.ID, rng.Intn(100))
		for j := range active {
			active[j] = txn.ID(rng.Intn(11000))
		}
		snap, _ := snapshot.New(snap.Table(), uint64(rng.Intn(11000)), 0, active)
		id := txn.ID(rng.Intn(11000))
		got, gerr := ch.Check(snap, id)
		want, werr := NaiveCheck(snap, id)
		if got != want || (gerr == nil) != (werr == nil) {
			t.Fatalf("i=%d id=%d: got (%v,%v), naive (%v,%v)", i, id, got, gerr, want, werr)
		}
	}
}
