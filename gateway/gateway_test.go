package gateway

import (
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/record"
)

// fakeClock is the injected time source for tests: nothing in the
// gateway may read the wall clock, so tests move time explicitly.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

const testTTL = 10 * time.Second

func newGateway() (*Gateway, *fakeClock) {
	clock := newFakeClock()
	return New(clock.Now, testTTL), clock
}

func valueOf(v any) func() (any, error) {
	return func() (any, error) { return v, nil }
}

func TestExactlyOnceAndReplayDistinguishable(t *testing.T) {
	g, _ := newGateway()
	body := []byte(`{"op":"create"}`)

	first, err := g.Submit("k1", body, valueOf("v1"))
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if first.Replayed {
		t.Fatal("first submit must be marked as executed, not replayed")
	}
	for i := 0; i < 5; i++ {
		res, err := g.Submit("k1", body, valueOf("other"))
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if !res.Replayed {
			t.Fatalf("replay %d must be marked as replayed", i)
		}
		if res.Value != first.Value {
			t.Fatalf("replay %d value = %v, want %v", i, res.Value, first.Value)
		}
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("exec count = %d, want exactly 1", got)
	}
}

func TestConflictDoesNotExecuteNorOverwrite(t *testing.T) {
	g, _ := newGateway()
	if _, err := g.Submit("k1", []byte("body-A"), valueOf("vA")); err != nil {
		t.Fatalf("first submit: %v", err)
	}

	_, err := g.Submit("k1", []byte("body-B"), valueOf("vB"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("different body must fail with ErrConflict, got %v", err)
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("conflict must not execute, exec count = %d, want 1", got)
	}

	res, err := g.Submit("k1", []byte("body-A"), valueOf("unreachable"))
	if err != nil {
		t.Fatalf("replay after conflict: %v", err)
	}
	if !res.Replayed || res.Value != "vA" {
		t.Fatalf("conflict must not overwrite: got (%v, replayed=%v)", res.Value, res.Replayed)
	}
	if got := g.ExecCount(); got != 1 {
		t.Fatalf("exec count = %d, want 1", got)
	}
}

func TestFailureRememberedThenRetryableThenFixed(t *testing.T) {
	g, _ := newGateway()
	body := []byte("body")
	boom := errors.New("boom")

	_, err := g.Submit("k1", body, func() (any, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("failure must be returned as-is, got %v", err)
	}
	st, ok := g.Lookup("k1")
	if !ok || st.State != record.Failed || !st.HasResult {
		t.Fatalf("failed record must be queryable, got (%+v, %v)", st, ok)
	}

	// A failed key rejects a different body, same as a succeeded one.
	if _, err := g.Submit("k1", []byte("other"), valueOf("x")); !errors.Is(err, ErrConflict) {
		t.Fatalf("failed key + different body: got %v, want ErrConflict", err)
	}

	// Same key + same body retries and really executes again.
	res, err := g.Submit("k1", body, valueOf("recovered"))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if res.Replayed || res.Value != "recovered" {
		t.Fatalf("retry must execute, got (%v, replayed=%v)", res.Value, res.Replayed)
	}
	if got := g.ExecCount(); got != 2 {
		t.Fatalf("exec count = %d, want 2 (fail + retry)", got)
	}

	// Success is final: only replays from now on.
	res, err = g.Submit("k1", body, valueOf("unreachable"))
	if err != nil || !res.Replayed || res.Value != "recovered" {
		t.Fatalf("post-success submit must replay, got (%v, %v, replayed=%v)", res.Value, err, res.Replayed)
	}
	if got := g.ExecCount(); got != 2 {
		t.Fatalf("exec count = %d, want 2", got)
	}
}

func TestExpiryBoundaryIsLeftClosed(t *testing.T) {
	g, clock := newGateway()
	body := []byte("body")

	if _, err := g.Submit("k1", body, valueOf("v1")); err != nil {
		t.Fatalf("first submit: %v", err)
	}

	clock.Advance(testTTL - time.Nanosecond)
	res, err := g.Submit("k1", body, valueOf("unreachable"))
	if err != nil || !res.Replayed || res.Value != "v1" {
		t.Fatalf("just before expiry must replay, got (%v, %v, replayed=%v)", res.Value, err, res.Replayed)
	}

	// now == expiresAt already counts as expired: a brand-new request.
	clock.Advance(time.Nanosecond)
	res, err = g.Submit("k1", body, valueOf("v2"))
	if err != nil || res.Replayed || res.Value != "v2" {
		t.Fatalf("at expiry must re-execute, got (%v, %v, replayed=%v)", res.Value, err, res.Replayed)
	}
	if got := g.ExecCount(); got != 2 {
		t.Fatalf("exec count = %d, want 2", got)
	}
}

func TestLookupZeroValueForMissingOrExpired(t *testing.T) {
	g, clock := newGateway()

	st, ok := g.Lookup("nope")
	if ok || st != (Status{}) {
		t.Fatalf("missing key must return zero value + false, got (%+v, %v)", st, ok)
	}

	if _, err := g.Submit("k1", []byte("b"), valueOf("v")); err != nil {
		t.Fatalf("submit: %v", err)
	}
	st, ok = g.Lookup("k1")
	if !ok || st.State != record.Completed || !st.HasResult || st.Remaining != testTTL {
		t.Fatalf("live key status = (%+v, %v)", st, ok)
	}

	clock.Advance(testTTL)
	st, ok = g.Lookup("k1")
	if ok || st != (Status{}) {
		t.Fatalf("expired key must return zero value + false, got (%+v, %v)", st, ok)
	}
}
