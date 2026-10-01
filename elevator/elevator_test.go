package elevator

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func callReason(t *testing.T, err error) CallErrorReason {
	t.Helper()
	var callErr *CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected *CallError, got %v", err)
	}
	return callErr.Reason
}

func mustCall(t *testing.T, elevator *Elevator, id, from, to int) {
	t.Helper()
	if err := elevator.Call(id, from, to); err != nil {
		t.Fatalf("Call(%d,%d,%d): %v", id, from, to, err)
	}
}

func requireInfo(t *testing.T, elevator *Elevator, id int, status PassengerStatus, boardTick, deliverTick int) PassengerInfo {
	t.Helper()
	info, err := elevator.Passenger(id)
	if err != nil {
		t.Fatalf("passenger %d: %v", id, err)
	}
	if info.Status != status || info.BoardTick != boardTick || info.DeliverTick != deliverTick {
		t.Fatalf("passenger %d = %+v, want status=%v board=%d deliver=%d", id, info, status, boardTick, deliverTick)
	}
	return info
}

func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func sameIDTwice(left, right []int) bool {
	for _, id := range left {
		for _, other := range right {
			if id == other {
				return true
			}
		}
	}
	return false
}

func tickResultsEqual(got, want TickResult) bool {
	return got.BeforeFloor == want.BeforeFloor &&
		got.CurrentFloor == want.CurrentFloor &&
		got.Direction == want.Direction &&
		equalInts(got.Exited, want.Exited) &&
		equalInts(got.Boarded, want.Boarded)
}

