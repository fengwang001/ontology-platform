package ontology

import (
	"errors"
	"slices"
	"testing"
)

func TestSoftBounceAttemptInvalidation(t *testing.T) {
	tracker := mustNewTracker(t, 2)
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("A")}, 100); err != nil {
		t.Fatal(err)
	}

	receive(t, tracker, "m", "A", Soft, 1, 1, Applied)
	receive(t, tracker, "m", "A", Sent, 2, 2, Applied)
	receive(t, tracker, "m", "A", Soft, 2, 3, Applied)
	assertRcpt(t, tracker, "m", "A", 1, NoFailure, false)
	receive(t, tracker, "m", "A", Soft, 3, 4, Applied)
	assertRcpt(t, tracker, "m", "A", 1, SoftFail, false)

	tracker = mustNewTracker(t, 2)
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("A")}, 100); err != nil {
		t.Fatal(err)
	}
	receive(t, tracker, "m", "A", Sent, 1, 1, Applied)
	receive(t, tracker, "m", "A", Soft, 1, 2, Applied)
	receive(t, tracker, "m", "A", Soft, 2, 3, Applied)
	assertRcpt(t, tracker, "m", "A", 1, SoftFail, false)
}

func TestSoftThresholdExactAndOneBelow(t *testing.T) {
	tracker := mustNewTracker(t, 3)
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("A"), []byte("B")}, 100); err != nil {
		t.Fatal(err)
	}

	receive(t, tracker, "m", "A", Soft, 1, 1, Applied)
	receive(t, tracker, "m", "A", Soft, 2, 2, Applied)
	assertRcpt(t, tracker, "m", "A", 0, NoFailure, false)
	receive(t, tracker, "m", "A", Soft, 3, 3, Applied)
	assertRcpt(t, tracker, "m", "A", 0, SoftFail, false)

	receive(t, tracker, "m", "B", Soft, 1, 1, Applied)
	receive(t, tracker, "m", "B", Soft, 2, 2, Applied)
	assertRcpt(t, tracker, "m", "B", 0, NoFailure, false)
}

func TestOutOfOrderSentAfterDelivered(t *testing.T) {
	tracker := mustNewTracker(t, 2)
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("A")}, 100); err != nil {
		t.Fatal(err)
	}

	receive(t, tracker, "m", "A", Delivered, 1, 2, Applied)
	receive(t, tracker, "m", "A", Sent, 2, 1, Stale)
	got := storedRecipient(t, tracker, "m", "A")
	if got.r != 2 || got.sentA != 2 {
		t.Fatalf("r=%d sentA=%d, want r=2 sentA=2", got.r, got.sentA)
	}
}

func TestHardAndSoftAfterDelivery(t *testing.T) {
	tracker := mustNewTracker(t, 2)
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("sent"), []byte("delivered")}, 100); err != nil {
		t.Fatal(err)
	}

	receive(t, tracker, "m", "sent", Sent, 1, 1, Applied)
	receive(t, tracker, "m", "sent", Hard, 1, 2, Applied)
	assertRcpt(t, tracker, "m", "sent", 1, HardFail, false)

	receive(t, tracker, "m", "delivered", Delivered, 1, 1, Applied)
	receive(t, tracker, "m", "delivered", Soft, 1, 2, Stale)
	receive(t, tracker, "m", "delivered", Hard, 1, 3, Stale)
	assertRcpt(t, tracker, "m", "delivered", 2, NoFailure, true)
}

func TestExpirationRevocationBoundary(t *testing.T) {
	cases := []struct {
		name   string
		kind   ReceiptKind
		ts     int64
		result ReceiptResult
		r      int
		fail   Failure
	}{
		{"delivered one before deadline", Delivered, 99, Applied, 2, NoFailure},
		{"delivered equal deadline", Delivered, 100, Ignored, 0, Expired},
		{"read one before deadline", Read, 99, Applied, 3, NoFailure},
		{"sent cannot revoke", Sent, 99, Ignored, 0, Expired},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tracker := mustNewTracker(t, 2)
			if err := tracker.Send([]byte("m"), [][]byte{[]byte("A")}, 100); err != nil {
				t.Fatal(err)
			}
			if got := mustTick(t, tracker, 100); len(got) != 1 {
				t.Fatalf("Tick = %v, want one expiration", got)
			}
			receive(t, tracker, "m", "A", tc.kind, 1, tc.ts, tc.result)
			assertRcpt(t, tracker, "m", "A", tc.r, tc.fail, false)
		})
	}
}

