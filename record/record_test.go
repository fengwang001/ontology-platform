package record

import (
	"errors"
	"testing"
	"time"

	"ontology/digest"
)

var base = time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)

func fpOf(s string) digest.Fingerprint { return digest.Of([]byte(s)) }

func TestNewRecordIsInFlight(t *testing.T) {
	r := New(fpOf("a"), base, time.Minute)
	if r.State() != StateInFlight {
		t.Fatalf("state = %v, want in-flight", r.State())
	}
	if r.Expired(base.Add(10*time.Minute)) {
		t.Fatal("in-flight record must never be expired")
	}
}

func TestCompleteSuccess(t *testing.T) {
	r := New(fpOf("a"), base, time.Minute)
	r.Complete([]byte("ok"), nil)
	res, err, st := r.Result()
	if st != StateCompleted || err != nil || string(res) != "ok" {
		t.Fatalf("got (%q, %v, %v)", res, err, st)
	}
}

func TestCompleteFailure(t *testing.T) {
	r := New(fpOf("a"), base, time.Minute)
	want := errors.New("boom")
	r.Complete(nil, want)
	_, err, st := r.Result()
	if st != StateFailed || !errors.Is(err, want) {
		t.Fatalf("got (%v, %v)", err, st)
	}
}

func TestCompleteIsIdempotent(t *testing.T) {
	r := New(fpOf("a"), base, time.Minute)
	r.Complete([]byte("first"), nil)
	r.Complete([]byte("second"), nil)
	res, _, _ := r.Result()
	if string(res) != "first" {
		t.Fatalf("second Complete must be a no-op, got %q", res)
	}
}

func TestExpiryBoundaryLeftClosedRightOpen(t *testing.T) {
	r := New(fpOf("a"), base, time.Minute)
	r.Complete([]byte("ok"), nil)
	if r.Expired(base.Add(time.Minute - time.Nanosecond)) {
		t.Fatal("just before expiry must not be expired")
	}
	if !r.Expired(base.Add(time.Minute)) {
		t.Fatal("exactly at expiry must be expired (left-closed right-open)")
	}
}

func TestWaitReleasesAfterComplete(t *testing.T) {
	r := New(fpOf("a"), base, time.Minute)
	done := make(chan struct{})
	go func() {
		r.Wait()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Wait returned before Complete")
	case <-time.After(20 * time.Millisecond):
	}
	r.Complete([]byte("ok"), nil)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after Complete")
	}
}

func TestRemainingClampsAtZero(t *testing.T) {
	r := New(fpOf("a"), base, time.Minute)
	if got := r.Remaining(base.Add(30 * time.Second)); got != 30*time.Second {
		t.Fatalf("remaining = %v", got)
	}
	if got := r.Remaining(base.Add(2 * time.Minute)); got != 0 {
		t.Fatalf("remaining past expiry = %v, want 0", got)
	}
}

func TestStateString(t *testing.T) {
	for s, want := range map[State]string{
		StateInFlight:  "in-flight",
		StateCompleted: "completed",
		StateFailed:    "failed",
		StateExpired:   "expired",
	} {
		if s.String() != want {
			t.Fatalf("State(%d).String() = %q, want %q", s, s.String(), want)
		}
	}
}
