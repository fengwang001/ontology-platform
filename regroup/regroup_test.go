package regroup

import (
	"errors"
	"testing"

	"ontology/txn"
)

func sameStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type step struct {
	k    txn.Kind
	tx   int64
	d    string
	err  error    // expected error (nil = accepted)
	buf  int      // expected Buffered after the step
	rows []string // nil = no emission; non-nil = expected emitted rows in order
}

func (s step) apply(r *Regrouper) (txn.Txn, error) {
	switch s.k {
	case txn.BEGIN:
		return txn.Txn{}, r.Begin(s.tx)
	case txn.ROW:
		return txn.Txn{}, r.Row(s.tx, s.d)
	case txn.COMMIT:
		return r.Commit(s.tx)
	default:
		return txn.Txn{}, r.Rollback(s.tx)
	}
}

func runSteps(t *testing.T, max int, steps []step) *Regrouper {
	t.Helper()
	r := New(max)
	for i, s := range steps {
		got, err := s.apply(r)
		if !errors.Is(err, s.err) || r.Buffered() != s.buf {
			t.Fatalf("step %d: err=%v buf=%d (want err=%v buf=%d)", i, err, r.Buffered(), s.err, s.buf)
		}
		if s.rows != nil && (got.Tx != s.tx || !sameStr(got.Rows, s.rows)) {
			t.Fatalf("step %d: emitted tx=%d rows=%v want tx=%d rows=%v", i, got.Tx, got.Rows, s.tx, s.rows)
		}
	}
	return r
}

func TestRules(t *testing.T) {
	B, R, C, X := txn.BEGIN, txn.ROW, txn.COMMIT, txn.ROLLBACK
	cases := []struct {
		name string
		max  int
		ss   []step
	}{
		{"dup begin active", 4, []step{{B, 1, "", nil, 0, nil}, {B, 1, "", txn.ErrDuplicateBegin, 0, nil}}},
		{"dup begin after commit", 4, []step{{B, 1, "", nil, 0, nil}, {C, 1, "", nil, 0, []string{}}, {B, 1, "", txn.ErrDuplicateBegin, 0, nil}}},
		{"row unknown", 4, []step{{R, 1, "x", ErrUnknownTxn, 0, nil}}},
		{"orphan commit", 4, []step{{C, 1, "", ErrUnknownTxn, 0, nil}}},
		{"commit after rollback", 4, []step{{B, 1, "", nil, 0, nil}, {X, 1, "", nil, 0, nil}, {C, 1, "", ErrUnknownTxn, 0, nil}}},
		{"double commit", 4, []step{{B, 1, "", nil, 0, nil}, {C, 1, "", nil, 0, []string{}}, {C, 1, "", ErrUnknownTxn, 0, nil}}},
		{"rollback unknown then repeat", 4, []step{{X, 1, "", ErrUnknownTxn, 0, nil}, {B, 1, "", nil, 0, nil}, {X, 1, "", nil, 0, nil}, {X, 1, "", ErrUnknownTxn, 0, nil}}},
		{"rollback drops rows", 4, []step{{B, 1, "", nil, 0, nil}, {R, 1, "a", nil, 1, nil}, {R, 1, "b", nil, 2, nil}, {X, 1, "", nil, 0, nil}, {R, 1, "a", ErrUnknownTxn, 0, nil}}},
		{"full keeps accepted rows", 1, []step{{B, 1, "", nil, 0, nil}, {R, 1, "keep", nil, 1, nil}, {R, 1, "drop", ErrBufferFull, 1, nil}, {C, 1, "", nil, 0, []string{"keep"}}}},
		{"empty commit emits zero rows", 2, []step{{B, 1, "", nil, 0, nil}, {C, 1, "", nil, 0, []string{}}}},
		{"commit arrival order", 3, []step{{B, 1, "", nil, 0, nil}, {B, 2, "", nil, 0, nil}, {R, 2, "a", nil, 1, nil}, {R, 1, "b", nil, 2, nil}, {C, 2, "", nil, 1, []string{"a"}}, {C, 1, "", nil, 0, []string{"b"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { runSteps(t, c.max, c.ss) })
	}
}

// TestCheckedCounterBounds proves per-transaction buffering by reading the
// unexported counter directly (never through an exported method): a 1-row
// tx commit inspects 1 row regardless of another tx's m rows; the m-row
// tx then inspects m; a 2-row rollback inspects 2.
func TestCheckedCounterBounds(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := New(m + 10)
		if err := r.Begin(1); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			if err := r.Row(1, "x"); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.Begin(2); err != nil {
			t.Fatal(err)
		}
		if err := r.Row(2, "s"); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Commit(2); err != nil || r.lastChecked != 1 {
			t.Fatalf("small commit: err=%v checked=%d want 1", err, r.lastChecked)
		}
		if _, err := r.Commit(1); err != nil || r.lastChecked != m {
			t.Fatalf("big commit: err=%v checked=%d want %d", err, r.lastChecked, m)
		}
		_ = r.Begin(3)
		_ = r.Row(3, "p")
		_ = r.Row(3, "q")
		if err := r.Rollback(3); err != nil || r.lastChecked != 2 {
			t.Fatalf("rollback: err=%v checked=%d want 2", err, r.lastChecked)
		}
	}
}

// TestRejectionLeavesState verifies every rejection class mutates nothing
// (buffer stays 1, output empty) and the rejected tx is never created; the
// in-progress tx then commits exactly its one kept row.
func TestRejectionLeavesState(t *testing.T) {
	r := runSteps(t, 1, []step{{txn.BEGIN, 1, "", nil, 0, nil}, {txn.ROW, 1, "keep", nil, 1, nil}})
	reject := []step{
		{txn.BEGIN, 1, "", txn.ErrDuplicateBegin, 1, nil},
		{txn.ROW, 2, "x", ErrUnknownTxn, 1, nil},
		{txn.COMMIT, 9, "", ErrUnknownTxn, 1, nil},
		{txn.ROLLBACK, 9, "", ErrUnknownTxn, 1, nil},
		{txn.ROW, 1, "drop", ErrBufferFull, 1, nil},
	}
	for i, s := range reject {
		_, err := s.apply(r)
		if !errors.Is(err, s.err) || r.Buffered() != 1 || len(r.Output()) != 0 {
			t.Fatalf("reject step %d changed state: err=%v buf=%d out=%d", i, err, r.Buffered(), len(r.Output()))
		}
	}
	r.mu.Lock()
	_, tx2Created := r.txns[2]
	r.mu.Unlock()
	if tx2Created {
		t.Fatal("event for unknown tx2 created the transaction")
	}
	got, err := r.Commit(1)
	if err != nil || !sameStr(got.Rows, []string{"keep"}) || r.Buffered() != 0 {
		t.Fatalf("final: %+v err=%v buf=%d", got, err, r.Buffered())
	}
}