func TestExpiredHardUpgradesSoftIgnored(t *testing.T) {
	tracker := mustNewTracker(t, 2)
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("soft"), []byte("hard")}, 100); err != nil {
		t.Fatal(err)
	}
	mustTick(t, tracker, 100)

	receive(t, tracker, "m", "soft", Soft, 1, 99, Ignored)
	assertRcpt(t, tracker, "m", "soft", 0, Expired, false)
	receive(t, tracker, "m", "hard", Hard, 1, 99, Applied)
	assertRcpt(t, tracker, "m", "hard", 0, HardFail, false)
}

func TestDeadlineEqualNowAndRepeatedTick(t *testing.T) {
	tracker := mustNewTracker(t, 2)
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("B"), []byte("A")}, 100); err != nil {
		t.Fatal(err)
	}

	first := mustTick(t, tracker, 100)
	want := []Expiration{{Message: []byte("m"), Recipient: []byte("A")}, {Message: []byte("m"), Recipient: []byte("B")}}
	if !slices.EqualFunc(first, want, expirationEqual) {
		t.Fatalf("first tick = %#v, want %#v", first, want)
	}
	if second := mustTick(t, tracker, 100); len(second) != 0 {
		t.Fatalf("second tick = %#v, want empty", second)
	}
}

func TestDuplicatesDoNotChangeState(t *testing.T) {
	tracker := mustNewTracker(t, 2)
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("A")}, 100); err != nil {
		t.Fatal(err)
	}

	receive(t, tracker, "m", "A", Sent, 1, 1, Applied)
	receive(t, tracker, "m", "A", Sent, 1, 2, Duplicate)
	receive(t, tracker, "m", "A", Soft, 1, 1, Applied)
	receive(t, tracker, "m", "A", Soft, 1, 2, Duplicate)
	assertRcpt(t, tracker, "m", "A", 1, NoFailure, false)
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	tracker := mustNewTracker(t, 2)
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("A")}, 100); err != nil {
		t.Fatal(err)
	}

	if err := tracker.Send(nil, [][]byte{[]byte("A")}, 100); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid Send error = %v", err)
	}
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("A")}, 100); !errors.Is(err, ErrMessageExists) {
		t.Fatalf("duplicate Send error = %v", err)
	}
	if _, err := tracker.Receipt([]byte("m"), []byte("A"), "bad", 1, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid Receipt error = %v", err)
	}
	if _, err := tracker.Receipt([]byte("x"), []byte("A"), Sent, 1, 1); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("unknown message error = %v", err)
	}
	if _, err := tracker.Receipt([]byte("m"), []byte("X"), Sent, 1, 1); !errors.Is(err, ErrUnknownRecipient) {
		t.Fatalf("unknown recipient error = %v", err)
	}
	if _, err := tracker.Tick(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid Tick error = %v", err)
	}
	if _, err := tracker.Tick(1); err != nil {
		t.Fatalf("valid Tick: %v", err)
	}
	if _, err := tracker.Tick(0); !errors.Is(err, ErrClockMovedBack) {
		t.Fatalf("backward Tick error = %v", err)
	}
	if _, err := tracker.Status(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid Status error = %v", err)
	}
	if _, err := tracker.Status([]byte("x")); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("unknown Status message error = %v", err)
	}

	assertRcpt(t, tracker, "m", "A", 0, NoFailure, false)
	if got := storedRecipient(t, tracker, "m", "A"); len(got.seen) != 0 || tracker.lastTick != 1 {
		t.Fatalf("rejection changed state: seen=%v lastTick=%d", got.seen, tracker.lastTick)
	}
}
