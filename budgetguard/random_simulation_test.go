package budgetguard

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"testing"
)

type randomOperation struct {
	kind   string
	day    int
	amount int64
	budget int64
}

type naiveGuard struct {
	days       int
	percent    []int
	forecast   int
	minElapsed int
	budget     int64
	spent      int64
	currentDay int
	fired      []bool
	forecastOn bool
}

func newNaiveGuard(days int, percent []int, forecast int, minElapsed int, budget int64) *naiveGuard {
	return &naiveGuard{
		days:       days,
		percent:    append([]int(nil), percent...),
		forecast:   forecast,
		minElapsed: minElapsed,
		budget:     budget,
		fired:      make([]bool, len(percent)),
		currentDay: 0,
	}
}

func (g *naiveGuard) frozen() bool {
	return g.spent >= g.budget
}

func bigCondition(spent int64, multiplier int64, budget int64, percent int) bool {
	left := new(big.Int).Mul(big.NewInt(spent), big.NewInt(multiplier))
	right := new(big.Int).Mul(big.NewInt(budget), big.NewInt(int64(percent)))
	return left.Cmp(right) >= 0
}

func (g *naiveGuard) evaluate() []Event {
	events := make([]Event, 0)
	for index, percent := range g.percent {
		if !g.fired[index] && bigCondition(g.spent, 100, g.budget, percent) {
			g.fired[index] = true
			events = append(events, Event{Type: Ladder, Percent: percent})
		}
	}

	elapsed := g.currentDay + 1
	condition := false
	if elapsed >= g.minElapsed {
		projection := new(big.Int).Quo(
			new(big.Int).Mul(big.NewInt(g.spent), big.NewInt(int64(g.days))),
			big.NewInt(int64(elapsed)),
		)
		threshold := new(big.Int).Mul(big.NewInt(g.budget), big.NewInt(int64(g.forecast)))
		condition = new(big.Int).Mul(projection, big.NewInt(100)).Cmp(threshold) >= 0
	}

	if condition {
		if !g.forecastOn {
			g.forecastOn = true
			events = append(events, Event{Type: Forecast})
		}
	} else {
		g.forecastOn = false
	}

	if g.spent >= g.budget {
		events = append(events, Event{Type: Froze})
	} else {
		events = append(events, Event{Type: Thawed})
	}
	return events
}

func (g *naiveGuard) spend(day int, amount int64) ([]Event, RejectReason, bool) {
	if day < 0 || day >= g.days || amount < minSpendAmount || amount > maxSpendAmount {
		return nil, InvalidArgument, false
	}
	if day < g.currentDay {
		return nil, DateRegression, false
	}
	if amount > 0 && g.frozen() {
		return nil, Frozen, false
	}
	next := g.spent + amount
	if next < 0 || next > maxCumulativeSpend {
		return nil, AmountOutOfRange, false
	}

	wasFrozen := g.frozen()
	g.currentDay = day
	g.spent = next
	return g.finishEvaluation(wasFrozen), 0, true
}

func (g *naiveGuard) adjust(budget int64) ([]Event, RejectReason, bool) {
	if budget < minBudgetCents || budget > maxBudgetCents {
		return nil, InvalidArgument, false
	}

	wasFrozen := g.frozen()
	for index, percent := range g.percent {
		if g.fired[index] && !bigCondition(g.spent, 100, budget, percent) {
			g.fired[index] = false
		}
	}
	g.budget = budget
	return g.finishEvaluation(wasFrozen), 0, true
}

func (g *naiveGuard) finishEvaluation(wasFrozen bool) []Event {
	events := g.evaluate()
	if wasFrozen == g.frozen() {
		return events[:len(events)-1]
	}
	return events
}

func (g *naiveGuard) snapshot() Snapshot {
	return Snapshot{
		Spent:       g.spent,
		CurrentDay:  g.currentDay,
		Budget:      g.budget,
		LadderFired: append([]bool(nil), g.fired...),
		Forecast:    g.forecastOn,
		Frozen:      g.frozen(),
	}
}

