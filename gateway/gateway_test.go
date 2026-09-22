package gateway

import (
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/record"
)

// fakeClock is a manually advanced injected clock for tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
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

const testTTL = time.Minute

func newGateway() (*Gateway, *fakeClock) {
	clk := newFakeClock()
	return New(clk.now, testTTL), clk
}

func okExec(value string) ExecFunc {
	return func(body []byte) ([]byte, error) { return []byte(value), nil }
}

func TestExactlyOnceAndReplay(t *testing.T) {
	g, _ := newGateway()
	body := []byte(`{"op":"create"}`)
	first, err := g.Submit("k1", body, okExec("v1"))
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if first.Replayed {
		t.Fatal("first submit must not be marked replayed")
	}
	for i := 0; i < 5; i++ {
		out, err := g.Submit("k1", body, okExec("v-other"))
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if !out.Replayed {
			t.Fatalf("replay %d not marked replayed", i)
		}
		if string(out.Value) != string(first.Value) {
			t.Fatalf("replay %d = %q, want %q", i, out.Value, first.Value)
		}
	}
	if got := g.ExecCalls(); got != 1 {
		t.Fatalf("ExecCalls = %d, want exactly 1", got)
	}
}

func TestConflictDoesNotExecuteOrOverwrite(t *testing.T) {
	g, _ := newGateway()
	if _, err := g.Submit("k", []byte("body-a"), okExec("A")); err != nil {
		t.Fatal(err)
	}
	_, err := g.Submit("k", []byte("body-b"), okExec("B"))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if got := g.ExecCalls(); got != 1 {
		t.Fatalf("conflict must not execute, ExecCalls = %d", got)
	}
	out, err := g.Submit("k", []byte("body-a"), okExec("A"))
	if err != nil || string(out.Value) != "A" {
		t.Fatalf("stored result overwritten: %q, %v", out.Value, err)
	}
}

func TestFailureRetryableThenFixed(t *testing.T) {
	g, _ := newGateway()
	body := []byte("job")
	boom := errors.New("boom")
	calls := 0
	exec := func(b []byte) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, boom
		}
		return []byte("done"), nil
	}
	if _, err := g.Submit("k", body, exec); !errors.Is(err, boom) {
		t.Fatalf("first err = %v, want boom returned unchanged", err)
	}
	out, err := g.Submit("k", body, exec)
	if err != nil || out.Replayed || string(out.Value) != "done" {
		t.Fatalf("retry = %+v, %v; want fresh success", out, err)
	}
	if got := g.ExecCalls(); got != 2 {
		t.Fatalf("ExecCalls = %d, want 2 (failure + retry)", got)
	}
	out, err = g.Submit("k", body, exec)
	if err != nil || !out.Replayed || string(out.Value) != "done" {
		t.Fatalf("post-success = %+v, %v; want replay of success", out, err)
	}
	if got := g.ExecCalls(); got != 2 {
		t.Fatalf("success must be fixed, ExecCalls = %d", got)
	}
}

func TestExpiryBoundaryReexecutes(t *testing.T) {
	g, clk := newGateway()
	body := []byte("b")
	if _, err := g.Submit("k", body, okExec("v")); err != nil {
		t.Fatal(err)
	}
	clk.advance(testTTL - time.Nanosecond)
	out, _ := g.Submit("k", body, okExec("v"))
	if !out.Replayed {
		t.Fatal("just before expiry must still replay")
	}
	clk.advance(time.Nanosecond) // now exactly at expiry: half-open => expired
	out, err := g.Submit("k", body, okExec("v2"))
	if err != nil || out.Replayed {
		t.Fatalf("at expiry must re-execute: %+v, %v", out, err)
	}
	if got := g.ExecCalls(); got != 2 {
		t.Fatalf("ExecCalls = %d, want 2 after expiry", got)
	}
}

func TestInspect(t *testing.T) {
	g, clk := newGateway()
	if info := g.Inspect("nope"); info != (Info{}) {
		t.Fatalf("unknown key: %+v, want zero Info", info)
	}
	if _, err := g.Submit("k", []byte("b"), okExec("v")); err != nil {
		t.Fatal(err)
	}
	clk.advance(10 * time.Second)
	info := g.Inspect("k")
	if !info.Exists || info.State != record.StateCompleted || !info.HasResult {
		t.Fatalf("info = %+v, want completed with result", info)
	}
	if info.Remaining != testTTL-10*time.Second {
		t.Fatalf("remaining = %v, want %v", info.Remaining, testTTL-10*time.Second)
	}
	clk.advance(testTTL)
	if info := g.Inspect("k"); info != (Info{}) {
		t.Fatalf("expired key: %+v, want zero Info", info)
	}
}
