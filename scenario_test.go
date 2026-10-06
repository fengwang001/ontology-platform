package waitlist

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func testSystem(t *testing.T) *System {
	t.Helper()
	system, err := NewSystem(Config{MaxQueueEntries: 100, ConfirmationWindow: 10})
	if err != nil {
		t.Fatal(err)
	}
	return system
}

func keyOf(id string) FlightKey { return FlightKey{FlightID: id, Cabin: "Y"} }

func requireCode(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var callErr *CallError
	if !errors.As(err, &callErr) || callErr.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func statusOf(t *testing.T, state *FlightState, id int64) Status {
	t.Helper()
	return state.Entries[id].Status
}

func TestExactFitSkippingAndPriorityOrder(t *testing.T) {
	system := testSystem(t)
	key := keyOf("CA1")
	if err := system.AddFlight(0, key, 2, 2); err != nil {
		t.Fatal(err)
	}
	big, _ := system.Register(1, key, "big", 2, PriorityLow)
	small, _ := system.Register(2, key, "small", 1, PriorityLow)
	high, _ := system.Register(3, key, "high", 1, PriorityHigh)

	if err := system.AdjustCapacity(4, key, 4); err != nil {
		t.Fatal(err)
	}
	state, err := system.Snapshot(4, key)
	if err != nil {
		t.Fatal(err)
	}
	if statusOf(t, state, big) != StatusWaiting || len(state.WaitingOrder) != 1 || state.WaitingOrder[0] != big {
		t.Fatalf("skipped big entry must retain position: %#v", state)
	}
	if statusOf(t, state, high) != StatusPending || statusOf(t, state, small) != StatusPending {
		t.Fatalf("high priority precedes later low: %#v", state.Entries)
	}
	state, _ = system.Snapshot(14, key)
	if statusOf(t, state, big) != StatusPending || state.Available() != 0 {
		t.Fatalf("exact fit must fulfill big: %#v", state)
	}
}

func TestSameDeadlineExpiriesMergedAndLaterRegistrationExcluded(t *testing.T) {
	system := testSystem(t)
	key := keyOf("CA2")
	if err := system.AddFlight(0, key, 3, 3); err != nil {
		t.Fatal(err)
	}
	first, _ := system.Register(1, key, "first", 1, PriorityHigh)
	second, _ := system.Register(2, key, "second", 1, PriorityNormal)
	third, _ := system.Register(3, key, "third", 1, PriorityNormal)
	if err := system.AdjustCapacity(10, key, 5); err != nil {
		t.Fatal(err)
	}
	state0, _ := system.Snapshot(10, key)
	if statusOf(t, state0, first) != StatusPending || statusOf(t, state0, second) != StatusPending {
		t.Fatalf("setup expected first and second pending: %#v", state0.Entries)
	}
	if state0.Entries[first].Deadline != 20 || state0.Entries[second].Deadline != 20 {
		t.Fatalf("one release must assign equal deadlines: %#v", state0.Entries)
	}
	state, _ := system.Snapshot(19, key)
	if statusOf(t, state, first) != StatusPending || statusOf(t, state, second) != StatusPending {
		t.Fatalf("deadline equality is not yet expired: %#v", state.Entries)
	}
	state, _ = system.Snapshot(20, key)
	if statusOf(t, state, first) != StatusExpired || statusOf(t, state, second) != StatusExpired {
		t.Fatalf("same deadline must expire as one release: %#v", state.Entries)
	}
	if statusOf(t, state, third) != StatusPending || state.PendingCount != 1 {
		t.Fatalf("merged release should fulfill only third: %#v", state.Entries)
	}
	if state.Entries[third].Deadline != 30 {
		t.Fatalf("merged release time defines new deadline: %#v", state.Entries[third])
	}
	late, _ := system.Register(20, key, "late", 1, PriorityHigh)
	state, _ = system.Snapshot(30, key)
	if statusOf(t, state, third) != StatusExpired || statusOf(t, state, late) != StatusPending || state.PendingCount != 1 {
		t.Fatalf("late entry only joins the later release: %#v", state.Entries)
	}
	state, _ = system.Snapshot(40, key)
	if statusOf(t, state, late) != StatusExpired || state.PendingCount != 0 {
		t.Fatalf("late entry expires in the next batch: %#v", state)
	}
}

func TestNegativeAvailabilityThenIncreaseAcrossZero(t *testing.T) {
	system := testSystem(t)
	key := keyOf("CA3")
	if err := system.AddFlight(0, key, 2, 2); err != nil {
		t.Fatal(err)
	}
	entry, _ := system.Register(0, key, "p", 1, PriorityLow)
	if err := system.AdjustCapacity(1, key, 1); err != nil {
		t.Fatal(err)
	}
	state, _ := system.Snapshot(1, key)
	if state.Available() >= 0 || statusOf(t, state, entry) != StatusWaiting {
		t.Fatalf("negative availability must not fulfill: %#v", state)
	}
	if err := system.AdjustCapacity(2, key, 3); err != nil {
		t.Fatal(err)
	}
	state, _ = system.Snapshot(2, key)
	if statusOf(t, state, entry) != StatusPending {
		t.Fatalf("crossing zero fulfills: %#v", state.Entries)
	}
}

func TestPriorityChangeKeepsRegistrationTime(t *testing.T) {
	system := testSystem(t)
	key := keyOf("CA4")
	if err := system.AddFlight(0, key, 1, 1); err != nil {
		t.Fatal(err)
	}
	old, _ := system.Register(1, key, "old", 1, PriorityLow)
	newer, _ := system.Register(2, key, "new", 1, PriorityHigh)
	if err := system.ChangePriority(3, old, PriorityHigh); err != nil {
		t.Fatal(err)
	}
	if err := system.AdjustCapacity(4, key, 2); err != nil {
		t.Fatal(err)
	}
	state, _ := system.Snapshot(4, key)
	if statusOf(t, state, old) != StatusPending || statusOf(t, state, newer) != StatusWaiting {
		t.Fatalf("original registration time breaks priority tie: %#v", state.Entries)
	}
	if state.Entries[old].RegisteredAt != 1 {
		t.Fatal("priority change must preserve registration time")
	}
}

func TestCancelFlightKeepsAllSixStatusesDistinguishable(t *testing.T) {
	system := testSystem(t)
	key := keyOf("CA5")
	if err := system.AddFlight(0, key, 3, 2); err != nil {
		t.Fatal(err)
	}
	canceller, _ := system.Register(0, key, "canceller", 1, PriorityLow)
	confirmed, _ := system.Register(0, key, "confirmed", 1, PriorityLow)
	withdrawn, _ := system.Register(0, key, "withdrawn", 1, PriorityLow)
	if err := system.Withdraw(0, withdrawn); err != nil {
		t.Fatal(err)
	}
	if err := system.AdjustCapacity(0, key, 4); err != nil {
		t.Fatal(err)
	}
	if err := system.Confirm(1, confirmed); err != nil {
		t.Fatal(err)
	}
	if err := system.Confirm(1, canceller); err != nil {
		t.Fatal(err)
	}
	if err := system.CancelConfirmed(2, canceller); err != nil {
		t.Fatal(err)
	}
	expired, _ := system.Register(3, key, "expired", 1, PriorityLow)
	if err := system.AdjustCapacity(3, key, 5); err != nil {
		t.Fatal(err)
	}
	state, _ := system.Snapshot(13, key)
	if statusOf(t, state, expired) != StatusExpired || statusOf(t, state, confirmed) != StatusConfirmed || statusOf(t, state, canceller) != StatusWithdrawn {
		t.Fatalf("confirmed and expired setup failed: %#v", state.Entries)
	}
	pending, _ := system.Register(14, key, "pending", 1, PriorityLow)
	waiting, _ := system.Register(14, key, "waiting", 1, PriorityLow)
	if err := system.AdjustCapacity(14, key, 6); err != nil {
		t.Fatal(err)
	}
	if err := system.CancelFlight(15, key); err != nil {
		t.Fatal(err)
	}
	state, _ = system.Snapshot(15, key)
	for id, expected := range map[int64]Status{confirmed: StatusConfirmed, withdrawn: StatusWithdrawn, pending: StatusVoid, expired: StatusExpired, waiting: StatusVoid} {
		if statusOf(t, state, id) != expected {
			t.Fatalf("entry %d = %s, want %s", id, state.Entries[id].Status, expected)
		}
	}
	_, err := system.Register(16, key, "rejected", 1, PriorityLow)
	requireCode(t, err, ErrFlightCanceled)
}

func TestAdjacentRejectionPrecedence(t *testing.T) {
	system := testSystem(t)
	key := keyOf("ORDER")
	missing := keyOf("MISSING")
	if err := system.AddFlight(0, key, 1, 1); err != nil {
		t.Fatal(err)
	}
	entry, _ := system.Register(0, key, "p", 1, PriorityLow)

	_, err := system.Register(-1, key, "bad", 1, Priority(99))
	requireCode(t, err, ErrInvalidArgument)
	_, err = system.Register(-1, missing, "bad", 1, PriorityLow)
	requireCode(t, err, ErrInvalidArgument)
	_, err = system.Register(-1, key, "", 1, PriorityLow)
	requireCode(t, err, ErrInvalidArgument)

	system.mu.Lock()
	system.lastTime = 1
	system.mu.Unlock()
	_, err = system.Register(0, missing, "p", 1, PriorityLow)
	requireCode(t, err, ErrClockRollback)
	requireCode(t, system.Withdraw(0, 999), ErrClockRollback)

	_, err = system.Register(1, missing, "p", 1, PriorityLow)
	requireCode(t, err, ErrFlightNotFound)

	if err := system.AdjustCapacity(1, key, 3); err != nil {
		t.Fatal(err)
	}
	if err := system.Confirm(2, entry); err != nil {
		t.Fatal(err)
	}
	requireCode(t, system.Withdraw(3, entry), ErrInvalidStatus)
	requireCode(t, system.Withdraw(1, 999), ErrClockRollback)
	requireCode(t, system.Withdraw(3, 999), ErrEntryNotFound)

	if err := system.AdjustCapacity(3, key, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := system.Register(3, key, "again", 1, PriorityLow); err != nil {
		t.Fatal(err)
	}
	_, err = system.Register(3, key, "again", 1, PriorityLow)
	requireCode(t, err, ErrDuplicateEntry)

	fullSystem, err := NewSystem(Config{MaxQueueEntries: 1, ConfirmationWindow: 10})
	if err != nil {
		t.Fatal(err)
	}
	fullKey := keyOf("FULL")
	if err := fullSystem.AddFlight(0, fullKey, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := fullSystem.Register(0, fullKey, "first", 1, PriorityLow); err != nil {
		t.Fatal(err)
	}
	_, err = fullSystem.Register(0, fullKey, "first", 1, PriorityLow)
	requireCode(t, err, ErrDuplicateEntry)
	_, err = fullSystem.Register(0, fullKey, "second", 1, PriorityLow)
	requireCode(t, err, ErrQueueFull)

	if err := system.CancelFlight(4, key); err != nil {
		t.Fatal(err)
	}
	_, err = system.Register(4, key, "late", 1, PriorityLow)
	requireCode(t, err, ErrFlightCanceled)
	requireCode(t, system.Withdraw(4, entry), ErrFlightCanceled)
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	system, err := NewSystem(Config{MaxQueueEntries: 100, ConfirmationWindow: 100})
	if err != nil {
		t.Fatal(err)
	}
	key := keyOf("CONCURRENT")
	if err := system.AddFlight(0, key, 50, 50); err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 8)
	register := make(chan struct{})
	adjust := make(chan struct{})
	confirm := make(chan struct{})
	var registerWG, adjustWG, confirmWG sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		registerWG.Add(1)
		go func(worker int) {
			defer registerWG.Done()
			passenger := "c" + string(rune('a'+worker))
			<-register
			id, err := system.Register(1, key, passenger, 1, Priority(worker%3))
			if err != nil {
				t.Error(err)
				return
			}
			ids[worker] = id
		}(worker)
	}
	for worker := 0; worker < 8; worker++ {
		adjustWG.Add(1)
		go func(worker int) {
			defer adjustWG.Done()
			<-adjust
			if err := system.AdjustCapacity(2, key, 58); err != nil {
				t.Error(err)
				return
			}
		}(worker)
	}
	for worker := 0; worker < 8; worker++ {
		confirmWG.Add(1)
		go func(worker int) {
			defer confirmWG.Done()
			<-confirm
			if err := system.Confirm(3, ids[worker]); err != nil {
				t.Error(err)
			}
		}(worker)
	}
	close(register)
	registerWG.Wait()
	time.Sleep(time.Millisecond)
	close(adjust)
	adjustWG.Wait()
	time.Sleep(time.Millisecond)
	close(confirm)
	registerWG.Wait()
	adjustWG.Wait()
	confirmWG.Wait()
	state, err := system.Snapshot(4, key)
	if err != nil {
		t.Fatal(err)
	}
	if state.Confirmed != 58 || state.PendingCount != 0 || len(state.WaitingOrder) != 0 || state.Confirmed+state.PendingCount > state.Capacity {
		t.Fatalf("unexpected concurrent result: %#v", state)
	}
	if state.Confirmed < 50 {
		t.Fatalf("serializable execution cannot lose original confirmed count: %#v", state)
	}
}
