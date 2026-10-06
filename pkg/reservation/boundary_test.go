package reservation

import (
	"errors"
	"testing"
)

func asOpErr(t *testing.T, err error) *OpError {
	t.Helper()
	var oe *OpError
	if !errors.As(err, &oe) {
		t.Fatalf("want *OpError, got %T: %v", err, err)
	}
	return oe
}

func baseSystem(t *testing.T, hold int) *System {
	t.Helper()
	return NewSystem(
		map[int]int{1: 10, 2: 6},
		[]CapacityRecord{{EffectiveAt: 0, Capacity: 10}},
		Config{HoldDuration: hold},
	)
}

// Adjacent intervals [0,5) and [5,10) do not conflict.
func TestAdjacentIntervalsNoConflict(t *testing.T) {
	sys := baseSystem(t, 3)
	a, err := sys.Create(0, 1, Interval{0, 5}, 10)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sys.Create(0, 1, Interval{5, 10}, 10)
	if err != nil {
		t.Fatalf("end-adjacent reservation rejected: %v", err)
	}
	if err := sys.Confirm(0, a); err != nil {
		t.Fatal(err)
	}
	if err := sys.Confirm(0, b); err != nil {
		t.Fatal(err)
	}
}

// Confirm exactly at the expiry instant is rejected; the reservation turns void.
func TestConfirmAtExpiryInstant(t *testing.T) {
	sys := baseSystem(t, 3)
	id, err := sys.Create(0, 1, Interval{0, 10}, 5)
	if err != nil {
		t.Fatal(err)
	}
	err = sys.Confirm(3, id)
	if err == nil {
		t.Fatal("confirm at expiry instant must fail")
	}
	if oe := asOpErr(t, err); oe.Kind != ErrHoldExpired {
		t.Fatalf("want 占位已到期, got %v", oe.Kind)
	}
	r, _ := sys.Get(id)
	if r.State != StateVoid {
		t.Fatalf("want void, got %v", r.State)
	}
	if _, err := sys.Create(3, 1, Interval{3, 6}, 10); err != nil {
		t.Fatalf("occupancy not released after expiry: %v", err)
	}
}

