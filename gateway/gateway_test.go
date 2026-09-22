package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/record"
)

// fakeClock is a manually advanced clock for deterministic TTL tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var errBoom = errors.New("boom")

func okExec(result string) ExecuteFunc {
	return func(context.Context, []byte) ([]byte, error) { return []byte(result), nil }
}

func TestExactlyOnceAndReplay(t *testing.T) {
	g := New(newFakeClock().now, time.Minute)
	body := []byte(`{"op":"create"}`)

	first := g.Submit(context.Background(), "k1", body, okExec("r1"))
	if first.Err != nil || first.Replayed || string(first.Result) != "r1" {
		t.Fatalf("first submit = %+v", first)
	}
	for i := 0; i < 5; i++ {
		out := g.Submit(context.Background(), "k1", body, okExec("other"))
		if !out.Replayed {
			t.Fatal("repeat submit must be marked as replay")
		}
		if out.Err != nil || string(out.Result) != "r1" {
			t.Fatalf("replay must equal first result, got %+v", out)
		}
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("ExecCount = %d, want 1", got)
	}
}

func TestConflictDoesNotExecuteNorOverwrite(t *testing.T) {
	g := New(newFakeClock().now, time.Minute)
	g.Submit(context.Background(), "k1", []byte("body-a"), okExec("r1"))

	execs := 0
	out := g.Submit(context.Background(), "k1", []byte("body-b"),
		func(context.Context, []byte) ([]byte, error) { execs++; return nil, nil })
	if !errors.Is(out.Err, ErrConflict) {
		t.Fatalf("err = %v, want conflict", out.Err)
	}
	var ce *ConflictError
	if !errors.As(out.Err, &ce) || ce.Key != "k1" {
		t.Fatalf("err must be *ConflictError for k1, got %T", out.Err)
	}
	if execs != 0 || g.ExecCount() != 1 {
		t.Fatalf("conflict must not execute: execs=%d total=%d", execs, g.ExecCount())
	}
	again := g.Submit(context.Background(), "k1", []byte("body-a"), okExec("x"))
	if !again.Replayed || string(again.Result) != "r1" {
		t.Fatalf("stored result must survive conflict, got %+v", again)
	}
}

func TestFailureIsRetryableThenSuccessSticks(t *testing.T) {
	g := New(newFakeClock().now, time.Minute)
	ctx := context.Background()

	fail := g.Submit(ctx, "k1", []byte("b"),
		func(context.Context, []byte) ([]byte, error) { return nil, errBoom })
	if !errors.Is(fail.Err, errBoom) || fail.Replayed {
		t.Fatalf("failure must be returned as-is, got %+v", fail)
	}
	ok := g.Submit(ctx, "k1", []byte("b"), okExec("r1"))
	if ok.Err != nil || ok.Replayed {
		t.Fatalf("retry after failure must really execute, got %+v", ok)
	}
	if got := g.ExecCount(); got != 2 {
		t.Fatalf("ExecCount = %d, want 2 (fail + retry)", got)
	}
	replay := g.Submit(ctx, "k1", []byte("b"), okExec("x"))
	if !replay.Replayed || string(replay.Result) != "r1" {
		t.Fatalf("success must stick, got %+v", replay)
	}
	if got := g.ExecCount(); got != 2 {
		t.Fatalf("ExecCount after success = %d, want 2", got)
	}
}

func TestExpiryBoundaryAndReexecution(t *testing.T) {
	clock := newFakeClock()
	g := New(clock.now, time.Minute)
	ctx := context.Background()

	g.Submit(ctx, "k1", []byte("b"), okExec("r1"))
	clock.advance(time.Minute - time.Nanosecond)
	st := g.Inspect("k1")
	if !st.Exists || st.State != record.StateCompleted {
		t.Fatalf("just before expiry must still exist: %+v", st)
	}
	clock.advance(time.Nanosecond) // exactly at expiry moment
	if st := g.Inspect("k1"); st != (Status{}) {
		t.Fatalf("at expiry must be gone, got %+v", st)
	}
	out := g.Submit(ctx, "k1", []byte("b"), okExec("r2"))
	if out.Replayed || string(out.Result) != "r2" {
		t.Fatalf("post-expiry submit must really execute, got %+v", out)
	}
	if got := g.ExecCount(); got != 2 {
		t.Fatalf("ExecCount = %d, want 2", got)
	}
}

func TestInspectMissingKeyIsZero(t *testing.T) {
	g := New(newFakeClock().now, time.Minute)
	if st := g.Inspect("nope"); st != (Status{}) {
		t.Fatalf("missing key must be zero Status, got %+v", st)
	}
}

func TestInspectReportsStateAndTTL(t *testing.T) {
	clock := newFakeClock()
	g := New(clock.now, time.Minute)
	g.Submit(context.Background(), "k1", []byte("b"), okExec("r1"))
	clock.advance(10 * time.Second)
	st := g.Inspect("k1")
	if !st.Exists || st.State != record.StateCompleted || !st.HasResult {
		t.Fatalf("bad status: %+v", st)
	}
	if st.Remaining != 50*time.Second {
		t.Fatalf("remaining = %v, want 50s", st.Remaining)
	}
}
