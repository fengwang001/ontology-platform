package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestFailedActionProducesNoRecordNorSeq(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, &ActionType{
		Name: "Ok",
		Run:  func(*Tx, map[string]any) error { return nil },
	})
	mustRegister(t, e, &ActionType{
		Name: "Bad",
		Run:  func(*Tx, map[string]any) error { return errors.New("boom") },
	})
	if _, err := e.Execute("Bad", nil); err == nil {
		t.Fatal("want failure")
	}
	rec, err := e.Execute("Ok", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Seq != 1 {
		t.Fatalf("failed action consumed a seq: got %d, want 1", rec.Seq)
	}
	if got := e.Records(1, 100); len(got) != 1 {
		t.Fatalf("failed action produced a record: %v", got)
	}
}

func TestRecordsRangeQueryStableOrder(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, &ActionType{
		Name: "Ok",
		Run:  func(*Tx, map[string]any) error { return nil },
	})
	for i := 0; i < 5; i++ {
		if _, err := e.Execute("Ok", nil); err != nil {
			t.Fatal(err)
		}
	}
	recs := e.Records(2, 4)
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d", len(recs))
	}
	for i, want := range []uint64{2, 3, 4} {
		if recs[i].Seq != want {
			t.Fatalf("record %d: want seq %d, got %d", i, want, recs[i].Seq)
		}
	}
	if got := e.Records(9, 10); len(got) != 0 {
		t.Fatalf("out-of-range query must be empty, got %v", got)
	}
	// Returned records are immutable copies.
	recs[0].Params["x"] = 1
	recs[0].Objects = append(recs[0].Objects, "y")
	again := e.Records(2, 2)
	if len(again[0].Params) != 0 || len(again[0].Objects) != 0 {
		t.Fatal("mutating returned records must not affect the log")
	}
}

func TestConcurrentCommitsSeqStrictlyIncreasing(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, &ActionType{
		Name: "Ok",
		Params: []ParamSpec{
			{Name: "id", Type: TypeString, Required: true},
		},
		Run: func(tx *Tx, p map[string]any) error {
			return tx.CreateObject(p["id"].(string), "T", nil)
		},
	})
	mustRegister(t, e, &ActionType{
		Name: "Bad",
		Run:  func(*Tx, map[string]any) error { return errors.New("boom") },
	})
	const n = 64
	seqs := make(chan uint64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%3 == 0 {
				// Interleave failures; they must not consume sequence numbers.
				_, _ = e.Execute("Bad", nil)
			}
			rec, err := e.Execute("Ok", map[string]any{"id": fmt.Sprintf("o%d", i)})
			if err != nil {
				t.Error(err)
				return
			}
			seqs <- rec.Seq
		}(i)
	}
	wg.Wait()
	close(seqs)
	seen := map[uint64]bool{}
	count := 0
	for s := range seqs {
		if seen[s] {
			t.Fatalf("duplicate seq %d", s)
		}
		seen[s] = true
		count++
	}
	// Strictly increasing with no gaps: exactly 1..count.
	for s := uint64(1); s <= uint64(count); s++ {
		if !seen[s] {
			t.Fatalf("gap at seq %d", s)
		}
	}
	if got := len(e.Records(1, 1<<60)); got != count {
		t.Fatalf("log has %d records, want %d", got, count)
	}
	if got := e.Store().ObjectCount(); got != count {
		t.Fatalf("store has %d objects, want %d", got, count)
	}
}
