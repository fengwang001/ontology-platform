package lockd_test

import (
	"errors"
	"testing"
	"time"

	"ontology/fence"
	"ontology/internal/testclock"
	"ontology/lease"
	"ontology/lockd"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const ttl = 10 * time.Second

func newDaemon() (*lockd.Daemon, *testclock.Clock) {
	c := testclock.New(t0)
	return lockd.New(c.Now), c
}

func TestWriteRejectsStaleAndNotHeldSeparately(t *testing.T) {
	d, c := newDaemon()
	// 无人持有：ErrNotHeld，且不是 ErrStaleToken。
	err := d.Write("res", 1, []byte("x"))
	if !errors.Is(err, lockd.ErrNotHeld) || errors.Is(err, lockd.ErrStaleToken) {
		t.Fatalf("write with no holder: got %v, want ErrNotHeld only", err)
	}
	tokA, err := d.Acquire("res", "alice", ttl)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Write("res", tokA, []byte("a")); err != nil {
		t.Fatalf("holder write should pass: %v", err)
	}
	// alice 过期，bob 抢到更大令牌。
	c.Advance(ttl)
	tokB, err := d.Acquire("res", "bob", ttl)
	if err != nil {
		t.Fatal(err)
	}
	// 旧持有者的过期令牌：ErrStaleToken，且不是 ErrNotHeld。
	err = d.Write("res", tokA, []byte("stale"))
	if !errors.Is(err, lockd.ErrStaleToken) || errors.Is(err, lockd.ErrNotHeld) {
		t.Fatalf("stale write: got %v, want ErrStaleToken only", err)
	}
	if err := d.Write("res", tokB, []byte("b")); err != nil {
		t.Fatalf("current holder write should pass: %v", err)
	}
}

func TestWriteWatermarkNeverRegresses(t *testing.T) {
	d, c := newDaemon()
	tokA, err := d.Acquire("res", "alice", ttl)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Write("res", tokA, []byte("a")); err != nil {
		t.Fatal(err)
	}
	c.Advance(ttl)
	if _, err := d.Acquire("res", "bob", ttl); err != nil {
		t.Fatal(err)
	}
	// 水位已被抬高，等于旧水位的令牌也要拒绝。
	if err := d.Write("res", tokA, nil); !errors.Is(err, fence.ErrStale) {
		t.Fatalf("old watermark token: got %v, want ErrStale", err)
	}
}

func TestTokensIndependentAcrossResources(t *testing.T) {
	d, _ := newDaemon()
	tok1, err := d.Acquire("r1", "alice", ttl)
	if err != nil {
		t.Fatal(err)
	}
	tok2, err := d.Acquire("r2", "alice", ttl)
	if err != nil {
		t.Fatal(err)
	}
	// 跨资源各自独立，都从 1 开始。
	if tok1 != 1 || tok2 != 1 {
		t.Fatalf("tokens = %d, %d, want both 1", tok1, tok2)
	}
}

func TestStatusZeroValuesWhenNotHeld(t *testing.T) {
	d, c := newDaemon()
	st := d.Status("res")
	if st.Held || st.Holder != "" || st.Remaining != 0 || st.Token != 0 {
		t.Fatalf("fresh status = %+v, want zero values", st)
	}
	if _, err := d.Acquire("res", "alice", ttl); err != nil {
		t.Fatal(err)
	}
	if err := d.Release("res", "alice"); err != nil {
		t.Fatal(err)
	}
	st = d.Status("res")
	if st.Held || st.Holder != "" || st.Remaining != 0 {
		t.Fatalf("post-release status = %+v, must not leak previous holder", st)
	}
	c.Advance(time.Hour)
	st = d.Status("unknown")
	if st.Held || st.Holder != "" || st.Remaining != 0 {
		t.Fatalf("unknown resource status = %+v, want zero values", st)
	}
}

func TestGatewayRenewAndReleaseErrors(t *testing.T) {
	d, _ := newDaemon()
	if _, err := d.Acquire("res", "alice", ttl); err != nil {
		t.Fatal(err)
	}
	if err := d.Renew("res", "bob", ttl); !errors.Is(err, lease.ErrNotHolder) {
		t.Fatalf("renew by non-holder: got %v", err)
	}
	if err := d.Release("res", "bob"); !errors.Is(err, lease.ErrNotHolder) {
		t.Fatalf("release by non-holder: got %v", err)
	}
	if err := d.Release("res", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := d.Release("res", "alice"); !errors.Is(err, lease.ErrNotHeld) {
		t.Fatalf("second release: got %v, want ErrNotHeld", err)
	}
}
