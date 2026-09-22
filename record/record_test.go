package record

import (
	"errors"
	"testing"
	"time"

	"ontology/digest"
)

var (
	t0  = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	ttl = time.Minute
)

func newRecord(body string) *Record {
	return New(digest.Of([]byte(body)), t0, t0.Add(ttl))
}

func TestNewRecordIsInFlight(t *testing.T) {
	r := newRecord("a")
	if got := r.StateAt(t0); got != StateInFlight {
		t.Fatalf("new record state = %v, want in-flight", got)
	}
	if r.HasResult() {
		t.Fatal("new record must not have a result")
	}
	if !r.MatchesBody(digest.Of([]byte("a"))) {
		t.Fatal("record must match its opening fingerprint")
	}
	if r.MatchesBody(digest.Of([]byte("b"))) {
		t.Fatal("record must not match a different fingerprint")
	}
}

func TestFinishSuccess(t *testing.T) {
	r := newRecord("a")
	r.Finish("done", nil)
	if got := r.StateAt(t0); got != StateCompleted {
		t.Fatalf("state = %v, want completed", got)
	}
	if !r.HasResult() {
		t.Fatal("completed record must have a result")
	}
	v, err := r.Await()
	if v != "done" || err != nil {
		t.Fatalf("Await = (%v, %v), want (done, nil)", v, err)
	}
}

func TestFinishFailure(t *testing.T) {
	r := newRecord("a")
	boom := errors.New("boom")
	r.Finish(nil, boom)
	if got := r.StateAt(t0); got != StateFailed {
		t.Fatalf("state = %v, want failed", got)
	}
	if !r.HasResult() {
		t.Fatal("failed record must have a result")
	}
	if _, err := r.Await(); !errors.Is(err, boom) {
		t.Fatalf("Await err = %v, want boom", err)
	}
}

func TestExpiryBoundaryIsLeftClosedRightOpen(t *testing.T) {
	r := newRecord("a")
	r.Finish("done", nil)
	if got := r.StateAt(t0.Add(ttl).Add(-time.Nanosecond)); got != StateCompleted {
		t.Fatalf("just before expiry state = %v, want completed", got)
	}
	if got := r.StateAt(t0.Add(ttl)); got != StateExpired {
		t.Fatalf("at exact expiry state = %v, want expired", got)
	}
	if got := r.StateAt(t0.Add(ttl).Add(time.Nanosecond)); got != StateExpired {
		t.Fatalf("after expiry state = %v, want expired", got)
	}
}

func TestInFlightNeverExpires(t *testing.T) {
	r := newRecord("a")
	if got := r.StateAt(t0.Add(1000 * ttl)); got != StateInFlight {
		t.Fatalf("in-flight state at far future = %v, want in-flight", got)
	}
}

func TestRemaining(t *testing.T) {
	r := newRecord("a")
	if got := r.Remaining(t0); got != ttl {
		t.Fatalf("remaining at start = %v, want %v", got, ttl)
	}
	if got := r.Remaining(t0.Add(ttl)); got != 0 {
		t.Fatalf("remaining at expiry = %v, want 0", got)
	}
}

func TestAwaitWakesWaiters(t *testing.T) {
	r := newRecord("a")
	const waiters = 8
	got := make(chan any, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			v, _ := r.Await()
			got <- v
		}()
	}
	r.Finish("shared", nil)
	for i := 0; i < waiters; i++ {
		if v := <-got; v != "shared" {
			t.Fatalf("waiter got %v, want shared", v)
		}
	}
}

func TestFinishTwicePanics(t *testing.T) {
	r := newRecord("a")
	r.Finish("x", nil)
	defer func() {
		if recover() == nil {
			t.Fatal("second Finish must panic")
		}
	}()
	r.Finish("y", nil)
}