func errorStrings(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func joinLogs(lines []string) string {
	result := ""
	for _, line := range lines {
		result += line + "\n"
	}
	return result
}

func lastLogs(lines []string, limit int) []string {
	if len(lines) <= limit {
		return lines
	}
	return lines[len(lines)-limit:]
}

func TestRejectsInvalidConstruction(t *testing.T) {
	for _, params := range [][2]int{{1, 1}, {2, 0}, {0, 2}} {
		if elevator, err := NewElevator(params[0], params[1]); err == nil {
			t.Fatalf("NewElevator(%d, %d) = %v, want error", params[0], params[1], elevator)
		}
	}
}

func TestCallReasonsAreDistinguishableAndOrdered(t *testing.T) {
	elevator, err := NewElevator(5, 2)
	if err != nil {
		t.Fatal(err)
	}

	if reason := callReason(t, elevator.Call(0, 6, 6)); reason != InvalidCallID {
		t.Fatalf("reason = %v, want InvalidCallID", reason)
	}
	if reason := callReason(t, elevator.Call(1, 0, 6)); reason != InvalidFloor {
		t.Fatalf("reason = %v, want InvalidFloor", reason)
	}
	if reason := callReason(t, elevator.Call(1, 3, 3)); reason != SameFloor {
		t.Fatalf("reason = %v, want SameFloor", reason)
	}
	if err := elevator.Call(1, 2, 4); err != nil {
		t.Fatal(err)
	}
	if reason := callReason(t, elevator.Call(1, 3, 5)); reason != DuplicateCallID {
		t.Fatalf("reason = %v, want DuplicateCallID", reason)
	}

	result := elevator.Tick()
	if result.BeforeFloor != 1 || result.CurrentFloor != 2 || result.Direction != Up {
		t.Fatalf("accepted call moved elevator incorrectly: %+v", result)
	}

	var queryErr *QueryError
	_, err = elevator.Passenger(99)
	if !errors.As(err, &queryErr) || queryErr.Reason != PassengerNotFound {
		t.Fatalf("query error = %v, want PassengerNotFound", err)
	}
}

func TestFullCarLeavesSameFloorWaitingThenBoardsAfterExit(t *testing.T) {
	elevator, _ := NewElevator(5, 2)
	mustCall(t, elevator, 1, 1, 3)
	mustCall(t, elevator, 2, 1, 4)
	mustCall(t, elevator, 3, 1, 5)

	first := elevator.Tick()
	if !equalInts(first.Boarded, []int{1, 2}) || !equalInts(first.Exited, nil) {
		t.Fatalf("first tick = %+v", first)
	}
	requireInfo(t, elevator, 3, Waiting, 0, 0)

	second := elevator.Tick()
	if second.BeforeFloor != 2 || second.CurrentFloor != 3 || len(second.Boarded) != 0 {
		t.Fatalf("second tick = %+v", second)
	}

	mustCall(t, elevator, 4, 3, 2)
	third := elevator.Tick()
	if third.BeforeFloor != 3 || !equalInts(third.Exited, []int{1}) || !equalInts(third.Boarded, []int{4}) {
		t.Fatalf("third tick = %+v", third)
	}
	if sameIDTwice(third.Exited, third.Boarded) {
		t.Fatalf("exited passenger boarded immediately: %+v", third)
	}
	requireInfo(t, elevator, 1, Delivered, 1, 3)
	requireInfo(t, elevator, 3, Waiting, 0, 0)
	requireInfo(t, elevator, 4, InCar, 3, 0)
}

func TestDownwardPassengerBoardsWhileElevatorMovesUp(t *testing.T) {
	elevator, _ := NewElevator(5, 3)
	mustCall(t, elevator, 1, 1, 5)
	first := elevator.Tick()
	if first.Direction != Up || first.CurrentFloor != 2 {
		t.Fatalf("first = %+v", first)
	}

	mustCall(t, elevator, 2, 2, 1)
	second := elevator.Tick()
	if !equalInts(second.Boarded, []int{2}) || second.Direction != Up || second.CurrentFloor != 3 {
		t.Fatalf("second = %+v", second)
	}
	requireInfo(t, elevator, 2, InCar, 2, 0)
}

func TestNearestTargetTieChoosesUp(t *testing.T) {
	t.Run("waiting targets", func(t *testing.T) {
		elevator, _ := NewElevator(5, 2)
		mustCall(t, elevator, 1, 2, 1)
		mustCall(t, elevator, 2, 4, 5)
		result := elevator.Tick()
		if result.BeforeFloor != 1 || result.Direction != Up || result.CurrentFloor != 2 {
			t.Fatalf("result = %+v", result)
		}
	})

	t.Run("car target farther but waiting closer", func(t *testing.T) {
		elevator, _ := NewElevator(7, 2)
		mustCall(t, elevator, 1, 1, 7)
		elevator.Tick()
		mustCall(t, elevator, 2, 2, 1)
		mustCall(t, elevator, 3, 5, 7)
		result := elevator.Tick()
		if !equalInts(result.Boarded, []int{2}) || result.Direction != Up || result.CurrentFloor != 3 {
			t.Fatalf("result = %+v, want closer target floor 5 upward", result)
		}
	})

	t.Run("car target and waiting target equally near", func(t *testing.T) {
		elevator, _ := NewElevator(7, 1)
		mustCall(t, elevator, 1, 1, 5)
		elevator.Tick()
		elevator.Tick()
		mustCall(t, elevator, 2, 1, 7)
		result := elevator.Tick()
		if len(result.Boarded) != 0 || result.BeforeFloor != 3 || result.Direction != Up || result.CurrentFloor != 4 {
			t.Fatalf("result = %+v, want equal-distance tie upward", result)
		}
	})
}

func TestDirectionHoldsUntilNoTargetAheadThenReverses(t *testing.T) {
	elevator, _ := NewElevator(7, 3)
	mustCall(t, elevator, 1, 1, 5)
	elevator.Tick()
	elevator.Tick()
	mustCall(t, elevator, 2, 1, 7)

	result := elevator.Tick()
	if result.BeforeFloor != 3 || result.Direction != Up || result.CurrentFloor != 4 {
		t.Fatalf("holding result = %+v", result)
	}
	result = elevator.Tick()
	if result.BeforeFloor != 4 || result.Direction != Up || result.CurrentFloor != 5 {
		t.Fatalf("continued up result = %+v", result)
	}
	result = elevator.Tick()
	if result.BeforeFloor != 5 || !equalInts(result.Exited, []int{1}) || result.Direction != Down || result.CurrentFloor != 4 {
		t.Fatalf("reverse result = %+v", result)
	}
}

func TestIdleWakesAtCurrentFloorAndBoardsImmediately(t *testing.T) {
	elevator, _ := NewElevator(4, 2)
	mustCall(t, elevator, 1, 1, 4)
	result := elevator.Tick()
	if !equalInts(result.Boarded, []int{1}) || result.Direction != Up || result.CurrentFloor != 2 {
		t.Fatalf("wake result = %+v", result)
	}
}

func TestIdleAfterAllDeliveredAndFloorStaysFixed(t *testing.T) {
	elevator, _ := NewElevator(4, 2)
	mustCall(t, elevator, 1, 1, 3)
	elevator.Tick()
	elevator.Tick()
	result := elevator.Tick()
	if !equalInts(result.Exited, []int{1}) || result.Direction != Idle || result.CurrentFloor != 3 {
		t.Fatalf("delivery result = %+v", result)
	}
	result = elevator.Tick()
	if result.BeforeFloor != 3 || result.CurrentFloor != 3 || result.Direction != Idle || len(result.Exited) != 0 || len(result.Boarded) != 0 {
		t.Fatalf("idle result = %+v", result)
	}
	requireInfo(t, elevator, 1, Delivered, 1, 3)
}

type naivePassenger struct {
	id          int
	from        int
	to          int
	status      PassengerStatus
	boardTick   int
	deliverTick int
}

type naiveElevator struct {
	floors     int
	capacity   int
	floor      int
	direction  Direction
	tickCount  int
	waiting    []*naivePassenger
	car        []*naivePassenger
	passengers map[int]*naivePassenger
}

func newNaiveElevator(floors, capacity int) *naiveElevator {
	return &naiveElevator{
		floors:     floors,
		capacity:   capacity,
		floor:      1,
		direction:  Idle,
		passengers: make(map[int]*naivePassenger),
	}
}

func (n *naiveElevator) call(id, from, to int) error {
	if id < 1 {
		return &CallError{Reason: InvalidCallID}
	}
	if from < 1 || from > n.floors || to < 1 || to > n.floors {
		return &CallError{Reason: InvalidFloor}
	}
	if from == to {
		return &CallError{Reason: SameFloor}
	}
	if _, exists := n.passengers[id]; exists {
		return &CallError{Reason: DuplicateCallID}
	}
	p := &naivePassenger{id: id, from: from, to: to, status: Waiting}
	n.passengers[id] = p
	n.waiting = append(n.waiting, p)
	return nil
}

func (n *naiveElevator) tick() TickResult {
	n.tickCount++
	before := n.floor
	exited := []int{}
	stillInCar := []*naivePassenger{}
	for _, p := range n.car {
		if p.to == n.floor {
			p.status = Delivered
			p.deliverTick = n.tickCount
			exited = append(exited, p.id)
		} else {
			stillInCar = append(stillInCar, p)
		}
	}
	n.car = stillInCar

	boarded := []int{}
	stillWaiting := []*naivePassenger{}
	for _, p := range n.waiting {
		if p.from == n.floor && len(n.car) < n.capacity {
			p.status = InCar
			p.boardTick = n.tickCount
			n.car = append(n.car, p)
			boarded = append(boarded, p.id)
		} else {
			stillWaiting = append(stillWaiting, p)
		}
	}
	n.waiting = stillWaiting

	targets := map[int]bool{}
	for _, p := range n.car {
		if p.to != n.floor {
			targets[p.to] = true
		}
	}
	for _, p := range n.waiting {
		if p.from != n.floor {
			targets[p.from] = true
		}
	}

	if len(targets) == 0 {
		n.direction = Idle
	} else {
		if n.direction == Up {
			found := false
			for target := range targets {
				if target > n.floor {
					found = true
				}
			}
			if !found {
				n.direction = n.naiveNearestDirection(targets)
			}
		} else if n.direction == Down {
			found := false
			for target := range targets {
				if target < n.floor {
					found = true
				}
			}
			if !found {
				n.direction = n.naiveNearestDirection(targets)
			}
		} else {
			n.direction = n.naiveNearestDirection(targets)
		}

		if n.direction == Up {
			n.floor++
		} else if n.direction == Down {
			n.floor--
		}
	}

	return TickResult{
		BeforeFloor:  before,
		Exited:       exited,
		Boarded:      boarded,
		Direction:    n.direction,
		CurrentFloor: n.floor,
	}
}

func (n *naiveElevator) naiveNearestDirection(targets map[int]bool) Direction {
	best := 0
	bestDistance := 0
	for floor := 1; floor <= n.floors; floor++ {
		if !targets[floor] {
			continue
		}
		distance := floor - n.floor
		if distance < 0 {
			distance = -distance
		}
		if best == 0 || distance < bestDistance || (distance == bestDistance && floor > n.floor) {
			best = floor
			bestDistance = distance
		}
	}
	if best > n.floor {
		return Up
	}
	return Down
}

func (n *naiveElevator) passenger(id int) (PassengerInfo, error) {
	p, exists := n.passengers[id]
	if !exists {
		return PassengerInfo{}, &QueryError{Reason: PassengerNotFound}
	}
	return PassengerInfo{
		Status:      p.status,
		BoardTick:   p.boardTick,
		DeliverTick: p.deliverTick,
	}, nil
}

func TestRandomSequencesMatchNaiveSimulation(t *testing.T) {
	const cases = 2000
	rng := rand.New(rand.NewSource(20261001))

	for caseNumber := 0; caseNumber < cases; caseNumber++ {
		floors := rng.Intn(8) + 2
		capacity := rng.Intn(3) + 1
		elevator, err := NewElevator(floors, capacity)
		if err != nil {
			t.Fatal(err)
		}
		reference := newNaiveElevator(floors, capacity)
		log := make([]string, 0, 64)

		operations := rng.Intn(45) + 1
		for operation := 0; operation < operations; operation++ {
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4:
				id := rng.Intn(18) + 1
				from := rng.Intn(floors) + 1
				to := rng.Intn(floors) + 1
				gotErr := elevator.Call(id, from, to)
				wantErr := reference.call(id, from, to)
				line := fmt.Sprintf("case=%d op=%d input=Call(id=%d,from=%d,to=%d) output=%q reference=%q basis=ordered-validation-and-queue-append", caseNumber, operation, id, from, to, errorStrings(gotErr), errorStrings(wantErr))
				log = append(log, line)
				if errorStrings(gotErr) != errorStrings(wantErr) {
					t.Fatalf("call mismatch\n%s", joinLogs(log))
				}
			case 5, 6, 7, 8:
				got := elevator.Tick()
				want := reference.tick()
				line := fmt.Sprintf("case=%d op=%d input=Tick output=TickResult(before=%d,exited=%v,boarded=%v,direction=%s,current=%d) reference=TickResult(before=%d,exited=%v,boarded=%v,direction=%s,current=%d) basis=exit-capacity-board-direction-move", caseNumber, operation, got.BeforeFloor, got.Exited, got.Boarded, got.Direction, got.CurrentFloor, want.BeforeFloor, want.Exited, want.Boarded, want.Direction, want.CurrentFloor)
				log = append(log, line)
				if !tickResultsEqual(got, want) {
					t.Fatalf("tick mismatch\n%s", joinLogs(log))
				}
			default:
				id := rng.Intn(20) + 1
				got, gotQueryErr := elevator.Passenger(id)
				want, wantQueryErr := reference.passenger(id)
				line := fmt.Sprintf("case=%d op=%d input=Passenger(id=%d) output=(%+v,%q) reference=(%+v,%q) basis=status-and-step-numbers", caseNumber, operation, id, got, errorStrings(gotQueryErr), want, errorStrings(wantQueryErr))
				log = append(log, line)
				if got != want || errorStrings(gotQueryErr) != errorStrings(wantQueryErr) {
					t.Fatalf("passenger mismatch\n%s", joinLogs(log))
				}
			}
		}

		flush := 0
		for len(reference.waiting)+len(reference.car) > 0 {
			flush++
			if flush > 1000 {
				t.Fatalf("case %d did not finish\n%s", caseNumber, joinLogs(log))
			}
			got := elevator.Tick()
			want := reference.tick()
			line := fmt.Sprintf("case=%d flush=%d input=Tick output=TickResult(before=%d,exited=%v,boarded=%v,direction=%s,current=%d) reference=TickResult(before=%d,exited=%v,boarded=%v,direction=%s,current=%d) basis=deliver-all-accepted-passengers", caseNumber, flush, got.BeforeFloor, got.Exited, got.Boarded, got.Direction, got.CurrentFloor, want.BeforeFloor, want.Exited, want.Boarded, want.Direction, want.CurrentFloor)
			log = append(log, line)
			if !tickResultsEqual(got, want) {
				t.Fatalf("flush tick mismatch\n%s", joinLogs(log))
			}
		}

		idleGot := elevator.Tick()
		idleWant := reference.tick()
		line := fmt.Sprintf("case=%d input=FinalIdleTick output=%+v reference=%+v basis=empty-target-set-keeps-floor-and-Idle", caseNumber, idleGot, idleWant)
		log = append(log, line)
		if !tickResultsEqual(idleGot, idleWant) {
			t.Fatalf("final idle tick mismatch\n%s", joinLogs(log))
		}

		delivered := 0
		for id := range reference.passengers {
			got, err := elevator.Passenger(id)
			if err != nil {
				t.Fatalf("accepted passenger %d missing: %v\n%s", id, err, joinLogs(log))
			}
			want, _ := reference.passenger(id)
			line := fmt.Sprintf("case=%d input=Passenger(id=%d) output=%+v reference=%+v basis=one-boarding-one-delivery-and-ordered-step-numbers", caseNumber, id, got, want)
			log = append(log, line)
			if got != want || got.Status != Delivered || got.DeliverTick <= got.BoardTick {
				t.Fatalf("final passenger %d mismatch\n%s", id, joinLogs(log))
			}
			delivered++
		}
		if len(reference.passengers) != delivered {
			t.Fatalf("accepted=%d delivered=%d", len(reference.passengers), delivered)
		}
		t.Logf("case=%d input=floors-%d capacity-%d operations-%d output=accepted-%d final-idle-floor-%d basis=full-log-kept-for-failure-replay\n%s", caseNumber, floors, capacity, operations, delivered, idleGot.CurrentFloor, joinLogs(log))
	}
}

