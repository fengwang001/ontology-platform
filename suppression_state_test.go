package ontology

import (
	"errors"
	"testing"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := NewManager(2, 10, 100, 50, 2, 30, 200)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return manager
}

func assertSuppressed(t *testing.T, manager *Manager, address string, now int64, want bool, wantReason Reason) {
	t.Helper()
	got, reason, err := manager.IsSuppressed(address, now)
	t.Logf("input=IsSuppressed address=%q now=%d output=(%v,%s,%v) basis=address reason then active domain", address, now, got, reason, err)
	if err != nil || got != want || reason != wantReason {
		t.Fatalf("IsSuppressed(%q, %d) = (%v, %s, %v), want (%v, %s, nil)", address, now, got, reason, err, want, wantReason)
	}
}

func TestSoftWindowAndTTLBoundaries(t *testing.T) {
	manager := newTestManager(t)
	address := "a.b+x@Gmail.com"
	alias := "AB@googlemail.com"

	if err := manager.Soft(address, 1); err != nil {
		t.Fatalf("Soft at 1: %v", err)
	}
	if err := manager.Soft(alias, 8); err != nil {
		t.Fatalf("Soft at 8: %v", err)
	}

	state := manager.addresses["ab@gmail.com"]
	t.Logf("input=Soft normalized=ab@gmail.com times=[1 8] output=reason=%s since=%d softUntil=%d basis=both timestamps are > now-W", state.reason, state.since, state.softUntil)
	if state.reason != Soft || state.since != 8 || state.softUntil != 108 || len(state.log) != 0 {
		t.Fatalf("state = %+v, want active soft until 108 with cleared log", state)
	}
	assertSuppressed(t, manager, alias, 107, true, Soft)
	assertSuppressed(t, manager, alias, 108, false, None)
}

func TestSoftWindowUsesStrictlyGreaterThan(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.Soft("a@example.com", 1); err != nil {
		t.Fatal(err)
	}
	if err := manager.Soft("a@example.com", 11); err != nil {
		t.Fatal(err)
	}
	state := manager.addresses["a@example.com"]
	t.Logf("input=Soft times=[1,11] W=10 output=reason=%s basis=1 is not strictly greater than 11-10", state.reason)
	if state.reason != None || len(state.log) != 1 || state.log[0] != 11 {
		t.Fatalf("state = %+v, want one retained timestamp at 11", state)
	}
	assertSuppressed(t, manager, "a@example.com", 11, false, None)
}

func TestHardUpgradesSoftAndRepeatedHardKeepsSince(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.Soft("a@example.com", 1); err != nil {
		t.Fatal(err)
	}
	if err := manager.Soft("a@example.com", 2); err != nil {
		t.Fatal(err)
	}
	if err := manager.Hard("a@example.com", 20); err != nil {
		t.Fatal(err)
	}
	state := manager.addresses["a@example.com"]
	if state.reason != Hard || state.since != 20 || state.lastHard != 20 || len(state.log) != 0 {
		t.Fatalf("after first hard state = %+v", state)
	}

	if err := manager.Hard("a@example.com", 30); err != nil {
		t.Fatal(err)
	}
	t.Logf("input=Hard repeated at=30 output=since=%d lastHard=%d basis=hard is not lower than hard", state.since, state.lastHard)
	if state.since != 20 || state.lastHard != 30 {
		t.Fatalf("state = %+v, want unchanged since 20 and refreshed lastHard 30", state)
	}
}

func TestUnsubIgnoredForHard(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.Hard("a@example.com", 5); err != nil {
		t.Fatal(err)
	}
	if err := manager.Unsub("a@example.com", 6); err != nil {
		t.Fatal(err)
	}
	state := manager.addresses["a@example.com"]
	t.Logf("input=Unsub at=6 while=hard since=5 output=reason=%s since=%d basis=only none or soft can become unsub", state.reason, state.since)
	if state.reason != Hard || state.since != 5 {
		t.Fatalf("state = %+v, want hard unchanged", state)
	}
}

func TestRejectionsDoNotMutateStateOrClock(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.Hard("a@example.com", 5); err != nil {
		t.Fatal(err)
	}

	err := manager.Hard("bad-address", 6)
	if !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("invalid address error = %v", err)
	}
	err = manager.Hard("a@example.com", 4)
	if !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback error = %v", err)
	}

	if manager.maxNow != 5 {
		t.Fatalf("maxNow = %d, want 5", manager.maxNow)
	}
	state := manager.addresses["a@example.com"]
	if state.reason != Hard || state.since != 5 || state.lastHard != 5 {
		t.Fatalf("state = %+v, want rejected operations to leave it unchanged", state)
	}
}