func TestConfirmJustBeforeExpiry(t *testing.T) {
	sys := baseSystem(t, 3)
	id, err := sys.Create(0, 1, Interval{0, 10}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Confirm(2, id); err != nil {
		t.Fatalf("confirm before expiry rejected: %v", err)
	}
}

func TestFeederWinsOverTransformer(t *testing.T) {
	sys := NewSystem(
		map[int]int{1: 10},
		[]CapacityRecord{{EffectiveAt: 0, Capacity: 10}},
		Config{HoldDuration: 10},
	)
	if _, err := sys.Create(0, 1, Interval{0, 10}, 6); err != nil {
		t.Fatal(err)
	}
	_, err := sys.Create(0, 1, Interval{0, 10}, 6)
	oe := asOpErr(t, err)
	if oe.Kind != ErrCapacity || oe.CapInfo.Level != LevelFeeder || oe.CapInfo.Time != 0 {
		t.Fatalf("want feeder failure at 0, got %+v", oe.CapInfo)
	}
}

func TestTransformerFailureFirstTime(t *testing.T) {
	sys := NewSystem(
		map[int]int{1: 10, 2: 10},
		[]CapacityRecord{{EffectiveAt: 0, Capacity: 10}, {EffectiveAt: 4, Capacity: 5}},
		Config{HoldDuration: 10},
	)
	if _, err := sys.Create(0, 1, Interval{0, 10}, 4); err != nil {
		t.Fatal(err)
	}
	// Second feeder usage ends exactly when the transformer capacity drops.
	if _, err := sys.Create(0, 2, Interval{0, 4}, 4); err != nil {
		t.Fatal(err)
	}
	// Feeder 1 keeps 4; adding 2 fits on [2,4) (cap 10) but fails at t=4
	// where the transformer drops to 5: 4+2 > 5, first bad time is 4.
	_, err := sys.Create(0, 1, Interval{2, 8}, 2)
	oe := asOpErr(t, err)
	if oe.CapInfo.Level != LevelTransformer || oe.CapInfo.Time != 4 {
		t.Fatalf("want transformer@4, got %+v", oe.CapInfo)
	}
}

func TestRescheduleExcludesSelf(t *testing.T) {
	sys := baseSystem(t, 10)
	id, err := sys.Create(0, 1, Interval{0, 10}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Reschedule(0, id, Interval{2, 12}, 10); err != nil {
		t.Fatalf("self-overlapping reschedule rejected: %v", err)
	}
	if _, err := sys.Create(0, 1, Interval{2, 12}, 10); err == nil {
		t.Fatal("expected capacity failure")
	}
	if _, err := sys.Create(0, 1, Interval{0, 2}, 10); err != nil {
		t.Fatalf("original reservation should remain [2,12): %v", err)
	}
}

func TestRescheduleKeepsStateAndExpiry(t *testing.T) {
	sys := baseSystem(t, 5)
	id, err := sys.Create(0, 1, Interval{0, 10}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Reschedule(1, id, Interval{1, 9}, 4); err != nil {
		t.Fatal(err)
	}
	r, _ := sys.Get(id)
	if r.State != StateHolding || r.ExpiresAt != 5 {
		t.Fatalf("state/expiry changed: %+v", r)
	}
}

func TestCapacityDecreaseBlockedByConfirmed(t *testing.T) {
	sys := NewSystem(
		map[int]int{1: 100},
		[]CapacityRecord{{EffectiveAt: 0, Capacity: 100}},
		Config{HoldDuration: 10},
	)
	cid, err := sys.Create(0, 1, Interval{0, 20}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Confirm(0, cid); err != nil {
		t.Fatal(err)
	}
	hid, err := sys.Create(0, 1, Interval{0, 20}, 50)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sys.ChangeCapacity(5, CapacityRecord{EffectiveAt: 5, Capacity: 40})
	oe := asOpErr(t, err)
	if oe.Kind != ErrCapacity || oe.CapInfo.Time != 5 {
		t.Fatalf("want confirmed block @5, got %+v", oe.CapInfo)
	}
	r, _ := sys.Get(hid)
	if r.State != StateHolding {
		t.Fatalf("hold touched by rejected change: %v", r.State)
	}
}

func TestCapacityDecreaseCancelsLatestHolds(t *testing.T) {
	sys := NewSystem(
		map[int]int{1: 100},
		[]CapacityRecord{{EffectiveAt: 0, Capacity: 100}},
		Config{HoldDuration: 100},
	)
	cid, _ := sys.Create(0, 1, Interval{0, 20}, 40)
	if err := sys.Confirm(0, cid); err != nil {
		t.Fatal(err)
	}
	h1, _ := sys.Create(0, 1, Interval{0, 20}, 20)
	h2, _ := sys.Create(1, 1, Interval{0, 20}, 20)
	h3, _ := sys.Create(2, 1, Interval{0, 20}, 20)
	cancelled, err := sys.ChangeCapacity(3, CapacityRecord{EffectiveAt: 5, Capacity: 50})
	if err != nil {
		t.Fatal(err)
	}
	// Occupancy 100 -> cap 50: h3 (createdAt 2), h2 (1), h1 (0) are cancelled
	// in exactly the latest-CreatedAt-first order.
	want := []int64{h3, h2, h1}
	if len(cancelled) != len(want) {
		t.Fatalf("cancel count want %d, got %v", len(want), cancelled)
	}
	for i, id := range want {
		if cancelled[i] != id {
			t.Fatalf("cancel order want %v, got %v", want, cancelled)
		}
	}
	err = sys.Confirm(4, h2)
	if err == nil || asOpErr(t, err).Kind != ErrCancelled {
		t.Fatalf("cancelled hold confirm want 已取消, got %v", err)
	}
	err = sys.Reschedule(4, h2, Interval{5, 6}, 1)
	if err == nil || asOpErr(t, err).Kind != ErrCancelled {
		t.Fatalf("cancelled hold reschedule want 已取消, got %v", err)
	}
	err = sys.Release(4, h2)
	if err == nil || asOpErr(t, err).Kind != ErrCancelled {
		t.Fatalf("cancelled hold release want 已取消, got %v", err)
	}
}

func TestCapacityEffectiveAtReservationStart(t *testing.T) {
	sys := NewSystem(
		map[int]int{1: 100},
		[]CapacityRecord{{EffectiveAt: 0, Capacity: 100}, {EffectiveAt: 5, Capacity: 6}},
		Config{HoldDuration: 10},
	)
	_, err := sys.Create(0, 1, Interval{5, 10}, 7)
	oe := asOpErr(t, err)
	if oe.CapInfo.Time != 5 || oe.CapInfo.Level != LevelTransformer {
		t.Fatalf("want transformer@5, got %+v", oe.CapInfo)
	}
	if _, err := sys.Create(0, 1, Interval{5, 10}, 6); err != nil {
		t.Fatal(err)
	}
}

func TestEarlyFinishStartedConfirmed(t *testing.T) {
	sys := baseSystem(t, 10)
	id, err := sys.Create(0, 1, Interval{0, 20}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := sys.Confirm(0, id); err != nil {
		t.Fatal(err)
	}
	if err := sys.Advance(7); err != nil {
		t.Fatal(err)
	}
	// end == now is legal: [0,7) is empty from now on.
	if err := sys.Reschedule(7, id, Interval{0, 7}, 5); err != nil {
		t.Fatalf("end==now should be allowed, got %v", err)
	}
	r, _ := sys.Get(id)
	if r.State != StateConfirmed {
		t.Fatalf("reschedule of completed-by-shorten keeps state until settle, got %v", r.State)
	}
	// New full-capacity booking is now possible over [7,10).
	if _, err := sys.Create(7, 1, Interval{7, 10}, 10); err != nil {
		t.Fatal(err)
	}
}

func TestEarlyFinishRejectsEndBeforeNow(t *testing.T) {
	sys := baseSystem(t, 10)
	id, _ := sys.Create(0, 1, Interval{0, 20}, 5)
	if err := sys.Confirm(0, id); err != nil {
		t.Fatal(err)
	}
	if err := sys.Advance(7); err != nil {
		t.Fatal(err)
	}
	err := sys.Reschedule(7, id, Interval{0, 6}, 5)
	if err == nil || asOpErr(t, err).Kind != ErrInvalidParam {
		t.Fatalf("end<now must be 参数非法, got %v", err)
	}
	err = sys.Reschedule(7, id, Interval{1, 8}, 5)
	if err == nil || asOpErr(t, err).Kind != ErrInvalidParam {
		t.Fatalf("moving start must be 参数非法, got %v", err)
	}
}

func TestConfirmedRescheduleStartBeforeNow(t *testing.T) {
	sys := baseSystem(t, 10)
	id, _ := sys.Create(0, 1, Interval{5, 20}, 5)
	if err := sys.Confirm(0, id); err != nil {
		t.Fatal(err)
	}
	if err := sys.Advance(3); err != nil {
		t.Fatal(err)
	}
	err := sys.Reschedule(3, id, Interval{2, 20}, 5)
	if err == nil || asOpErr(t, err).Kind != ErrInvalidParam {
		t.Fatalf("want 参数非法, got %v", err)
	}
}

func TestRejectionOrder(t *testing.T) {
	sys := baseSystem(t, 3)
	// 参数非法 beats 时钟回退: power over the feeder limit on a Create call
	// with a past clock (existence is irrelevant for create).
	_, err := sys.Create(9, 1, Interval{0, 5}, 100)
	if oe := asOpErr(t, err); oe.Kind != ErrInvalidParam {
		t.Fatalf("create past clock with bad power: want invalid param, got %v", oe.Kind)
	}
	if err := sys.Advance(1); err != nil {
		t.Fatal(err)
	}
	// 时钟回退 beats 预约不存在.
	err = sys.Confirm(0, 999)
	if oe := asOpErr(t, err); oe.Kind != ErrClockRollback {
		t.Fatalf("want clock rollback, got %v", oe.Kind)
	}
	err = sys.Confirm(1, 999)
	if err == nil || asOpErr(t, err).Kind != ErrNotFound {
		t.Fatalf("want not found, got %v", err)
	}
	id, _ := sys.Create(1, 1, Interval{1, 11}, 3)
	err = sys.Reschedule(4, id, Interval{0, 10}, 3)
	if oe := asOpErr(t, err); oe.Kind != ErrHoldExpired {
		t.Fatalf("want hold expired, got %v", oe.Kind)
	}
	err = sys.Release(4, id)
	if oe := asOpErr(t, err); oe.Kind != ErrStateNotAllowed {
		t.Fatalf("want state not allowed, got %v", oe.Kind)
	}
}

func TestParamErrors(t *testing.T) {
	sys := baseSystem(t, 3)
	cases := []struct {
		name   string
		feeder int
		iv     Interval
		power  int
	}{
		{"bad interval", 1, Interval{5, 5}, 1},
		{"nonpositive power", 1, Interval{0, 5}, 0},
		{"power over feeder limit", 1, Interval{0, 5}, 11},
		{"unknown feeder", 99, Interval{0, 5}, 1},
	}
	for _, c := range cases {
		if _, err := sys.Create(0, c.feeder, c.iv, c.power); err == nil ||
			asOpErr(t, err).Kind != ErrInvalidParam {
			t.Fatalf("%s: want 参数非法, got %v", c.name, err)
		}
	}
	if _, err := sys.ChangeCapacity(1, CapacityRecord{EffectiveAt: 0, Capacity: 5}); err == nil ||
		asOpErr(t, err).Kind != ErrInvalidParam {
		t.Fatalf("capacity eff before now: want 参数非法, got %v", err)
	}
}

func TestReleaseFreesOccupancy(t *testing.T) {
	sys := baseSystem(t, 10)
	id, _ := sys.Create(0, 1, Interval{0, 10}, 10)
	if _, err := sys.Create(0, 1, Interval{0, 10}, 1); err == nil {
		t.Fatal("feeder should be full")
	}
	if err := sys.Release(0, id); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Create(0, 1, Interval{0, 10}, 10); err != nil {
		t.Fatalf("occupancy not freed: %v", err)
	}
	// Released reservation is terminal.
	if err := sys.Release(0, id); err == nil || asOpErr(t, err).Kind != ErrStateNotAllowed {
		t.Fatalf("want state not allowed, got %v", err)
	}
}

func TestEventTreapPrefix(t *testing.T) {
	tr := newEventTreap()
	// Reservation-style deltas: [1,4)+3, [4,6)+3 adjacent -> 3 at t=1..5.
	tr.add(1, 3)
	tr.add(4, -3)
	tr.add(4, 3)
	tr.add(6, -3)
	cases := map[int]int{0: 0, 1: 3, 3: 3, 4: 3, 5: 3, 6: 0, 7: 0}
	for tm, want := range cases {
		if got := tr.prefixBefore(tm + 1); got != want {
			t.Fatalf("occupancy at %d = %d, want %d", tm, got, want)
		}
	}
	var keys []int
	tr.keysIn(0, 6, &keys)
	wantKeys := []int{1, 4}
	if len(keys) != len(wantKeys) {
		t.Fatalf("keysIn want %v got %v", wantKeys, keys)
	}
	for i := range keys {
		if keys[i] != wantKeys[i] {
			t.Fatalf("keysIn want %v got %v", wantKeys, keys)
		}
	}
	// Removing the last net delta at a time empties the node.
	tr.remove(1, 3)
	if got := tr.prefixBefore(2); got != 0 {
		t.Fatalf("after remove occupancy = %d, want 0", got)
	}
}

func TestExpiryTreapDrain(t *testing.T) {
	tr := newExpiryTreap()
	tr.add(10, 3)
	tr.add(5, 1)
	tr.add(7, 2)
	var got []int64
	tr.drain(7, func(exp int, id int64) {
		if exp > 7 {
			t.Fatalf("drained unexpired %d@%d", id, exp)
		}
		got = append(got, id)
	})
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("drain order want [1 2], got %v", got)
	}
	got = nil
	tr.drain(100, func(exp int, id int64) { got = append(got, id) })
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("remaining drain want [3], got %v", got)
	}
}
