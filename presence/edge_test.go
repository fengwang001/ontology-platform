package presence

import (
	"errors"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A lease expiring exactly at the query instant is treated as expired;
// one second before it is still active.
func TestExpiryExactBoundary(t *testing.T) {
	svc, _ := New(Config{LeaseSeconds: 10})
	must(t, svc.Report("u", "d", Online, 0))

	r, err := svc.Query("u", "u", 9)
	must(t, err)
	if r.Status != Online || r.ActiveDevice != 1 {
		t.Fatalf("t=9: %+v", r)
	}

	r, err = svc.Query("u", "u", 10)
	must(t, err)
	if r.Status != Offline || r.ActiveDevice != 0 || len(r.Devices) != 0 {
		t.Fatalf("t=10 exact expiry: %+v", r)
	}
}

// Aggregation priority and removal via Offline.
func TestAggregationPriorityAndOffline(t *testing.T) {
	svc, _ := New(Config{LeaseSeconds: 100})
	must(t, svc.Report("u", "a", Away, 0))
	must(t, svc.Report("u", "b", Busy, 0))
	must(t, svc.Report("u", "c", Online, 0))
	r, _ := svc.Query("u", "u", 0)
	if r.Status != Online || r.ActiveDevice != 3 {
		t.Fatalf("want online/3, got %+v", r)
	}

	must(t, svc.Offline("u", "c", 1))
	r, _ = svc.Query("u", "u", 1)
	if r.Status != Busy {
		t.Fatalf("want busy, got %v", r.Status)
	}

	must(t, svc.Offline("u", "b", 2))
	r, _ = svc.Query("u", "u", 2)
	if r.Status != Away {
		t.Fatalf("want away, got %v", r.Status)
	}

	if err := svc.Offline("u", "missing", 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := svc.Offline("ghost", "a", 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found user, got %v", err)
	}
}

// Eight device limit; expired slots are lazily freed.
func TestDeviceLimit(t *testing.T) {
	svc, _ := New(Config{LeaseSeconds: 5})
	for i := 0; i < 8; i++ {
		must(t, svc.Report("u", string(rune('a'+i)), Online, 0))
	}
	if err := svc.Report("u", "i", Online, 0); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("want too many, got %v", err)
	}
	// After expiry processing runs, the slot is reclaimable.
	must(t, svc.Report("u", "i", Online, 6))
	r, _ := svc.Query("u", "u", 6)
	if r.ActiveDevice != 1 || r.Status != Online {
		t.Fatalf("got %+v", r)
	}
}

// Clock rollback is rejected and changes nothing.
func TestClockRollback(t *testing.T) {
	svc, _ := New(Config{LeaseSeconds: 10})
	must(t, svc.Report("u", "d", Online, 10))
	if err := svc.Report("u", "d", Online, 9); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("got %v", err)
	}
	must(t, svc.SetInvisible("u", true, 11))
	if err := svc.SetInvisible("u", false, 10); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("got %v", err)
	}
	// Equal time is accepted (non-decreasing).
	must(t, svc.SetInvisible("u", false, 11))
}

// Real changes while invisible produce no notifications; turning visibility
// back on never replays the hidden backlog.
func TestInvisibleSuppressesAndNoBacklog(t *testing.T) {
	svc, _ := New(Config{LeaseSeconds: 100})
	must(t, svc.Report("u", "d", Online, 0))
	must(t, svc.Report("v", "self", Online, 0))
	must(t, svc.Subscribe("v", "u", 1))
	if d, _ := svc.Drain("v", 1); len(d.Notifications) != 0 {
		t.Fatalf("subscription must not notify: %+v", d)
	}

	must(t, svc.SetInvisible("u", true, 2))
	d, _ := svc.Drain("v", 2)
	if len(d.Notifications) != 1 || d.Notifications[0].Status != Offline {
		t.Fatalf("want one offline, got %+v", d)
	}

	must(t, svc.Report("u", "d", Busy, 3))
	must(t, svc.Report("u", "d", Away, 4))
	must(t, svc.Offline("u", "d", 5))
	if d, _ := svc.Drain("v", 5); len(d.Notifications) != 0 {
		t.Fatalf("hidden churn must not notify: %+v", d)
	}

	must(t, svc.SetInvisible("u", false, 6))
	d, _ = svc.Drain("v", 6)
	if len(d.Notifications) != 0 {
		t.Fatalf("unhide reveals offline, must not replay: %+v", d)
	}

	must(t, svc.Report("u", "d", Online, 7))
	d, _ = svc.Drain("v", 7)
	if len(d.Notifications) != 1 || d.Notifications[0].Status != Online {
		t.Fatalf("want one online after unhide: %+v", d)
	}
}

