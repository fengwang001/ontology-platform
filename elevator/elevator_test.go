package elevator

import (
	"errors"
	"reflect"
	"testing"
)

func TestConstructionRejectsInvalidArguments(t *testing.T) {
	if _, err := New(1, 1); !errors.Is(err, ErrInvalidFloors) {
		t.Fatalf("New(1, 1) error = %v, want %v", err, ErrInvalidFloors)
	}
	if _, err := New(2, 0); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("New(2, 0) error = %v, want %v", err, ErrInvalidCapacity)
	}
}

func TestFullCapacityLeavesSameFloorWaitingThenBoardsAfterAlighting(t *testing.T) {
	lift, err := New(5, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustCall(t, lift, 1, 1, 2)
	mustCall(t, lift, 2, 1, 3)
	mustCall(t, lift, 3, 2, 4)

	first := lift.Tick()
	assertTick(t, first, TickResult{BeforeFloor: 1, Boarded: []int{1}, Direction: Up, AfterFloor: 2})
	second := lift.Tick()
	assertTick(t, second, TickResult{BeforeFloor: 2, Alighted: []int{1}, Boarded: []int{3}, Direction: Up, AfterFloor: 3})
	third := lift.Tick()
	assertTick(t, third, TickResult{BeforeFloor: 3, Direction: Up, AfterFloor: 4})
	fourth := lift.Tick()
	assertTick(t, fourth, TickResult{BeforeFloor: 4, Alighted: []int{3}, Direction: Down, AfterFloor: 3})
	fifth := lift.Tick()
	assertTick(t, fifth, TickResult{BeforeFloor: 3, Direction: Down, AfterFloor: 2})
	sixth := lift.Tick()
	assertTick(t, sixth, TickResult{BeforeFloor: 2, Direction: Down, AfterFloor: 1})
	seventh := lift.Tick()
	assertTick(t, seventh, TickResult{BeforeFloor: 1, Boarded: []int{2}, Direction: Up, AfterFloor: 2})
	assertPassenger(t, lift, 1, PassengerInfo{ID: 1, From: 1, To: 2, Status: Delivered, BoardedTick: 1, ExitedTick: 2})
	assertPassenger(t, lift, 2, PassengerInfo{ID: 2, From: 1, To: 3, Status: InCar, BoardedTick: 7})
	assertPassenger(t, lift, 3, PassengerInfo{ID: 3, From: 2, To: 4, Status: Delivered, BoardedTick: 2, ExitedTick: 4})
}

func TestDownwardPassengerBoardsWhileCarTravelsUp(t *testing.T) {
	lift, err := New(5, 3)
	if err != nil {
		t.Fatal(err)
	}
	mustCall(t, lift, 1, 1, 4)
	mustCall(t, lift, 2, 2, 1)

	first := lift.Tick()
	assertTick(t, first, TickResult{BeforeFloor: 1, Boarded: []int{1}, Direction: Up, AfterFloor: 2})
	second := lift.Tick()
	assertTick(t, second, TickResult{BeforeFloor: 2, Boarded: []int{2}, Direction: Up, AfterFloor: 3})
	third := lift.Tick()
	assertTick(t, third, TickResult{BeforeFloor: 3, Direction: Up, AfterFloor: 4})
	fourth := lift.Tick()
	assertTick(t, fourth, TickResult{BeforeFloor: 4, Alighted: []int{1}, Direction: Down, AfterFloor: 3})
}

func TestIdleTieChoosesUpAndIncludesCarAndWaitingTargets(t *testing.T) {
	t.Run("nearest waiting target wins over farther car target", func(t *testing.T) {
		lift, err := New(7, 2)
		if err != nil {
			t.Fatal(err)
		}
		mustCall(t, lift, 1, 5, 2)
		mustCall(t, lift, 2, 3, 7)
		for floor := 1; floor < 7; floor++ {
			result := lift.Tick()
			wantBoard := []int{}
			if floor == 3 {
				wantBoard = []int{2}
			}
			if floor == 5 {
				wantBoard = []int{1}
			}
			assertTick(t, result, TickResult{BeforeFloor: floor, Boarded: wantBoard, Direction: Up, AfterFloor: floor + 1})
		}
		assertTick(t, lift.Tick(), TickResult{BeforeFloor: 7, Alighted: []int{2}, Direction: Down, AfterFloor: 6})
		mustCall(t, lift, 3, 2, 6)
		mustCall(t, lift, 4, 1, 2)
		for floor := 6; floor > 2; floor-- {
			assertTick(t, lift.Tick(), TickResult{BeforeFloor: floor, Direction: Down, AfterFloor: floor - 1})
		}
		assertTick(t, lift.Tick(), TickResult{BeforeFloor: 2, Alighted: []int{1}, Boarded: []int{3}, Direction: Down, AfterFloor: 1})
	})

	t.Run("equal distances choose up", func(t *testing.T) {
		lift, err := New(7, 3)
		if err != nil {
			t.Fatal(err)
		}
		mustCall(t, lift, 1, 6, 2)
		mustCall(t, lift, 2, 3, 6)
		for floor := 1; floor <= 6; floor++ {
			result := lift.Tick()
			wantBoard := []int{}
			wantAlighted := []int{}
			wantDirection := Direction(Up)
			afterFloor := floor + 1
			if floor == 3 {
				wantBoard = []int{2}
			}
			if floor == 6 {
				wantAlighted = []int{2}
				wantBoard = []int{1}
				wantDirection = Down
				afterFloor = 5
			}
			assertTick(t, result, TickResult{BeforeFloor: floor, Alighted: wantAlighted, Boarded: wantBoard, Direction: wantDirection, AfterFloor: afterFloor})
		}
		mustCall(t, lift, 3, 2, 7)
		for floor := 5; floor > 2; floor-- {
			assertTick(t, lift.Tick(), TickResult{BeforeFloor: floor, Direction: Down, AfterFloor: floor - 1})
		}
		assertTick(t, lift.Tick(), TickResult{BeforeFloor: 2, Alighted: []int{1}, Boarded: []int{3}, Direction: Up, AfterFloor: 3})
	})
}

func TestDirectionHoldsUntilNoTargetAheadThenReverses(t *testing.T) {
	lift, err := New(7, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustCall(t, lift, 1, 1, 5)
	mustCall(t, lift, 2, 2, 1)
	lift.Tick()
	lift.Tick()
	mustCall(t, lift, 3, 6, 1)

	result := lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 3, Direction: Up, AfterFloor: 4})
	result = lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 4, Direction: Up, AfterFloor: 5})
	result = lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 5, Alighted: []int{1}, Direction: Up, AfterFloor: 6})
	result = lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 6, Boarded: []int{3}, Direction: Down, AfterFloor: 5})
	result = lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 5, Direction: Down, AfterFloor: 4})
	result = lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 4, Direction: Down, AfterFloor: 3})
	result = lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 3, Direction: Down, AfterFloor: 2})
	result = lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 2, Direction: Down, AfterFloor: 1})
	result = lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 1, Alighted: []int{2, 3}, Direction: Idle, AfterFloor: 1})
}

