package paymentcard

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type randomOperation struct {
	kind         string
	card         int
	t            int64
	x            int64
	y            int64
	from         int64
	to           int64
	transactions []Transaction
}

type naiveAnchor struct {
	t int64
	x int64
	y int64
}

type naiveTravel struct {
	from int64
	to   int64
}

type naiveState struct {
	hasAnchor  bool
	anchor     naiveAnchor
	rejections []int64
	frozen     bool
	hasTravel  bool
	travel     naiveTravel
}

func TestRandomNaiveComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))

	for iteration := 0; iteration < 2000; iteration++ {
		speed := int64(rng.Intn(1000) + 1)
		limit := int64(rng.Intn(5) + 1)
		window := int64(rng.Intn(100) + 1)
		detector := newTestDetector(t, speed, limit, window)
		naive := make(map[int]*naiveState)

		t.Logf("iteration=%d input=(V=%d,K=%d,H=%d) basis=(%s)", iteration, speed, limit, window, "random seed 20261002")

		for step := 0; step < 40; step++ {
			operation := randomOperationGenerator(rng)
			card := []byte{byte(operation.card), 'c'}

			switch operation.kind {
			case "check":
				got := detector.Check(card, operation.t, operation.x, operation.y)
				want := naiveCheck(naive, operation.card, speed, limit, window, operation.t, operation.x, operation.y)
				t.Logf("iteration=%d step=%d input=Check(card=%d,t=%d,x=%d,y=%d) output=%s basis=%s",
					iteration, step, operation.card, operation.t, operation.x, operation.y, got, naiveBasis(want))
				if got != want {
					t.Fatalf("iteration=%d step=%d Check got %s, want %s", iteration, step, got, want)
				}
			case "batch":
				got := detector.CheckBatch(card, operation.transactions)
				want := naiveBatch(naive, operation.card, speed, limit, window, operation.transactions)
				t.Logf("iteration=%d step=%d input=CheckBatch(card=%d,transactions=%v) output=%v basis=%s",
					iteration, step, operation.card, operation.transactions, got, "stable t-ascending order")
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("iteration=%d step=%d batch got %v, want %v", iteration, step, got, want)
				}
			case "declare":
				got := detector.Declare(card, operation.from, operation.to)
				want := naiveDeclare(naive, operation.card, operation.from, operation.to)
				t.Logf("iteration=%d step=%d input=Declare(card=%d,from=%d,to=%d) output=%s basis=%s",
					iteration, step, operation.card, operation.from, operation.to, got, naiveBasis(want))
				if got != want {
					t.Fatalf("iteration=%d step=%d Declare got %s, want %s", iteration, step, got, want)
				}
			case "unfreeze":
				got := detector.Unfreeze(card)
				want := naiveUnfreeze(naive, operation.card)
				t.Logf("iteration=%d step=%d input=Unfreeze(card=%d) output=%s basis=%s",
					iteration, step, operation.card, got, naiveBasis(want))
				if got != want {
					t.Fatalf("iteration=%d step=%d Unfreeze got %s, want %s", iteration, step, got, want)
				}
			}

			if !naiveStatesMatch(t, detector, naive) {
				t.Fatalf("iteration=%d step=%d state mismatch after %s", iteration, step, operation.kind)
			}
		}
	}
}

func randomOperationGenerator(rng *rand.Rand) randomOperation {
	operation := randomOperation{card: rng.Intn(4)}

	switch rng.Intn(10) {
	case 0, 1, 2, 3:
		operation.kind = "check"
		operation.t = int64(rng.Intn(400))
		operation.x = int64(rng.Intn(21)) - 10
		operation.y = int64(rng.Intn(21)) - 10
		if rng.Intn(10) == 0 {
			operation.t = int64(rng.Intn(10)) - 5
		}
	case 4, 5, 6:
		operation.kind = "batch"
		size := rng.Intn(5) + 1
		operation.transactions = make([]Transaction, size)
		for i := range operation.transactions {
			operation.transactions[i] = Transaction{
				T: int64(rng.Intn(400)),
				X: int64(rng.Intn(21)) - 10,
				Y: int64(rng.Intn(21)) - 10,
			}
		}
	case 7, 8:
		operation.kind = "declare"
		operation.from = int64(rng.Intn(400))
		operation.to = operation.from + int64(rng.Intn(50)) + 1
		if rng.Intn(10) == 0 {
			operation.from = -1
		}
	default:
		operation.kind = "unfreeze"
	}

	return operation
}