func randomPercentages(random *rand.Rand) []int {
	count := 1 + random.IntN(8)
	percentages := make([]int, count)
	next := 1
	for index := range percentages {
		remaining := count - index
		upper := maxPercent - remaining + 1
		next += 1 + random.IntN(upper-next)
		percentages[index] = next
	}
	return percentages
}

func randomOperations(random *rand.Rand, days int) []randomOperation {
	count := 30 + random.IntN(61)
	operations := make([]randomOperation, count)
	for index := range operations {
		if random.IntN(5) == 0 {
			budget := int64(1 + random.IntN(5_000))
			if random.IntN(10) == 0 {
				budget = 1 + random.Int64N(maxBudgetCents)
			}
			operations[index] = randomOperation{kind: "adjust", budget: budget}
			continue
		}

		day := random.IntN(days+2) - 1
		var amount int64
		switch random.IntN(8) {
		case 0:
			amount = minSpendAmount - 1
		case 1:
			amount = maxSpendAmount + 1
		case 2:
			amount = -1 - random.Int64N(2_000)
		case 3:
			amount = 0
		default:
			amount = random.Int64N(2_001) - 300
			if amount < minSpendAmount {
				amount = minSpendAmount
			}
			if amount > maxSpendAmount {
				amount = maxSpendAmount
			}
		}
		operations[index] = randomOperation{kind: "spend", day: day, amount: amount}
	}
	return operations
}

func TestRandomSequencesMatchNaiveBigIntModel(t *testing.T) {
	random := rand.New(rand.NewPCG(0x4242424242424242, 0x9182736455463728))

	for trial := 0; trial < 2_000; trial++ {
		days := 1 + random.IntN(366)
		percentages := randomPercentages(random)
		forecast := 1 + random.IntN(1000)
		minElapsed := 1 + random.IntN(days)
		budget := int64(1 + random.Int64N(5_000))

		guard, err := New(days, percentages, forecast, minElapsed, budget)
		if err != nil {
			t.Fatalf("trial %d: New() error = %v", trial, err)
		}
		reference := newNaiveGuard(days, percentages, forecast, minElapsed, budget)
		operations := randomOperations(random, days)

		for step, operation := range operations {
			var actualEvents []Event
			var actualReason RejectReason
			var referenceEvents []Event
			var referenceReason RejectReason
			var accepted bool

			if operation.kind == "adjust" {
				actualEvents, err = guard.AdjustBudget(operation.budget)
				if err != nil {
					actualReason = err.(RejectError).Reason
				}
				referenceEvents, referenceReason, accepted = reference.adjust(operation.budget)
			} else {
				actualEvents, err = guard.Spend(operation.day, operation.amount)
				if err != nil {
					actualReason = err.(RejectError).Reason
				}
				referenceEvents, referenceReason, accepted = reference.spend(operation.day, operation.amount)
			}

			actualSnapshot := guard.Snapshot()
			referenceSnapshot := reference.snapshot()
			basis := fmt.Sprintf("trial=%d step=%d %s day=%d amount=%d budget=%d accepted=%t S=%d cur=%d B=%d fired=%v f=%t frozen=%t",
				trial, step, operation.kind, operation.day, operation.amount, operation.budget, accepted,
				referenceSnapshot.Spent, referenceSnapshot.CurrentDay, referenceSnapshot.Budget,
				referenceSnapshot.LadderFired, referenceSnapshot.Forecast, referenceSnapshot.Frozen)
			t.Logf("input: %s; output events=%v reason=%v; naive events=%v reason=%v; basis: %s",
				operation.kind, eventNames(actualEvents), actualReason, eventNames(referenceEvents), referenceReason, basis)

			if actualReason != referenceReason {
				t.Fatalf("reason mismatch: actual=%v reference=%v; %s", actualReason, referenceReason, basis)
			}
			if got, want := eventNames(actualEvents), eventNames(referenceEvents); !stringsEqual(got, want) {
				t.Fatalf("event mismatch: actual=%v reference=%v; %s", got, want, basis)
			}
			if !snapshotsEqual(actualSnapshot, referenceSnapshot) {
				t.Fatalf("state mismatch: actual=%+v reference=%+v; %s", actualSnapshot, referenceSnapshot, basis)
			}
		}
	}
}

func stringsEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
