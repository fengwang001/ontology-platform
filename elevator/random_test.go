package elevator

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"
)

type naivePassenger struct {
	from        int
	to          int
	status      PassengerStatus
	boardedTick int
	exitedTick  int
}

type naiveSimulator struct {
	floors     int
	capacity   int
	floor      int
	direction  Direction
	step       int
	waiting    []int
	car        []int
	passengers map[int]naivePassenger
}

func newNaive(floors, capacity int) *naiveSimulator {
	return &naiveSimulator{
		floors:     floors,
		capacity:   capacity,
		floor:      1,
		direction:  Idle,
		passengers: make(map[int]naivePassenger),
	}
}

func (n *naiveSimulator) call(id, from, to int) error {
	if id < 1 {
		return ErrInvalidID
	}
	if from < 1 || from > n.floors || to < 1 || to > n.floors {
		return ErrInvalidFloor
	}
	if from == to {
		return ErrSameFloor
	}
	if _, exists := n.passengers[id]; exists {
		return ErrDuplicateCall
	}
	n.passengers[id] = naivePassenger{from: from, to: to, status: Waiting}
	n.waiting = append(n.waiting, id)
	return nil
}

func (n *naiveSimulator) tick() TickResult {
	n.step++
	before := n.floor
	alighted := []int{}
	keptCar := []int{}
	for _, id := range n.car {
		passenger := n.passengers[id]
		if passenger.to == n.floor {
			passenger.status = Delivered
			passenger.exitedTick = n.step
			n.passengers[id] = passenger
			alighted = append(alighted, id)
		} else {
			keptCar = append(keptCar, id)
		}
	}
	n.car = keptCar

	boarded := []int{}
	keptWaiting := []int{}
	for _, id := range n.waiting {
		passenger := n.passengers[id]
		if passenger.from == n.floor && len(n.car) < n.capacity {
			passenger.status = InCar
			passenger.boardedTick = n.step
			n.passengers[id] = passenger
			n.car = append(n.car, id)
			boarded = append(boarded, id)
		} else {
			keptWaiting = append(keptWaiting, id)
		}
	}
	n.waiting = keptWaiting

	targets := map[int]struct{}{}
	for _, id := range n.car {
		if target := n.passengers[id].to; target != n.floor {
			targets[target] = struct{}{}
		}
	}
	for _, id := range n.waiting {
		if target := n.passengers[id].from; target != n.floor {
			targets[target] = struct{}{}
		}
	}

	if len(targets) == 0 {
		n.direction = Idle
	} else {
		n.direction = n.directionFor(targets)
		if n.direction == Up {
			n.floor++
		} else if n.direction == Down {
			n.floor--
		}
	}

	return TickResult{
		BeforeFloor: before,
		Alighted:    alighted,
		Boarded:     boarded,
		Direction:   n.direction,
		AfterFloor:  n.floor,
	}
}

func (n *naiveSimulator) directionFor(targets map[int]struct{}) Direction {
	if n.direction == Up {
		for target := range targets {
			if target > n.floor {
				return Up
			}
		}
	}
	if n.direction == Down {
		for target := range targets {
			if target < n.floor {
				return Down
			}
		}
	}

	nearest := 0
	distance := 0
	for target := range targets {
		candidate := target - n.floor
		if candidate < 0 {
			candidate = -candidate
		}
		if nearest == 0 || candidate < distance || (candidate == distance && target > n.floor) {
			nearest = target
			distance = candidate
		}
	}
	if nearest > n.floor {
		return Up
	}
	return Down
}

func (n *naiveSimulator) passenger(id int) (PassengerInfo, error) {
	passenger, exists := n.passengers[id]
	if !exists {
		return PassengerInfo{}, ErrPassengerMissing
	}
	return PassengerInfo{
		ID:          id,
		From:        passenger.from,
		To:          passenger.to,
		Status:      passenger.status,
		BoardedTick: passenger.boardedTick,
		ExitedTick:  passenger.exitedTick,
	}, nil
}

type testOp struct {
	kind string
	id   int
	from int
	to   int
}

