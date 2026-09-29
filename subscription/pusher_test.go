package subscription

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"
)

func testLogger() (*bytes.Buffer, *slog.Logger) {
	var logs bytes.Buffer
	return &logs, slog.New(slog.NewTextHandler(&logs, nil))
}

func findSubscription[K OrderedKey](t *testing.T, snapshot Snapshot[K], id string) SubscriptionInfo[K] {
	t.Helper()
	for _, sub := range snapshot.Subscriptions {
		if sub.ID == id {
			return sub
		}
	}
	t.Fatalf("subscription %q not found in snapshot: %+v", id, snapshot)
	return SubscriptionInfo[K]{}
}

func assertNoChange[K OrderedKey](t *testing.T, updates <-chan Change[K]) {
	t.Helper()
	select {
	case change := <-updates:
		t.Fatalf("received unexpected change: %+v", change)
	default:
	}
}

func TestBoundaryEqualityMatches(t *testing.T) {
	t.Parallel()

	logs, logger := testLogger()
	pusher := NewPusherWithLogger[int](logger)
	updates := make(chan Change[int], 2)

	if err := pusher.Register("alpha", 10, updates); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	below := pusher.Push(9)
	if below.Sequence != 1 {
		t.Fatalf("below-bound sequence = %d, want 1", below.Sequence)
	}
	assertNoChange(t, updates)

	equal := pusher.Push(10)
	if equal.Sequence != 2 {
		t.Fatalf("equal-bound sequence = %d, want 2", equal.Sequence)
	}
	received := <-updates
	if received != equal {
		t.Fatalf("received change = %+v, want %+v", received, equal)
	}

	t.Logf("input: keys 9 and 10 with lower bound 10; result: only sequence 2 delivered; decision: key >= lower_bound\n%s", logs.String())
}

func TestRegistrationStartsAtCurrentPosition(t *testing.T) {
	t.Parallel()

	logs, logger := testLogger()
	pusher := NewPusherWithLogger[int](logger)

	historical := pusher.Push(100)
	if historical.Sequence != 1 {
		t.Fatalf("historical sequence = %d, want 1", historical.Sequence)
	}

	updates := make(chan Change[int], 1)
	if err := pusher.Register("alpha", 0, updates); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	snapshotAfterRegister := pusher.Snapshot()
	alpha := findSubscription(t, snapshotAfterRegister, "alpha")
	if alpha.StartAfter != 1 {
		t.Fatalf("StartAfter = %d, want 1", alpha.StartAfter)
	}
	assertNoChange(t, updates)

	current := pusher.Push(1)
	received := <-updates
	if received != current {
		t.Fatalf("received change = %+v, want %+v", received, current)
	}

	t.Logf("input: historical sequence 1 then registration; result: only sequence 2 delivered; decision: start_after=%d\n%s", alpha.StartAfter, logs.String())
}

func TestUnsubscribeDeliversNothing(t *testing.T) {
	t.Parallel()

	logs, logger := testLogger()
	pusher := NewPusherWithLogger[int](logger)
	updates := make(chan Change[int], 1)

	if err := pusher.Register("alpha", 0, updates); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	beforeUnsubscribe := pusher.Push(1)
	if received := <-updates; received != beforeUnsubscribe {
		t.Fatalf("received before unsubscribe = %+v, want %+v", received, beforeUnsubscribe)
	}

	if err := pusher.Unsubscribe("alpha"); err != nil {
		t.Fatalf("Unsubscribe() error = %v", err)
	}
	pusher.Push(2)
	assertNoChange(t, updates)

	if delivered, exists := pusher.DeliveredThrough("alpha"); exists {
		t.Fatalf("DeliveredThrough after unsubscribe = (%d, true), want (0, false)", delivered)
	}

	t.Logf("input: one active change followed by unsubscribe and another change; result: channel has one delivery only; decision: inactive subscriptions are skipped\n%s", logs.String())
}

func TestRejectedRequestsLeaveStateUnchanged(t *testing.T) {
	t.Parallel()

	logs, logger := testLogger()
	pusher := NewPusherWithLogger[int](logger)
	alphaUpdates := make(chan Change[int], 2)

	if err := pusher.Register("alpha", 5, alphaUpdates); err != nil {
		t.Fatalf("Register(alpha) error = %v", err)
	}
	pusher.Push(5)
	initialAlpha := <-alphaUpdates

	duplicateUpdates := make(chan Change[int], 1)
	rejections := []struct {
		name string
		run  func() error
		want error
	}{
		{"invalid subscription id", func() error { return pusher.Register("   ", 0, duplicateUpdates) }, ErrInvalidSubscriptionID},
		{"duplicate subscription id", func() error { return pusher.Register("alpha", 0, duplicateUpdates) }, ErrDuplicateSubscription},
		{"unsubscribe unknown id", func() error { return pusher.Unsubscribe("missing") }, ErrSubscriptionNotFound},
	}
	for _, rejection := range rejections {
		if err := rejection.run(); !errors.Is(err, rejection.want) {
			t.Fatalf("%s error = %v, want %v", rejection.name, err, rejection.want)
		}
	}

	if ErrInvalidSubscriptionID == ErrDuplicateSubscription || ErrInvalidSubscriptionID == ErrSubscriptionNotFound || ErrDuplicateSubscription == ErrSubscriptionNotFound {
		t.Fatal("rejection errors must remain distinct")
	}

	snapshot := pusher.Snapshot()
	if snapshot.CurrentSequence != 1 {
		t.Fatalf("CurrentSequence after rejections = %d, want 1", snapshot.CurrentSequence)
	}
	if len(snapshot.Subscriptions) != 1 {
		t.Fatalf("subscription count = %d, want 1", len(snapshot.Subscriptions))
	}
	alpha := findSubscription(t, snapshot, "alpha")
	if alpha.DeliveredThrough != initialAlpha.Sequence {
		t.Fatalf("DeliveredThrough = %d, want %d", alpha.DeliveredThrough, initialAlpha.Sequence)
	}
	assertNoChange(t, duplicateUpdates)

	betaUpdates := make(chan Change[int], 1)
	if err := pusher.Register("beta", 0, betaUpdates); err != nil {
		t.Fatalf("Register(beta) after rejections error = %v", err)
	}
	afterRejection := pusher.Push(6)
	if received := <-alphaUpdates; received != afterRejection {
		t.Fatalf("alpha received = %+v, want %+v", received, afterRejection)
	}
	if received := <-betaUpdates; received != afterRejection {
		t.Fatalf("beta received = %+v, want %+v", received, afterRejection)
	}

	t.Logf("input: invalid id, duplicate alpha, missing unsubscribe; result: all rejected and state stayed usable; decisions: invalid-id, duplicate-id, not-found\n%s", logs.String())
}