func TestConcurrentCallsTicksAndQueriesAreSerializable(t *testing.T) {
	elevator, err := NewElevator(7, 3)
	if err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	var acceptedMu sync.Mutex
	acceptedIDs := make(map[int]bool)
	sharedAccepts := 0

	for worker := 0; worker < 3; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			rng := rand.New(rand.NewSource(int64(worker + 10)))
			for i := 0; i < 40; i++ {
				id := 1000 + worker*100 + i
				from := rng.Intn(7) + 1
				to := rng.Intn(7) + 1
				if err := elevator.Call(id, from, to); err == nil {
					acceptedMu.Lock()
					acceptedIDs[id] = true
					acceptedMu.Unlock()
				}
			}
		}(worker)
	}

	for worker := 0; worker < 4; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for i := 0; i < 80; i++ {
				if worker%2 == 0 {
					elevator.Tick()
				} else {
					_, _ = elevator.Passenger(1000 + (i % 250))
				}
			}
		}(worker)
	}

	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := elevator.Call(1, 1, 2); err == nil {
				acceptedMu.Lock()
				sharedAccepts++
				acceptedIDs[1] = true
				acceptedMu.Unlock()
			}
		}()
	}

	group.Wait()
	if sharedAccepts != 1 {
		t.Fatalf("duplicate concurrent id accepted %d times, want 1", sharedAccepts)
	}

	for i := 0; i < 200; i++ {
		elevator.Tick()
	}
	for id := range acceptedIDs {
		info, err := elevator.Passenger(id)
		if err != nil {
			t.Fatalf("accepted id %d: %v", id, err)
		}
		if info.Status != Delivered || info.DeliverTick <= info.BoardTick {
			t.Fatalf("id %d final info = %+v", id, info)
		}
	}
}

var _ = fmt.Sprintf
var _ = rand.Int
var _ sync.WaitGroup
var _ *testing.T
