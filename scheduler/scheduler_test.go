package scheduler

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func tx(seq int, reads []string, writes map[string]string) Transaction {
	return Transaction{Seq: seq, Reads: reads, Writes: writes}
}

func TestAddAndDepths(t *testing.T) {
	s := New(&bytes.Buffer{})
	w3 := map[string]string{"a": "3", "c": "3"}
	w3["a"] = "3"
	err := s.Add([]Transaction{
		tx(1, []string{"a", "b"}, map[string]string{"a": "1", "x": "1"}),
		tx(2, []string{"a"}, map[string]string{"b": "2"}),
		tx(3, nil, w3),
		tx(4, nil, map[string]string{"a": "4", "b": "4"}),
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	depths := s.Depths()
	want := map[int]int{1: 1, 2: 1, 3: 2, 4: 3}
	for seq, depth := range want {
		if depths[seq] != depth {
			t.Errorf("depth of tx %d = %d, want %d", seq, depths[seq], depth)
		}
	}
}

func TestAddContinuityAcrossBatches(t *testing.T) {
	s := New(nil)
	if err := s.Add([]Transaction{tx(1, nil, map[string]string{"a": "1"})}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add([]Transaction{tx(2, nil, map[string]string{"b": "2"}), tx(3, nil, map[string]string{"c": "3"})}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add([]Transaction{tx(3, nil, map[string]string{"c": "9"})}); err == nil {
		t.Fatal("duplicate seq accepted")
	}
	if err := s.Add([]Transaction{tx(5, nil, map[string]string{"e": "5"})}); err == nil {
		t.Fatal("gap accepted")
	}
	if got := s.Count(); got != 3 {
		t.Fatalf("count after rejects = %d, want 3", got)
	}
}

func TestRejectReasons(t *testing.T) {
	s := New(nil)
	observed := map[RejectReason]bool{}
	if err := s.Add(nil); RejectReasonOf(err) != ReasonNilTransactions {
		t.Fatalf("nil: %v", err)
	} else {
		observed[ReasonNilTransactions] = true
	}
	if err := s.Add([]Transaction{}); RejectReasonOf(err) != ReasonEmptyTransactions {
		t.Fatalf("empty: %v", err)
	} else {
		observed[ReasonEmptyTransactions] = true
	}
	cases := []struct {
		name string
		txs  []Transaction
		want RejectReason
	}{
		{"gap", []Transaction{
			tx(1, nil, map[string]string{"a": "1"}),
			tx(3, nil, map[string]string{"c": "3"}),
		}, ReasonSequenceGap},
		{"starts at two", []Transaction{tx(2, nil, map[string]string{"b": "2"})}, ReasonSequenceGap},
		{"empty write set", []Transaction{
			tx(1, nil, map[string]string{"a": "1"}),
			tx(2, nil, map[string]string{}),
		}, ReasonEmptyWriteSet},
		{"empty write key", []Transaction{
			tx(1, nil, map[string]string{"a": "1"}),
			tx(2, nil, map[string]string{"": "x"}),
		}, ReasonEmptyWriteKey},
	}
	for _, tc := range cases {
		err := s.Add(tc.txs)
		if RejectReasonOf(err) != tc.want {
			t.Fatalf("%s: got reason %q (%v), want %q", tc.name, RejectReasonOf(err), err, tc.want)
		}
		observed[tc.want] = true
	}

	big := make([]Transaction, MaxTransactions+1)
	for i := range big {
		big[i] = tx(i+1, nil, map[string]string{fmt.Sprintf("k%d", i): "v"})
	}
	if err := s.Add(big); RejectReasonOf(err) != ReasonTooManyTransactions {
		t.Fatalf("too many: %v", err)
	}
	observed[ReasonTooManyTransactions] = true

	if err := s.Add([]Transaction{tx(1, nil, map[string]string{"a": "1"})}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Schedule(0); RejectReasonOf(err) != ReasonInvalidParallelism {
		t.Fatalf("parallelism zero: %v", err)
	}
	if _, err := s.Schedule(-1); RejectReasonOf(err) != ReasonInvalidParallelism {
		t.Fatalf("parallelism negative: %v", err)
	}
	if _, err := s.Replay(0); RejectReasonOf(err) != ReasonInvalidParallelism {
		t.Fatalf("replay parallelism: %v", err)
	}
	observed[ReasonInvalidParallelism] = true

	wantReasons := []RejectReason{
		ReasonNilTransactions,
		ReasonEmptyTransactions,
		ReasonSequenceGap,
		ReasonEmptyWriteSet,
		ReasonEmptyWriteKey,
		ReasonTooManyTransactions,
		ReasonInvalidParallelism,
	}
	seen := map[RejectReason]bool{}
	for _, reason := range wantReasons {
		if seen[reason] {
			t.Fatalf("duplicate reason constant: %s", reason)
		}
		seen[reason] = true
		if !observed[reason] {
			t.Fatalf("reason not exercised: %s", reason)
		}
	}
}

func TestRejectLeavesNoTrace(t *testing.T) {
	var logs bytes.Buffer
	s := New(&logs)
	good := []Transaction{
		tx(1, nil, map[string]string{"a": "1"}),
		tx(2, nil, map[string]string{"a": "2", "b": "2"}),
	}
	if err := s.Add(good); err != nil {
		t.Fatal(err)
	}
	depthsBefore := s.Depths()
	roundsBefore, err := s.Schedule(2)
	if err != nil {
		t.Fatal(err)
	}

	badBatch := []Transaction{
		tx(3, nil, map[string]string{"c": "3"}),
		tx(4, nil, map[string]string{}),
	}
	if err := s.Add(badBatch); err == nil {
		t.Fatal("bad batch accepted")
	}
	if s.Count() != 2 {
		t.Fatalf("count changed after reject: %d", s.Count())
	}
	if !mapsEqual(s.Depths(), depthsBefore) {
		t.Fatal("depths changed after rejected batch")
	}
	roundsAfter, err := s.Schedule(2)
	if err != nil {
		t.Fatal(err)
	}
	if !roundsEqual(roundsAfter, roundsBefore) {
		t.Fatal("schedule changed after rejected batch")
	}
	result, err := s.Replay(2)
	if err != nil {
		t.Fatal(err)
	}
	if result.State["a"] != "2" || result.State["b"] != "2" || len(result.State) != 2 {
		t.Fatalf("replay state changed after rejected batch: %v", result.State)
	}
}

func mapsEqual(a, b map[int]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func roundsEqual(a, b []Round) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Index != b[i].Index || len(a[i].Transaction) != len(b[i].Transaction) {
			return false
		}
		for j := range a[i].Transaction {
			if a[i].Transaction[j] != b[i].Transaction[j] {
				return false
			}
		}
	}
	return true
}

func TestConcurrentQueriesAreStable(t *testing.T) {
	s := New(nil)
	txs := make([]Transaction, 200)
	for i := range txs {
		key := []string{"a", "b", "c", "d"}[i%4]
		txs[i] = tx(i+1, []string{"r" + key}, map[string]string{key: fmt.Sprintf("%d", i+1)})
	}
	if err := s.Add(txs); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for iter := 0; iter < 20; iter++ {
				p := 1 + iter%8
				depths := s.Depths()
				rounds, err := s.Schedule(p)
				if err != nil {
					t.Error(err)
					return
				}
				result, err := s.Replay(p)
				if err != nil {
					t.Error(err)
					return
				}
				if len(depths) != 200 || !result.SelfCheckOK {
					t.Errorf("bad result: depths=%d selfcheck=%v", len(depths), result.SelfCheckOK)
					return
				}
				if !roundsEqual(rounds, result.Rounds) {
					t.Error("Schedule/Replay rounds differ")
					return
				}
				for seq, depth := range depths {
					if result.Depth[seq] != depth {
						t.Errorf("depth mismatch seq %d", seq)
						return
					}
				}
				want := map[string]string{"a": "197", "b": "198", "c": "199", "d": "200"}
				for k, v := range want {
					if result.State[k] != v {
						t.Errorf("state[%s]=%s want %s", k, result.State[k], v)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}