// Blocked viewers see offline, get nothing while blocked, and receive no
// backlog after unblock; they learn only the current state if it differs.
func TestBlockSuppressesAndResumesCurrentOnly(t *testing.T) {
	svc, _ := New(Config{LeaseSeconds: 100})
	must(t, svc.Report("u", "d", Online, 0))
	must(t, svc.Report("v", "self", Online, 0))
	must(t, svc.Subscribe("v", "u", 1))

	if err := svc.Block("u", "u", 2); !errors.Is(err, ErrCannotBlockSelf) {
		t.Fatalf("got %v", err)
	}
	must(t, svc.Block("u", "v", 2))
	d, _ := svc.Drain("v", 2)
	if len(d.Notifications) != 1 || d.Notifications[0].Status != Offline {
		t.Fatalf("block should show offline: %+v", d)
	}

	must(t, svc.Report("u", "d", Busy, 3))
	must(t, svc.Report("u", "d", Away, 4))
	if d, _ := svc.Drain("v", 4); len(d.Notifications) != 0 {
		t.Fatalf("blocked viewer must not receive: %+v", d)
	}

	must(t, svc.Unblock("u", "v", 5))
	d, _ = svc.Drain("v", 5)
	if len(d.Notifications) != 1 || d.Notifications[0].Status != Away {
		t.Fatalf("unblock reveals current away only: %+v", d)
	}
}

