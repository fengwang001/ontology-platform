package record

import (
	"errors"
	"testing"
	"time"

	"ontology/digest"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestNewRecordIsInFlight(t *testing.T) {
	r := New(digest.Of([]byte("body")))
	if got := r.StateAt(base); got != StateInFlight {
		t.Fatalf("new record state = %v, want in-flight", got)
	}
	if got := r.StateAt(base.Add(1000 * time.Hour)); got != StateInFlight {
		t.Fatalf("in-flight record must never expire, got %v", got)
	}
}

func TestCompleteSuccess(t *testing.T) {
	r := New(digest.Of([]byte("body")))
	r.Complete([]byte("ok"), nil, base.Add(time.Minute))
	if got := r.StateAt(base); got != StateCompleted {
		t.Fatalf("state = %v, want completed", got)
	}
	res, err := r.Outcome()
	if err != nil || string(res) != "ok" {
		t.Fatalf("outcome = %q, %v; want ok, nil", res, err)
	}
}

func TestCompleteFailure(t *testing.T) {
	want := errors.New("boom")
	r := New(digest.Of([]byte("body")))
	r.Complete(nil, want, base.Add(time.Minute))
	if got := r.StateAt(base); got != StateFailed {
		t.Fatalf("state = %v, want failed", got)
	}
	if _, err := r.Outcome(); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestExpiryIsHalfOpen(t *testing.T) {
	r := New(digest.Of([]byte("body")))
	expiry := base.Add(time.Minute)
	r.Complete([]byte("ok"), nil, expiry)
	if got := r.StateAt(expiry.Add(-time.Nanosecond)); got != StateCompleted {
		t.Fatalf("just before expiry: state = %v, want completed", got)
	}
	if got := r.StateAt(expiry); got != StateExpired {
		t.Fatalf("exactly at expiry: state = %v, want expired (half-open)", got)
	}
	if got := r.StateAt(expiry.Add(time.Hour)); got != StateExpired {
		t.Fatalf("after expiry: state = %v, want expired", got)
	}
}

func TestRemaining(t *testing.T) {
	r := New(digest.Of([]byte("body")))
	if d := r.Remaining(base); d != 0 {
		t.Fatalf("in-flight remaining = %v, want 0", d)
	}
	r.Complete([]byte("ok"), nil, base.Add(time.Minute))
	if d := r.Remaining(base); d != time.Minute {
		t.Fatalf("remaining = %v, want 1m", d)
	}
	if d := r.Remaining(base.Add(2 * time.Minute)); d != 0 {
		t.Fatalf("expired remaining = %v, want 0", d)
	}
}

func TestWaitBlocksUntilComplete(t *testing.T) {
	r := New(digest.Of([]byte("body")))
	type out struct {
		res []byte
		err error
	}
	ch := make(chan out, 1)
	go func() {
		res, err := r.Wait()
		ch <- out{res, err}
	}()
	select {
	case <-ch:
		t.Fatal("Wait returned before Complete")
	default:
	}
	r.Complete([]byte("done"), nil, base.Add(time.Minute))
	got := <-ch
	if got.err != nil || string(got.res) != "done" {
		t.Fatalf("Wait = %q, %v; want done, nil", got.res, got.err)
	}
}

func TestStateString(t *testing.T) {
	want := map[State]string{
		StateUnknown:   "unknown",
		StateInFlight:  "in-flight",
		StateCompleted: "completed",
		StateFailed:    "failed",
		StateExpired:   "expired",
	}
	for s, w := range want {
		if s.String() != w {
			t.Fatalf("State(%d).String() = %q, want %q", int(s), s.String(), w)
		}
	}
}
