package budgetguard

import (
	"sync"

	"math/bits"
)

const (
	minPeriodDays      = 1
	maxPeriodDays      = 366
	minPercent         = 1
	maxPercent         = 1000
	minLadderCount     = 1
	maxLadderCount     = 8
	minBudgetCents     = int64(1)
	maxBudgetCents     = int64(1_000_000_000_000_000)
	minSpendAmount     = int64(-1_000_000_000_000)
	maxSpendAmount     = int64(1_000_000_000_000)
	maxCumulativeSpend = int64(1_000_000_000_000_000)
)

type EventType uint8

const (
	Ladder EventType = iota + 1
	Forecast
	Froze
	Thawed
)

type Event struct {
	Type    EventType
	Percent int
}

type RejectReason uint8

const (
	InvalidArgument RejectReason = iota + 1
	DateRegression
	Frozen
	AmountOutOfRange
)

type RejectError struct {
	Reason RejectReason
}

func (e RejectError) Error() string {
	switch e.Reason {
	case InvalidArgument:
		return "invalid argument"
	case DateRegression:
		return "date regression"
	case Frozen:
		return "budget is frozen"
	case AmountOutOfRange:
		return "resulting spend is out of range"
	default:
		return "budget operation rejected"
	}
}

type Snapshot struct {
	Spent       int64
	CurrentDay  int
	Budget      int64
	LadderFired []bool
	Forecast    bool
	Frozen      bool
}

type Guard struct {
	mu              sync.RWMutex
	periodDays      int
	ladderPercents  []int
	forecastPercent int64
	minElapsedDays  int
	budget          int64
	spent           int64
	currentDay      int
	ladderFired     []bool
	forecastFired   bool
}

func New(periodDays int, ladderPercentages []int, forecastPercent int, minElapsedDays int, initialBudget int64) (*Guard, error) {
	if periodDays < minPeriodDays || periodDays > maxPeriodDays ||
		len(ladderPercentages) < minLadderCount || len(ladderPercentages) > maxLadderCount ||
		forecastPercent < minPercent || forecastPercent > maxPercent ||
		minElapsedDays < 1 || minElapsedDays > periodDays ||
		initialBudget < minBudgetCents || initialBudget > maxBudgetCents {
		return nil, RejectError{Reason: InvalidArgument}
	}

	previous := 0
	for _, percent := range ladderPercentages {
		if percent <= previous || percent < minPercent || percent > maxPercent {
			return nil, RejectError{Reason: InvalidArgument}
		}
		previous = percent
	}

	percentages := append([]int(nil), ladderPercentages...)
	return &Guard{
		periodDays:      periodDays,
		ladderPercents:  percentages,
		forecastPercent: int64(forecastPercent),
		minElapsedDays:  minElapsedDays,
		budget:          initialBudget,
		ladderFired:     make([]bool, len(percentages)),
	}, nil
}

func (g *Guard) Spend(day int, amountCents int64) ([]Event, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if day < 0 || day >= g.periodDays || amountCents < minSpendAmount || amountCents > maxSpendAmount {
		return nil, RejectError{Reason: InvalidArgument}
	}
	if day < g.currentDay {
		return nil, RejectError{Reason: DateRegression}
	}
	if amountCents > 0 && g.frozenLocked() {
		return nil, RejectError{Reason: Frozen}
	}

	nextSpent := g.spent + amountCents
	if nextSpent < 0 || nextSpent > maxCumulativeSpend {
		return nil, RejectError{Reason: AmountOutOfRange}
	}

	wasFrozen := g.frozenLocked()
	g.currentDay = day
	g.spent = nextSpent
	return g.evaluateLocked(wasFrozen), nil
}

func (g *Guard) AdjustBudget(budgetCents int64) ([]Event, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if budgetCents < minBudgetCents || budgetCents > maxBudgetCents {
		return nil, RejectError{Reason: InvalidArgument}
	}

	wasFrozen := g.frozenLocked()
	for index, percent := range g.ladderPercents {
		if g.ladderFired[index] && !productGE(g.spent, 100, budgetCents, int64(percent)) {
			g.ladderFired[index] = false
		}
	}
	g.budget = budgetCents
	return g.evaluateLocked(wasFrozen), nil
}

func (g *Guard) Frozen() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.frozenLocked()
}

func (g *Guard) Snapshot() Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()

	return Snapshot{
		Spent:       g.spent,
		CurrentDay:  g.currentDay,
		Budget:      g.budget,
		LadderFired: append([]bool(nil), g.ladderFired...),
		Forecast:    g.forecastFired,
		Frozen:      g.frozenLocked(),
	}
}

func (g *Guard) frozenLocked() bool {
	return g.spent >= g.budget
}

func (g *Guard) evaluateLocked(wasFrozen bool) []Event {
	events := make([]Event, 0)

	for index, percent := range g.ladderPercents {
		if !g.ladderFired[index] && productGE(g.spent, 100, g.budget, int64(percent)) {
			g.ladderFired[index] = true
			events = append(events, Event{Type: Ladder, Percent: percent})
		}
	}

	elapsedDays := int64(g.currentDay + 1)
	forecastCondition := false
	if int(elapsedDays) >= g.minElapsedDays {
		projection := g.spent * int64(g.periodDays) / elapsedDays
		forecastCondition = productGE(projection, 100, g.budget, g.forecastPercent)
	}

	if forecastCondition {
		if !g.forecastFired {
			g.forecastFired = true
			events = append(events, Event{Type: Forecast})
		}
	} else {
		g.forecastFired = false
	}

	isFrozen := g.frozenLocked()
	if !wasFrozen && isFrozen {
		events = append(events, Event{Type: Froze})
	} else if wasFrozen && !isFrozen {
		events = append(events, Event{Type: Thawed})
	}

	return events
}

func productGE(leftA int64, leftB int64, rightA int64, rightB int64) bool {
	leftHigh, leftLow := bits.Mul64(uint64(leftA), uint64(leftB))
	rightHigh, rightLow := bits.Mul64(uint64(rightA), uint64(rightB))
	return leftHigh > rightHigh || (leftHigh == rightHigh && leftLow >= rightLow)
}