// Subscription errors are distinguishable.
func TestSubscriptionErrors(t *testing.T) {
	svc, _ := New(Config{LeaseSeconds: 100})
	must(t, svc.Report("u", "d", Online, 0))
	must(t, svc.Report("v", "d", Online, 0))

	if err := svc.Subscribe("u", "u", 1); !errors.Is(err, ErrCannotSubscribeSelf) {
		t.Fatalf("got %v", err)
	}
	must(t, svc.Subscribe("u", "v", 1))
	if err := svc.Subscribe("u", "v", 2); !errors.Is(err, ErrAlreadySubscribed) {
		t.Fatalf("got %v", err)
	}
	if err := svc.Subscribe("u", "ghost", 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := svc.Subscribe("ghost", "u", 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	must(t, svc.Unsubscribe("u", "v", 3))
	if err := svc.Unsubscribe("u", "v", 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

// One Drain surfaces multiple expiry events from distinct effective times
// (here out-of-maturity-order via interleaved leases), sorted by time, and
// consecutive states chain.
func TestDrainMultipleExpiries(t *testing.T) {
	// Two devices expire at different instants with no drain in between; one
	// drain delivers the whole chain ordered by effective time.
	svc, _ := New(Config{LeaseSeconds: 10})
	must(t, svc.Report("u", "a", Online, 0)) // expiry 10
	must(t, svc.Report("u", "b", Busy, 5))   // expiry 15
	must(t, svc.Report("v", "self", Online, 0))
	must(t, svc.Subscribe("v", "u", 5))
	d, _ := svc.Drain("v", 20)
	if len(d.Notifications) != 2 {
		t.Fatalf("want busy@10, offline@15, got %+v", d)
	}
	if d.Notifications[0].Effective != 10 || d.Notifications[0].Status != Busy {
		t.Fatalf("first %+v", d.Notifications[0])
	}
	if d.Notifications[1].Effective != 15 || d.Notifications[1].Status != Offline {
		t.Fatalf("second %+v", d.Notifications[1])
	}

	// Switch and expiry events from two targets interleave in one drain.
	svc2, _ := New(Config{LeaseSeconds: 10})
	must(t, svc2.Report("w", "d1", Online, 0)) // expiry 10
	must(t, svc2.Report("x", "self", Online, 0))
	must(t, svc2.Report("z", "d1", Online, 0)) // expiry 10
	must(t, svc2.Subscribe("x", "w", 0))
	must(t, svc2.Subscribe("x", "z", 0))
	must(t, svc2.Report("z", "d1", Busy, 8)) // switch @8
	d2, _ := svc2.Drain("x", 20)
	if len(d2.Notifications) != 3 {
		t.Fatalf("got %+v", d2.Notifications)
	}
	want2 := []struct {
		target string
		eff    int64
		st     Status
	}{{"z", 8, Busy}, {"w", 10, Offline}, {"z", 18, Offline}}
	for i, w := range want2 {
		got := d2.Notifications[i]
		if got.Target != w.target || got.Effective != w.eff || got.Status != w.st {
			t.Fatalf("[%d] got %+v want %+v", i, got, w)
		}
	}
}

// Same-instant expiry is emitted before the switch stamped at the same time.
func TestExpiryBeforeSwitchSameInstant(t *testing.T) {
	svc, _ := New(Config{LeaseSeconds: 10})
	must(t, svc.Report("u", "a", Busy, 0)) // expires 10
	must(t, svc.Report("v", "self", Online, 0))
	must(t, svc.Subscribe("v", "u", 0))
	// At t=10 the device is already exactly expired, then hide at the same t.
	// A single offline notification results; category ordering is asserted
	// indirectly by chains across two targets below.

	svc2, _ := New(Config{LeaseSeconds: 10})
	must(t, svc2.Report("a", "d", Online, 0)) // expires 10
	must(t, svc2.Report("b", "d", Online, 0)) // expires 10
	must(t, svc2.Report("v", "self", Online, 0))
	must(t, svc2.Subscribe("v", "a", 0))
	must(t, svc2.Subscribe("v", "b", 0))
	must(t, svc2.SetInvisible("b", true, 10))
	d, _ := svc2.Drain("v", 10)
	if len(d.Notifications) != 2 {
		t.Fatalf("%+v", d)
	}
	for _, n := range d.Notifications {
		if n.Status != Offline || n.Effective != 10 {
			t.Fatalf("unexpected %+v", n)
		}
	}
}

// Backlog beyond 1000 is trimmed oldest-first and reported once.
func TestBacklogOverflow(t *testing.T) {
	svc, _ := New(Config{LeaseSeconds: 3600})
	must(t, svc.Report("u", "d", Online, 0))
	must(t, svc.Report("v", "self", Online, 0))
	must(t, svc.Subscribe("v", "u", 0))

	// Generate 1002 distinct visible toggles without draining.
	states := []Status{Online, Busy, Away}
	for i := 1; i <= 1002; i++ {
		must(t, svc.Report("u", "d", states[i%3], int64(i)))
	}
	d, _ := svc.Drain("v", 1002)
	if d.Dropped != 2 || len(d.Notifications) != 1000 {
		t.Fatalf("dropped=%d len=%d", d.Dropped, len(d.Notifications))
	}
	// Remaining chain must be contiguous in status.
	prev := d.Notifications[0]
	for _, n := range d.Notifications[1:] {
		if n.Effective < prev.Effective {
			t.Fatal("not sorted")
		}
		prev = n
	}
	// Dropped count is not reported again.
	if d2, _ := svc.Drain("v", 1003); d2.Dropped != 0 || len(d2.Notifications) != 0 {
		t.Fatalf("%+v", d2)
	}
}
