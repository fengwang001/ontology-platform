package gateway

import (
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/record"
)

// fakeClock is a manually advanced clock for tests.
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

const testTTL = time.Minute

func newGateway() (*Gateway, *fakeClock) {
	clock := newFakeClock()
	return New(clock.Now, testTTL), clock
}

func okExec(value any) ExecFunc {
	return func() (any, error) { return value, nil }
}

func TestExactlyOnceAndReplay(t *testing.T) {
	g, _ := newGateway()
	body := []byte(`{"op":"create"}`)
	first, err := g.Submit("k1", body, okExec("v1"))
	if err != nil {
		t.Fatalf("first submit err = %v", err)
	}
	if first.Replayed {
		t.Fatal("first submit must not be marked replayed")
	}
	for i := 0; i < 5; i++ {
		res, err := g.Submit("k1", body, okExec("other"))
		if err != nil {
			t.Fatalf("replay %d err = %v", i, err)
		}
		if !res.Replayed {
			t.Fatalf("replay %d must be marked replayed", i)
		}
		if res.Value != first.Value {
			t.Fatalf("replay %d value = %v, want %v", i, res.Value, first.Value)
		}
	}
	if n := g.ExecCount(); n != 1 {
		t.Fatalf("ExecCount = %d, want 1", n)
	}
}

func TestConflictingBodyRejected(t *testing.T) {
	g, _ := newGateway()
	if _, err := g.Submit("k1", []byte("a"), okExec("va")); err != nil {
		t.Fatalf("first submit err = %v", err)
	}
	_, err := g.Submit("k1", []byte("b"), okExec("vb"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict err = %v, want ErrConflict", err)
	}
	if n := g.ExecCount(); n != 1 {
		t.Fatalf("conflict must not execute, ExecCount = %d, want 1", n)
	}
	res, err := g.Submit("k1", []byte("a"), okExec("other"))
	if err != nil || !res.Replayed || res.Value != "va" {
		t.Fatalf("stored result overwritten: res = %+v, err = %v", res, err)
	}
}

func TestFailureIsRetryableAndSuccessIsFinal(t *testing.T) {
	g, _ := newGateway()
	body := []byte("flaky")
	boom := errors.New("boom")

	_, err := g.Submit("k1", body, func() (any, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("failure must be returned as-is, got %v", err)
	}
	if n := g.ExecCount(); n != 1 {
		t.Fatalf("ExecCount = %d, want 1", n)
	}

	res, err := g.Submit("k1", body, okExec("recovered"))
	if err != nil || res.Replayed || res.Value != "recovered" {
		t.Fatalf("retry res = %+v, err = %v, want fresh success", res, err)
	}
	if n := g.ExecCount(); n != 2 {
		t.Fatalf("retry must re-execute, ExecCount = %d, want 2", n)
	}

	res, err = g.Submit("k1", body, okExec("other"))
	if err != nil || !res.Replayed || res.Value != "recovered" {
		t.Fatalf("success must be fixed, res = %+v, err = %v", res, err)
	}
	if n := g.ExecCount(); n != 2 {
		t.Fatalf("success must not re-execute, ExecCount = %d, want 2", n)
	}
}

func TestQueryMissingKeyIsZero(t *testing.T) {
	g, _ := newGateway()
	st := g.Query("nope")
	if st != (Status{}) {
		t.Fatalf("missing key status = %+v, want zero", st)
	}
}

func TestQueryReportsLiveRecord(t *testing.T) {
	g, clock := newGateway()
	if _, err := g.Submit("k1", []byte("a"), okExec("v")); err != nil {
		t.Fatalf("submit err = %v", err)
	}
	clock.Advance(10 * time.Second)
	st := g.Query("k1")
	if !st.Exists || st.State != record.StateCompleted || !st.HasResult {
		t.Fatalf("status = %+v, want completed with result", st)
	}
	if st.Remaining != testTTL-10*time.Second {
		t.Fatalf("remaining = %v, want %v", st.Remaining, testTTL-10*time.Second)
	}
}
