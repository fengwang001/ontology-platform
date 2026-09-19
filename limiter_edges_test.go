package ontology

import (
	"errors"
	"testing"
	"time"
)

func TestClockBackwardIsRejectedWithoutChangingBucket(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(1)
	if err := l.AddTenant("t", Config{Capacity: 2, Rate: 1}, base); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.Allow("t", 2, base); !ok || err != nil {
		t.Fatalf("drain = (%v, %v)", ok, err)
	}

	if _, err := l.AvailableTokens("t", base.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Allow("t", 1, base.Add(time.Second-time.Nanosecond)); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("backward Allow = %v", err)
	}
	if _, err := l.AvailableTokens("t", base.Add(time.Second-time.Nanosecond)); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("backward query = %v", err)
	}

	ok, err := l.Allow("t", 1, base.Add(2*time.Second))
	if !ok || err != nil {
		t.Fatalf("state after backward rejection = (%v, %v)", ok, err)
	}
}

func TestSameTimestampDoesNotRefillOrMutateTwice(t *testing.T) {
	base := time.Unix(0, 0)
	now := base.Add(time.Second)
	l := NewLimiter(1)
	if err := l.AddTenant("t", Config{Capacity: 1, Rate: 1}, base); err != nil {
		t.Fatal(err)
	}

	ok, err := l.Allow("t", 1, base)
	if !ok || err != nil {
		t.Fatal(err)
	}
	ok, err = l.Allow("t", 1, now)
	if !ok || err != nil {
		t.Fatalf("first at timestamp = (%v, %v)", ok, err)
	}
	if ok, err := l.Allow("t", 1, now); ok || !errors.Is(err, ErrTokensUnavailable) {
		t.Fatalf("repeat same time = (%v, %v)", ok, err)
	}
	got, err := l.AvailableTokens("t", now)
	if err != nil || got != 0 {
		t.Fatalf("repeat state = %d, %v", got, err)
	}
}

func TestRequestBoundaries(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(1)
	if err := l.AddTenant("t", Config{Capacity: 2, Rate: 1}, base); err != nil {
		t.Fatal(err)
	}

	before, err := l.AvailableTokens("t", base)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := l.Allow("missing", 0, base); !ok || err != nil {
		t.Fatalf("zero request for unknown tenant = (%v, %v)", ok, err)
	}
	after, err := l.AvailableTokens("t", base)
	if err != nil || before != after {
		t.Fatalf("zero changed tokens: before %d after %d, %v", before, after, err)
	}
	if _, err := l.Allow("t", -1, base); !errors.Is(err, ErrNegativeTokens) {
		t.Fatalf("negative = %v", err)
	}
	if _, err := l.Allow("t", 3, base); !errors.Is(err, ErrRequestExceedsCapacity) {
		t.Fatalf("over capacity = %v", err)
	}
	if ok, err := l.Allow("t", 2, base); !ok || err != nil {
		t.Fatalf("exact capacity = (%v, %v)", ok, err)
	}
}

func TestDeniedWaitIsExactAndDoesNotConsume(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(1)
	if err := l.AddTenant("t", Config{Capacity: 2, Rate: 1}, base); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.Allow("t", 2, base); !ok || err != nil {
		t.Fatal(err)
	}

	requestAt := base.Add(100 * time.Millisecond)
	_, err := l.Allow("t", 2, requestAt)
	denied, ok := AsDenied(err)
	if !ok {
		t.Fatalf("denied type = %T", err)
	}
	if denied.Requested != 2 || denied.Available != 0 || denied.Wait != 1900*time.Millisecond {
		t.Fatalf("denied = %+v", denied)
	}
	if ok, _ := l.Allow("t", 2, requestAt.Add(denied.Wait-time.Nanosecond)); ok {
		t.Fatal("request succeeded one nanosecond before reported retry time")
	}
	if ok, err := l.Allow("t", 2, requestAt.Add(denied.Wait)); !ok || err != nil {
		t.Fatalf("request at reported retry time = (%v, %v)", ok, err)
	}
}
