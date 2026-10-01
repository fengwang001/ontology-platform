package quota

import (
	"errors"
	"testing"
)

func TestBatchBasics(t *testing.T) {
	l := New()
	mustOK(t, l.SetQuota(0, 1000, -1))

	// Empty batch is an invalid argument and a batch failure.
	_, err := l.Batch(nil)
	errIs(t, err, ErrBatch, ErrInvalid)

	// Later ops may reference ids allocated earlier inside the batch.
	ids, err := l.Batch([]Op{
		{Kind: OpMkdir, P: 0}, // -> 1
		{Kind: OpReserve, X: 1, Bytes: 10},
		{Kind: OpAddFile, P: 1, Size: 10}, // -> 2, consumes reserve
		{Kind: OpMkdir, P: 1},             // -> 3
	})
	mustOK(t, err)
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("unexpected ids: %v", ids)
	}
	b, e, r := lUsage(l, 0)
	if b != 10 || e != 3 || r != 0 {
		t.Fatalf("post-batch b=%d e=%d r=%d", b, e, r)
	}

	// Mid-batch failure: full rollback, no ids consumed.
	ids, err = l.Batch([]Op{
		{Kind: OpMkdir, P: 0},  // would be 4
		{Kind: OpMkdir, P: 99}, // missing parent -> fail
	})
	if ids != nil {
		t.Fatalf("failed batch must not return ids: %v", ids)
	}
	be, ok := err.(*BatchError)
	if !ok || be.Index != 1 {
		t.Fatalf("want BatchError index 1, got %v", err)
	}
	errIs(t, err, ErrBatch, ErrNotFound)

	// State unchanged: next allocations continue from 4.
	id, err := l.Mkdir(0)
	mustOK(t, err)
	if id != 4 {
		t.Fatalf("rolled-back batch consumed an id: next=%d", id)
	}
	b, e, r = lUsage(l, 0)
	if b != 10 || e != 4 || r != 0 {
		t.Fatalf("post-rollback b=%d e=%d r=%d", b, e, r)
	}

	// Unknown op kind is an invalid argument.
	_, err = l.Batch([]Op{{Kind: "Nope"}})
	errIs(t, err, ErrBatch, ErrInvalid)

	// Rejection order inside batch: invalid args before existence etc.
	_, err = l.Batch([]Op{{Kind: OpAddFile, P: 99, Size: -1}})
	be, _ = err.(*BatchError)
	if be.Index != 0 || !errors.Is(err, ErrInvalid) {
		t.Fatalf("want invalid first, got %v", err)
	}
}