func naiveCheck(states map[int]*naiveState, card int, speed, limit, window, t, x, y int64) Outcome {
	if t < 0 || t > 1_000_000_000_000 || x < -1_000_000_000 || x > 1_000_000_000 || y < -1_000_000_000 || y > 1_000_000_000 {
		return OutcomeInvalidParameters
	}

	state := naiveCard(states, card)
	if state.frozen {
		return OutcomeFrozen
	}
	if state.hasAnchor && state.anchor.t == t && state.anchor.x == x && state.anchor.y == y {
		return OutcomeDuplicate
	}
	if state.hasAnchor && t < state.anchor.t {
		return OutcomeOutOfOrder
	}
	if !state.hasAnchor {
		state.hasAnchor = true
		state.anchor = naiveAnchor{t: t, x: x, y: y}
		return OutcomeAccepted
	}
	if state.hasTravel && state.travel.from <= t && t < state.travel.to {
		state.anchor = naiveAnchor{t: t, x: x, y: y}
		return OutcomeAccepted
	}

	distance := naiveAbs(x-state.anchor.x) + naiveAbs(y-state.anchor.y)
	elapsed := t - state.anchor.t
	if distance*3600 <= speed*elapsed {
		state.anchor = naiveAnchor{t: t, x: x, y: y}
		return OutcomeAccepted
	}

	count := int64(1)
	cutoff := t - window
	for _, rejection := range state.rejections {
		if rejection > cutoff {
			count++
		}
	}
	state.rejections = append(state.rejections, t)
	if count >= limit {
		state.frozen = true
	}
	return OutcomeImpossible
}

func naiveBatch(states map[int]*naiveState, card int, speed, limit, window int64, transactions []Transaction) []Outcome {
	if len(transactions) < 1 || len(transactions) > 1000 {
		return invalidBatch(len(transactions))
	}
	for _, transaction := range transactions {
		if transaction.T < 0 || transaction.T > 1_000_000_000_000 || transaction.X < -1_000_000_000 || transaction.X > 1_000_000_000 || transaction.Y < -1_000_000_000 || transaction.Y > 1_000_000_000 {
			return invalidBatch(len(transactions))
		}
	}

	order := make([]int, len(transactions))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return transactions[order[i]].T < transactions[order[j]].T
	})

	results := make([]Outcome, len(transactions))
	for _, index := range order {
		transaction := transactions[index]
		results[index] = naiveCheck(states, card, speed, limit, window, transaction.T, transaction.X, transaction.Y)
	}
	return results
}

func naiveDeclare(states map[int]*naiveState, card int, from, to int64) Outcome {
	if from < 0 || from >= to || to > 10_000_000_000_000 {
		return OutcomeInvalidParameters
	}
	state := naiveCard(states, card)
	state.hasTravel = true
	state.travel = naiveTravel{from: from, to: to}
	return OutcomeOK
}

func naiveUnfreeze(states map[int]*naiveState, card int) Outcome {
	state := states[card]
	if state == nil {
		return OutcomeCardNotFound
	}
	if !state.frozen {
		return OutcomeNotFrozen
	}
	state.frozen = false
	state.rejections = nil
	return OutcomeOK
}

func naiveCard(states map[int]*naiveState, card int) *naiveState {
	state := states[card]
	if state == nil {
		state = &naiveState{}
		states[card] = state
	}
	return state
}

func naiveStatesMatch(t *testing.T, detector *Detector, states map[int]*naiveState) bool {
	t.Helper()

	for card, expected := range states {
		snapshot, exists := detector.Snapshot([]byte{byte(card), 'c'})
		if !exists {
			return false
		}
		actual := CardSnapshot{
			HasAnchor:       snapshot.HasAnchor,
			T:               snapshot.T,
			X:               snapshot.X,
			Y:               snapshot.Y,
			Rejections:      normalizeRejections(snapshot.Rejections),
			Frozen:          snapshot.Frozen,
			HasTravelWindow: snapshot.HasTravelWindow,
			TravelFrom:      snapshot.TravelFrom,
			TravelTo:        snapshot.TravelTo,
		}
		want := CardSnapshot{
			HasAnchor:       expected.hasAnchor,
			T:               expected.anchor.t,
			X:               expected.anchor.x,
			Y:               expected.anchor.y,
			Rejections:      normalizeRejections(expected.rejections),
			Frozen:          expected.frozen,
			HasTravelWindow: expected.hasTravel,
			TravelFrom:      expected.travel.from,
			TravelTo:        expected.travel.to,
		}
		if !reflect.DeepEqual(actual, want) {
			t.Errorf("card=%d actual=%+v want=%+v", card, actual, want)
			return false
		}
	}
	return true
}

func normalizeRejections(values []int64) []int64 {
	if len(values) == 0 {
		return []int64{}
	}
	return append([]int64(nil), values...)
}

func naiveAbs(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func naiveBasis(outcome Outcome) string {
	switch outcome {
	case OutcomeAccepted:
		return "accepted after first-anchor, travel exemption, or d*3600 <= V*dt"
	case OutcomeInvalidParameters:
		return "parameter validation failed before state access"
	case OutcomeFrozen:
		return "frozen flag has priority"
	case OutcomeDuplicate:
		return "t,x,y all equal anchor"
	case OutcomeOutOfOrder:
		return "t < anchor t"
	case OutcomeImpossible:
		return "d*3600 > V*dt; rejection window recounted before possible freeze"
	case OutcomeCardNotFound:
		return "card has never appeared or declared"
	case OutcomeNotFrozen:
		return "card exists but frozen flag is false"
	case OutcomeOK:
		return "state-changing operation succeeded"
	default:
		return fmt.Sprintf("unknown outcome %s", outcome)
	}
}
