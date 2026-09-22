package reasm

import (
	"errors"
	"testing"
	"time"
)

func TestOutOfOrderDeliveredOnceByteExact(t *testing.T) {
	r, _ := setup(1 << 20)
	full := []byte("hello world, reassembled!")
	parts := []struct {
		off  int
		data []byte
	}{{10, full[10:20]}, {0, full[0:10]}, {20, full[20:]}}
	deliveries := 0
	var got []byte
	for _, p := range parts {
		msg, done, err := r.Submit("m1", p.off, p.data, len(full))
		if err != nil {
			t.Fatal(err)
		}
		if done {
			deliveries++
			got = msg
		}
	}
	if deliveries != 1 {
		t.Fatalf("delivered %d times", deliveries)
	}
	if string(got) != string(full) {
		t.Fatalf("got %q", got)
	}
	if r.Used() != 0 {
		t.Fatalf("delivered message still holds %d bytes", r.Used())
	}
	if info := r.Query("m1"); info != (Info{}) {
		t.Fatalf("delivered message query not zero: %+v", info)
	}
}

func TestDuplicateIdempotentAndNotDoubleCharged(t *testing.T) {
	r, _ := setup(100)
	if _, _, err := r.Submit("m", 0, []byte("abcd"), 10); err != nil {
		t.Fatal(err)
	}
	usedBefore := r.Used()
	if _, done, err := r.Submit("m", 0, []byte("abcd"), 10); err != nil || done {
		t.Fatalf("duplicate: done=%v err=%v", done, err)
	}
	if r.Used() != usedBefore {
		t.Fatalf("duplicate charged budget: %d -> %d", usedBefore, r.Used())
	}
	if info := r.Query("m"); info.Received != 4 {
		t.Fatalf("received %d", info.Received)
	}
}

func TestConflictDoesNotPollute(t *testing.T) {
	r, _ := setup(100)
	if _, _, err := r.Submit("m", 0, []byte("abcde"), 10); err != nil {
		t.Fatal(err)
	}
	usedBefore := r.Used()
	_, _, err := r.Submit("m", 3, []byte("dZZ"), 10)
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if ce.Start != 4 || ce.End != 5 {
		t.Fatalf("conflict range [%d,%d)", ce.Start, ce.End)
	}
	if r.Used() != usedBefore {
		t.Fatalf("budget changed after conflict: %d -> %d", usedBefore, r.Used())
	}
	if info := r.Query("m"); info.Received != 5 {
		t.Fatalf("received changed to %d", info.Received)
	}
}

func TestDistinctErrors(t *testing.T) {
	r, _ := setup(100)
	submit := func(id string, off int, data []byte, total int) error {
		_, _, err := r.Submit(id, off, data, total)
		return err
	}
	if err := submit("a", 0, nil, 10); !errors.Is(err, ErrEmptyFragment) {
		t.Fatalf("empty: %v", err)
	}
	if err := submit("b", 0, []byte("x"), 0); !errors.Is(err, ErrZeroTotal) {
		t.Fatalf("zero total: %v", err)
	}
	if err := submit("c", 8, []byte("xyz"), 10); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("out of range: %v", err)
	}
	if _, _, err := r.Submit("d", 0, []byte("ab"), 10); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Submit("d", 2, []byte("cd"), 20); !errors.Is(err, ErrTotalMismatch) {
		t.Fatalf("total mismatch: %v", err)
	}
	for _, e := range []error{ErrEmptyFragment, ErrZeroTotal, ErrOutOfRange, ErrBudgetExceeded} {
		if errors.Is(e, ErrTotalMismatch) && e != ErrTotalMismatch {
			t.Fatalf("%v confusable with ErrTotalMismatch", e)
		}
	}
}

func TestEvictionAtExactExpiryReleasesBudget(t *testing.T) {
	r, c := setup(100)
	if _, _, err := r.Submit("m", 0, []byte("abcde"), 10); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Minute - time.Nanosecond)
	if info := r.Query("m"); info.Received != 5 {
		t.Fatalf("evicted early: %+v", info)
	}
	c.Advance(time.Nanosecond) // now == expiresAt, left-closed
	if info := r.Query("m"); info != (Info{}) {
		t.Fatalf("not evicted at expiry: %+v", info)
	}
	if r.Used() != 0 {
		t.Fatalf("budget not released: %d", r.Used())
	}
}

func TestEvictedIDRestartsAsNewMessage(t *testing.T) {
	r, c := setup(100)
	if _, _, err := r.Submit("m", 0, []byte("abcde"), 10); err != nil {
		t.Fatal(err)
	}
	c.Advance(2 * time.Minute)
	if _, done, err := r.Submit("m", 5, []byte("fghij"), 10); err != nil || done {
		t.Fatalf("restart: done=%v err=%v", done, err)
	}
	if info := r.Query("m"); info.Received != 5 {
		t.Fatalf("old state leaked: %+v", info)
	}
	msg, done, err := r.Submit("m", 0, []byte("abcde"), 10)
	if err != nil || !done || string(msg) != "abcdefghij" {
		t.Fatalf("done=%v msg=%q err=%v", done, msg, err)
	}
}

func TestBudgetRejectionLeavesNoTrace(t *testing.T) {
	r, _ := setup(10)
	if _, _, err := r.Submit("m", 0, []byte("abcdef"), 12); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.Submit("m", 6, []byte("ghijk"), 12); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("expected budget error, got %v", err)
	}
	if r.Used() != 6 {
		t.Fatalf("budget changed: %d", r.Used())
	}
	if info := r.Query("m"); info.Received != 6 {
		t.Fatalf("intervals changed: %+v", info)
	}
	// A fitting fragment still works afterwards.
	if _, _, err := r.Submit("m", 6, []byte("ghij"), 12); err != nil {
		t.Fatal(err)
	}
}

func TestQueryReportsRemaining(t *testing.T) {
	r, c := setup(100)
	if _, _, err := r.Submit("m", 0, []byte("ab"), 4); err != nil {
		t.Fatal(err)
	}
	c.Advance(10 * time.Second)
	info := r.Query("m")
	if info.Received != 2 || info.Complete || info.Remaining != 50*time.Second {
		t.Fatalf("bad info: %+v", info)
	}
}