func TestRandomSequencesMatchNaiveSimulator(t *testing.T) {
	const cases = 2000
	for seed := uint64(1); seed <= cases; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, seed+2000))
			floors := rng.IntN(10) + 2
			capacity := rng.IntN(4) + 1
			lift, err := New(floors, capacity)
			if err != nil {
				t.Fatal(err)
			}
			naive := newNaive(floors, capacity)
			operations := make([]testOp, 0, 60)
			nextID := 1

			for operation := 0; operation < 60; operation++ {
				switch rng.IntN(6) {
				case 0, 1, 2:
					var id, from, to int
					if nextID <= 12 && rng.IntN(5) == 0 {
						id = rng.IntN(nextID) + 1
					} else {
						id = nextID
						nextID++
					}
					if rng.IntN(6) == 0 {
						id = 0
					}
					from = rng.IntN(floors) + 1
					to = rng.IntN(floors) + 1
					if rng.IntN(5) == 0 {
						from = 0
					}
					if rng.IntN(7) == 0 {
						to = floors + 1
					}
					op := testOp{kind: "call", id: id, from: from, to: to}
					operations = append(operations, op)
					got := lift.Call(id, from, to)
					want := naive.call(id, from, to)
					t.Logf("input=%s output={err=%v} reason=errors must preserve first rejection rule and rejected calls mutate no state", formatCall(op), got)
					if !sameError(got, want) {
						t.Fatalf("Call error = %v, want %v", got, want)
					}
				case 3, 4:
					operations = append(operations, testOp{kind: "tick"})
					got := lift.Tick()
					want := naive.tick()
					t.Logf("input=Tick output={before=%d alighted=%v boarded=%v direction=%s after=%d} reason=unload then capacity-ordered boarding then hold-or-nearest direction then move", got.BeforeFloor, got.Alighted, got.Boarded, got.Direction, got.AfterFloor)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("Tick = %+v, want %+v", got, want)
					}
				default:
					id := rng.IntN(nextID + 3)
					op := testOp{kind: "passenger", id: id}
					operations = append(operations, op)
					got, gotErr := lift.Passenger(id)
					want, wantErr := naive.passenger(id)
					t.Logf("input=%s output={info=%+v err=%v} reason=query returns one of waiting in-car delivered and missing state", formatPassenger(op), got, gotErr)
					if !sameError(gotErr, wantErr) || !reflect.DeepEqual(got, want) {
						t.Fatalf("Passenger = %+v/%v, want %+v/%v", got, gotErr, want, wantErr)
					}
				}
				assertInvariants(t, lift, naive, seed, operation)
			}
			t.Logf("seed=%d input=%v reason=identical replayed input sequence gives same tick and passenger records", seed, operations)
		})
	}
}

func TestConcurrentCallsTicksAndQueriesAreSerializable(t *testing.T) {
	lift, err := New(6, 2)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for id := 1; id <= 40; id++ {
		from := (id % 6) + 1
		to := ((id + 2) % 6) + 1
		if from == to {
			to = from%6 + 1
		}
		group.Add(1)
		go func() {
			defer group.Done()
			_ = lift.Call(id, from, to)
		}()
	}
	for tick := 0; tick < 80; tick++ {
		group.Add(2)
		go func() {
			defer group.Done()
			lift.Tick()
		}()
		go func() {
			defer group.Done()
			_, _ = lift.Passenger((tick % 40) + 1)
		}()
	}
	group.Wait()

	lift.mu.Lock()
	defer lift.mu.Unlock()
	if len(lift.car) > lift.capacity {
		t.Fatalf("car size %d exceeds capacity %d", len(lift.car), lift.capacity)
	}
	waiting, inCar, delivered := 0, 0, 0
	for _, passenger := range lift.passengers {
		switch passenger.status {
		case Waiting:
			waiting++
		case InCar:
			inCar++
		case Delivered:
			if passenger.exitedTick <= passenger.boardedTick {
				t.Fatalf("passenger %d exit tick %d must be after board tick %d", passenger.id, passenger.exitedTick, passenger.boardedTick)
			}
			delivered++
		}
	}
	if waiting+inCar+delivered != len(lift.passengers) {
		t.Fatalf("status partition = %d+%d+%d, accepted = %d", waiting, inCar, delivered, len(lift.passengers))
	}
}

func assertInvariants(t *testing.T, lift *Elevator, naive *naiveSimulator, seed uint64, operation int) {
	t.Helper()
	lift.mu.Lock()
	defer lift.mu.Unlock()

	if len(lift.car) > lift.capacity {
		t.Fatalf("seed %d operation %d: car size %d exceeds capacity %d", seed, operation, len(lift.car), lift.capacity)
	}
	if lift.floor < 1 || lift.floor > lift.floors {
		t.Fatalf("seed %d operation %d: floor %d out of range", seed, operation, lift.floor)
	}

	waiting, inCar, delivered := 0, 0, 0
	for id, actual := range lift.passengers {
		want := naive.passengers[id]
		if actual.from != want.from || actual.to != want.to || actual.status != want.status || actual.boardedTick != want.boardedTick || actual.exitedTick != want.exitedTick {
			t.Fatalf("seed %d operation %d passenger %d = %+v, want %+v", seed, operation, id, actual, want)
		}
		switch actual.status {
		case Waiting:
			waiting++
		case InCar:
			inCar++
			if actual.boardedTick == 0 || actual.exitedTick != 0 {
				t.Fatalf("invalid in-car passenger %d: %+v", id, actual)
			}
		case Delivered:
			delivered++
			if actual.exitedTick <= actual.boardedTick || actual.boardedTick == 0 {
				t.Fatalf("invalid delivered passenger %d: %+v", id, actual)
			}
		}
	}
	if waiting != len(lift.waiting) || inCar != len(lift.car) {
		t.Fatalf("queue lengths mismatch: waiting %d/%d, car %d/%d", waiting, len(lift.waiting), inCar, len(lift.car))
	}
	if waiting+inCar+delivered != len(lift.passengers) {
		t.Fatalf("status partition = %d+%d+%d, accepted = %d", waiting, inCar, delivered, len(lift.passengers))
	}
}

func sameError(got, want error) bool {
	return (got == nil && want == nil) || (got != nil && want != nil && got.Error() == want.Error())
}

func formatCall(op testOp) string {
	return fmt.Sprintf("Call(id=%d,from=%d,to=%d)", op.id, op.from, op.to)
}

func formatPassenger(op testOp) string {
	return fmt.Sprintf("Passenger(id=%d)", op.id)
}
