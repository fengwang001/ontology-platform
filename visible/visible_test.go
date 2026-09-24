package visible_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/snapshot"
	"ontology/txn"
	"ontology/visible"
)

func TestVisibilityAndErrors(t *testing.T) {
	tab := txn.NewTable()
	tab.Commit(1, 5)
	tab.Commit(2, 0)
	tab.Commit(3, 10)
	tab.Abort(4)
	type tc struct {
		water  uint64
		own    txn.ID
		active []txn.ID
		v      txn.Version
		want   bool
	}
	cases := []tc{
		{0, 99, nil, txn.Version{Txn: 2, Seq: 0}, false},          // water zero hides all
		{10, 99, nil, txn.Version{Txn: 1, Seq: 5}, true},          // empty active set
		{10, 1, []txn.ID{1}, txn.Version{Txn: 1, Seq: 5}, true},   // own write visible
		{10, 99, nil, txn.Version{Txn: 3, Seq: 10}, false},        // seq == water
		{10, 99, nil, txn.Version{Txn: 4}, false},                 // aborted
		{10, 99, []txn.ID{1}, txn.Version{Txn: 1, Seq: 5}, false}, // counterexample
	}
	for i, c := range cases {
		s, _ := snapshot.New(c.water, c.own, c.active)
		got, n, err := visible.Check(tab, s, c.v)
		if err != nil || got != c.want || n > 2 {
			t.Fatalf("case %d: got=%v n=%d err=%v, want %v", i, got, n, err, c.want)
		}
	}
	if s, _ := snapshot.New(10, 99, []txn.ID{1}); !(5 < s.Water()) {
		t.Fatal("naive rule (seq < water) must call the counterexample visible")
	}
	es, _ := snapshot.New(10, 99, nil)
	_, _, unknownErr := visible.Check(tab, es, txn.Version{Txn: 42})
	es.Release()
	_, _, releasedErr := visible.Check(tab, es, txn.Version{Txn: 1})
	_, limitErr := snapshot.New(1, 0, make([]txn.ID, snapshot.MaxActive+1))
	bad := &txn.Version{Txn: 1, Seq: 5, Prev: &txn.Version{Txn: 1, Seq: 7}}
	errCases := []struct{ got, want error }{
		{unknownErr, txn.ErrUnknownTxn}, {releasedErr, snapshot.ErrReleased},
		{limitErr, snapshot.ErrTooManyActive}, {visible.CheckChain(bad), visible.ErrCorruptChain},
	}
	for i, c := range errCases {
		if !errors.Is(c.got, c.want) {
			t.Fatalf("error case %d: got %v, want %v", i, c.got, c.want)
		}
	}
}

func TestLimitBudgetConcurrent(t *testing.T) {
	tab, active := txn.NewTable(), make([]txn.ID, 1000)
	for i := txn.ID(1); i <= 10000; i++ {
		tab.Commit(i, uint64(i))
		if i <= 1000 {
			active[i-1] = i
		}
	}
	s, _ := snapshot.New(20000, 0, active)
	if s.Items() != len(active) {
		t.Fatalf("items=%d, want %d regardless of %d txns", s.Items(), len(active), tab.Len())
	}
	if _, n, _ := visible.Check(tab, s, txn.Version{Txn: 5000}); n > 2 {
		t.Fatalf("single judgment did %d lookups, want <= 2", n)
	}
	var wg sync.WaitGroup
	wg.Add(8)
	for g := 0; g < 8; g++ {
		go func() {
			defer wg.Done()
			for i := txn.ID(1); i <= 10000; i += 10 {
				a, n, _ := visible.Check(tab, s, txn.Version{Txn: i})
				if b, _ := visible.NaiveCheck(tab, s, txn.Version{Txn: i}); a != b || n > 2 {
					t.Errorf("txn %d: got=%v n=%d, naive=%v", i, a, n, b)
				}
			}
		}()
	}
	wg.Wait()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 10000; i++ {
		rs, _ := snapshot.New(uint64(rng.Intn(20000)), 0, active[:rng.Intn(1000)])
		v := txn.Version{Txn: txn.ID(rng.Intn(10000) + 1)}
		a, _, _ := visible.Check(tab, rs, v)
		if b, _ := visible.NaiveCheck(tab, rs, v); a != b {
			t.Fatalf("case %d: impl=%v naive=%v", i, a, b)
		}
	}
}