func TestIdleCarWakesForCallOnCurrentFloorAndBoardsImmediately(t *testing.T) {
	lift, err := New(5, 2)
	if err != nil {
		t.Fatal(err)
	}
	mustCall(t, lift, 1, 1, 3)
	lift.Tick()
	lift.Tick()
	lift.Tick()
	mustCall(t, lift, 2, 3, 1)

	result := lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 3, Boarded: []int{2}, Direction: Down, AfterFloor: 2})
}

func TestRejectedCallAndPassengerQueryDoNotChangeState(t *testing.T) {
	lift, err := New(3, 1)
	if err != nil {
		t.Fatal(err)
	}
	mustCall(t, lift, 1, 1, 2)

	invalidID := lift.Call(0, 1, 3)
	invalidFloor := lift.Call(2, 0, 3)
	sameFloor := lift.Call(2, 1, 1)
	duplicate := lift.Call(1, 1, 3)
	for name, actual := range map[string]error{
		"id":        invalidID,
		"floor":     invalidFloor,
		"same":      sameFloor,
		"duplicate": duplicate,
	} {
		if actual == nil {
			t.Fatalf("%s rejection: expected error", name)
		}
	}
	if !errors.Is(invalidID, ErrInvalidID) || !errors.Is(invalidFloor, ErrInvalidFloor) || !errors.Is(sameFloor, ErrSameFloor) || !errors.Is(duplicate, ErrDuplicateCall) {
		t.Fatalf("rejection reasons = %v, %v, %v, %v", invalidID, invalidFloor, sameFloor, duplicate)
	}

	result := lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 1, Boarded: []int{1}, Direction: Up, AfterFloor: 2})
	result = lift.Tick()
	assertTick(t, result, TickResult{BeforeFloor: 2, Alighted: []int{1}, Direction: Idle, AfterFloor: 2})
	if _, err := lift.Passenger(2); !errors.Is(err, ErrPassengerMissing) {
		t.Fatalf("Passenger(2) error = %v, want %v", err, ErrPassengerMissing)
	}
	assertPassenger(t, lift, 1, PassengerInfo{ID: 1, From: 1, To: 2, Status: Delivered, BoardedTick: 1, ExitedTick: 2})
}

func mustCall(t *testing.T, lift *Elevator, id, from, to int) {
	t.Helper()
	if err := lift.Call(id, from, to); err != nil {
		t.Fatalf("Call(%d, %d, %d): %v", id, from, to, err)
	}
}

func assertTick(t *testing.T, got, want TickResult) {
	t.Helper()
	normalizeSlices(&got)
	normalizeSlices(&want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tick() = %+v, want %+v", got, want)
	}
}

func normalizeSlices(result *TickResult) {
	if result.Alighted == nil {
		result.Alighted = []int{}
	}
	if result.Boarded == nil {
		result.Boarded = []int{}
	}
}

func assertPassenger(t *testing.T, lift *Elevator, id int, want PassengerInfo) {
	t.Helper()
	got, err := lift.Passenger(id)
	if err != nil {
		t.Fatalf("Passenger(%d): %v", id, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Passenger(%d) = %+v, want %+v", id, got, want)
	}
}
