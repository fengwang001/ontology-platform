package lease_test

import (
	"errors"
	"testing"
	"time"

	"ontology/internal/testclock"
	"ontology/lease"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const ttl = 10 * time.Second

func newLease() (*lease.Lease, *testclock.Clock) {
	c := testclock.New(t0)
	return lease.New(c.Now), c
}

func TestAcquireMutualExclusion(t *testing.T) {
	l, _ := newLease()
	if _, err := l.Acquire("alice", ttl); err != nil {
		t.Fatal(err)
	}
	_, err := l.Acquire("bob", ttl)
	if !errors.Is(err, lease.ErrHeld) {
		t.Fatalf("got %v, want ErrHeld", err)
	}
	var held *lease.HeldError
	if !errors.As(err, &held) {
		t.Fatal("error is not *HeldError")
	}
	if held.Holder != "alice" || held.Remaining != ttl {
		t.Fatalf("HeldError = %+v, want holder alice remaining %s", held, ttl)
	}
}

func TestRenewOnlyByHolder(t *testing.T) {
	l, c := newLease()
	if _, err := l.Acquire("alice", ttl); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Second)
	if err := l.Renew("bob", ttl); !errors.Is(err, lease.ErrNotHolder) {
		t.Fatalf("got %v, want ErrNotHolder", err)
	}
	// 到期时间不得被非持有者改变。
	if got := l.Status().Remaining; got != ttl-time.Second {
		t.Fatalf("remaining = %s, want %s", got, ttl-time.Second)
	}
}

func TestRenewIsNowPlusTTLNotAccumulate(t *testing.T) {
	l, c := newLease()
	if _, err := l.Acquire("alice", ttl); err != nil {
		t.Fatal(err)
	}
	c.Advance(6 * time.Second)
	if err := l.Renew("alice", ttl); err != nil {
		t.Fatal(err)
	}
	// now+ttl = 剩余 10s，而不是累加的 14s。
	if got := l.Status().Remaining; got != ttl {
		t.Fatalf("remaining = %s, want %s", got, ttl)
	}
}

func TestRenewKeepsToken(t *testing.T) {
	l, c := newLease()
	tok, err := l.Acquire("alice", ttl)
	if err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Second)
	if err := l.Renew("alice", ttl); err != nil {
		t.Fatal(err)
	}
	if got := l.Status().Token; got != tok {
		t.Fatalf("token changed on renew: %d -> %d", tok, got)
	}
}

func TestExpiryBoundaryIsExclusive(t *testing.T) {
	l, c := newLease()
	if _, err := l.Acquire("alice", ttl); err != nil {
		t.Fatal(err)
	}
	c.Advance(ttl - time.Nanosecond)
	if !l.Status().Held {
		t.Fatal("should still be held just before expiry")
	}
	// now 恰好等于到期时间即过期，他人立刻可抢。
	c.Advance(time.Nanosecond)
	if l.Status().Held {
		t.Fatal("should be expired exactly at expiry")
	}
	if _, err := l.Acquire("bob", ttl); err != nil {
		t.Fatalf("bob should acquire at expiry boundary: %v", err)
	}
}

func TestTokenStrictlyIncreasesAcrossRounds(t *testing.T) {
	l, c := newLease()
	prev, err := l.Acquire("alice", ttl)
	if err != nil {
		t.Fatal(err)
	}
	// 过期后被他人抢走。
	c.Advance(ttl)
	next, err := l.Acquire("bob", ttl)
	if err != nil {
		t.Fatal(err)
	}
	if next <= prev {
		t.Fatalf("token did not increase: %d -> %d", prev, next)
	}
	// 同一客户端释放后重新拿到，令牌也必须更大。
	prev = next
	if err := l.Release("bob"); err != nil {
		t.Fatal(err)
	}
	next, err = l.Acquire("bob", ttl)
	if err != nil {
		t.Fatal(err)
	}
	if next <= prev {
		t.Fatalf("token did not increase after re-acquire: %d -> %d", prev, next)
	}
}

func TestReleaseSemantics(t *testing.T) {
	l, _ := newLease()
	if _, err := l.Acquire("alice", ttl); err != nil {
		t.Fatal(err)
	}
	if err := l.Release("bob"); !errors.Is(err, lease.ErrNotHolder) {
		t.Fatalf("non-holder release: got %v, want ErrNotHolder", err)
	}
	if !l.Status().Held {
		t.Fatal("state must be unchanged after non-holder release")
	}
	if err := l.Release("alice"); err != nil {
		t.Fatal(err)
	}
	// 重复 Release 只有第一次成功。
	if err := l.Release("alice"); !errors.Is(err, lease.ErrNotHeld) {
		t.Fatalf("second release: got %v, want ErrNotHeld", err)
	}
	// 释放后他人立刻可抢。
	if _, err := l.Acquire("bob", ttl); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestExpiredLeaseCannotBeRevived(t *testing.T) {
	l, c := newLease()
	old, err := l.Acquire("alice", ttl)
	if err != nil {
		t.Fatal(err)
	}
	c.Advance(ttl + time.Second)
	if err := l.Renew("alice", ttl); !errors.Is(err, lease.ErrNotHeld) {
		t.Fatalf("renew after expiry: got %v, want ErrNotHeld", err)
	}
	// 必须重新 Acquire，且拿到更大的新令牌。
	tok, err := l.Acquire("alice", ttl)
	if err != nil {
		t.Fatal(err)
	}
	if tok <= old {
		t.Fatalf("re-acquire token %d not greater than %d", tok, old)
	}
}

func TestStatusZeroValuesWhenNotHeld(t *testing.T) {
	l, c := newLease()
	st := l.Status()
	if st.Held || st.Holder != "" || st.Remaining != 0 || st.Token != 0 {
		t.Fatalf("fresh status = %+v, want zero values", st)
	}
	if _, err := l.Acquire("alice", ttl); err != nil {
		t.Fatal(err)
	}
	c.Advance(ttl)
	st = l.Status()
	if st.Held || st.Holder != "" || st.Remaining != 0 {
		t.Fatalf("expired status = %+v, holder/remaining must be zero", st)
	}
}
