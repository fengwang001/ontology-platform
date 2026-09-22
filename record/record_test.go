package record

import (
	"errors"
	"testing"
	"time"

	"ontology/digest"
)

var (
	base   = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	ttl    = 10 * time.Second
	bodyFP = digest.OfString("body")
)

func newRecord() *Record {
	return New(bodyFP, base.Add(ttl))
}

func TestNewRecordIsInFlight(t *testing.T) {
	r := newRecord()
	if got := r.StateAt(base); got != InFlight {
		t.Fatalf("new record state = %v, want in-flight", got)
	}
	if r.HasResult() {
		t.Fatal("new record must not have a result")
	}
	if !r.Matches(bodyFP) {
		t.Fatal("record must match its own fingerprint")
	}
	if r.Matches(digest.OfString("other")) {
		t.Fatal("record must not match a different fingerprint")
	}
}

func TestCompleteStoresResult(t *testing.T) {
	r := newRecord()
	r.Complete("done")
	if got := r.StateAt(base); got != Completed {
		t.Fatalf("state = %v, want completed", got)
	}
	if !r.HasResult() {
		t.Fatal("completed record must have a result")
	}
	v, err := r.Result()
	if v != "done" || err != nil {
		t.Fatalf("result = (%v, %v), want (done, nil)", v, err)
	}
}

func TestFailStoresError(t *testing.T) {
	r := newRecord()
	want := errors.New("boom")
	r.Fail(want)
	if got := r.StateAt(base); got != Failed {
		t.Fatalf("state = %v, want failed", got)
	}
	if !r.HasResult() {
		t.Fatal("failed record must still have a result")
	}
	if _, err := r.Result(); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestExpiryIsLeftClosedRightOpen(t *testing.T) {
	r := newRecord()
	r.Complete("done")
	if got := r.StateAt(base.Add(ttl).Add(-time.Nanosecond)); got != Completed {
		t.Fatalf("just before deadline state = %v, want completed", got)
	}
	if got := r.StateAt(base.Add(ttl)); got != Expired {
		t.Fatalf("at deadline state = %v, want expired (left-closed)", got)
	}
	if got := r.StateAt(base.Add(ttl).Add(time.Hour)); got != Expired {
		t.Fatalf("after deadline state = %v, want expired", got)
	}
}

func TestInFlightNeverExpires(t *testing.T) {
	r := newRecord()
	if got := r.StateAt(base.Add(1000 * time.Hour)); got != InFlight {
		t.Fatalf("in-flight state far past deadline = %v, want in-flight", got)
	}
}

func TestWaitUnblocksWithOutcome(t *testing.T) {
	r := newRecord()
	got := make(chan any, 1)
	go func() {
		v, err := r.Wait()
		if err != nil {
			t.Errorf("wait error = %v, want nil", err)
		}
		got <- v
	}()
	r.Complete(42)
	if v := <-got; v != 42 {
		t.Fatalf("wait returned %v, want 42", v)
	}
}

func TestRemainingClampsAtZero(t *testing.T) {
	r := newRecord()
	if got := r.Remaining(base.Add(3 * time.Second)); got != 7*time.Second {
		t.Fatalf("remaining = %v, want 7s", got)
	}
	if got := r.Remaining(base.Add(time.Hour)); got != 0 {
		t.Fatalf("remaining past deadline = %v, want 0", got)
	}
}
