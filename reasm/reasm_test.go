package reasm

import (
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/budget"
	"ontology/frag"
)

// clock is a manually advanced, lock-protected injected clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestOutOfOrderDeliveredOnceByteExact(t *testing.T) {
	clk := newClock()
	r := New(1<<20, time.Minute, clk.Now)
	msg := []byte("hello ontology")
	parts := [][2]int{{7, 14}, {0, 5}, {5, 7}} // out of order
	deliveries := 0
	var got []byte
	for i, p := range parts {
		b, done, err := r.Submit("m1", p[0], msg[p[0]:p[1]], len(msg))
		if err != nil {
			t.Fatalf("part %d: %v", i, err)
		}
		if done {
			deliveries++
			got = b
		}
	}
	if deliveries != 1 {
		t.Fatalf("deliveries = %d, want exactly 1", deliveries)
	}
	if string(got) != string(msg) {
		t.Fatalf("delivered %q, want %q", got, msg)
	}
	if used := r.Used(); used != 0 {
		t.Fatalf("used after delivery = %d, want 0", used)
	}
}

func TestDuplicateFragmentIdempotentAndUncharged(t *testing.T) {
	clk := newClock()
	r := New(1<<20, time.Minute, clk.Now)
	if _, _, err := r.Submit("m", 0, []byte("abc"), 6); err != nil {
		t.Fatal(err)
	}
	usedBefore := r.Used()
	stBefore := r.Status("m")
	if _, done, err := r.Submit("m", 0, []byte("abc"), 6); err != nil || done {
		t.Fatalf("duplicate: done=%v err=%v", done, err)
	}
	if r.Used() != usedBefore {
		t.Fatalf("used changed by duplicate: %d -> %d", usedBefore, r.Used())
	}
	if st := r.Status("m"); st.Received != stBefore.Received {
		t.Fatalf("received changed by duplicate: %d -> %d", stBefore.Received, st.Received)
	}
}

func TestTotalMismatchDistinctError(t *testing.T) {
	clk := newClock()
	r := New(1<<20, time.Minute, clk.Now)
	if _, _, err := r.Submit("m", 0, []byte("ab"), 4); err != nil {
		t.Fatal(err)
	}
	_, _, err := r.Submit("m", 2, []byte("cd"), 8)
	if !errors.Is(err, ErrTotalMismatch) {
		t.Fatalf("err = %v, want ErrTotalMismatch", err)
	}
	for _, other := range []error{frag.ErrEmptyData, frag.ErrZeroTotal, frag.ErrOutOfBounds, budget.ErrExhausted} {
		if errors.Is(err, other) {
			t.Fatalf("ErrTotalMismatch must be distinct from %v", other)
		}
	}
}

func TestEvictionAtExactDeadlineReleasesBudget(t *testing.T) {
	clk := newClock()
	r := New(1<<20, 10*time.Second, clk.Now)
	if _, _, err := r.Submit("m", 0, []byte("ab"), 4); err != nil {
		t.Fatal(err)
	}
	if used := r.Used(); used != 2 {
		t.Fatalf("used = %d, want 2", used)
	}
	clk.Advance(9 * time.Second)
	if st := r.Status("m"); st.Received != 2 {
		t.Fatalf("before deadline: received = %d, want 2", st.Received)
	}
	clk.Advance(1 * time.Second) // now == deadline: left-closed expiry
	if st := r.Status("m"); st != (Status{}) {
		t.Fatalf("at deadline: status = %+v, want zero", st)
	}
	if used := r.Used(); used != 0 {
		t.Fatalf("used after eviction = %d, want 0", used)
	}
}

func TestEvictedIDRestartsAsNewMessage(t *testing.T) {
	clk := newClock()
	r := New(1<<20, 10*time.Second, clk.Now)
	if _, _, err := r.Submit("m", 0, []byte("ab"), 4); err != nil {
		t.Fatal(err)
	}
	clk.Advance(10 * time.Second)
	b, done, err := r.Submit("m", 0, []byte("xy"), 4)
	if err != nil {
		t.Fatalf("resubmit after eviction: %v", err)
	}
	if done || b != nil {
		t.Fatalf("partial resubmit delivered: done=%v", done)
	}
	st := r.Status("m")
	if st.Received != 2 || st.Remaining != 10*time.Second {
		t.Fatalf("status = %+v, want fresh message", st)
	}
}
