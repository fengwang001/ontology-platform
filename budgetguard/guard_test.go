package budgetguard

import (
	"fmt"
	"testing"
)

func eventNames(events []Event) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		switch event.Type {
		case Ladder:
			names = append(names, fmt.Sprintf("LADDER(%d)", event.Percent))
		case Forecast:
			names = append(names, "FORECAST")
		case Froze:
			names = append(names, "FROZE")
		case Thawed:
			names = append(names, "THAWED")
		}
	}
	return names
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
}

func TestSpecificationReplay(t *testing.T) {
	guard, err := New(30, []int{50, 80, 100}, 100, 3, 10_000)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	type operation struct {
		name   string
		day    int
		amount int64
		budget int64
		events []string
		err    RejectReason
	}

	operations := []operation{
		{name: "spend day 0", day: 0, amount: 3_000},
		{name: "cross 50%", day: 1, amount: 2_500, events: []string{"LADDER(50)"}},
		{name: "forecast at minimum elapsed day", day: 2, amount: 500, events: []string{"FORECAST"}},
		{name: "cross 80%, 100%, and freeze", day: 20, amount: 4_000, events: []string{"LADDER(80)", "LADDER(100)", "FROZE"}},
		{name: "positive spend rejected while frozen", day: 20, amount: 1, err: Frozen},
		{name: "refund thaws without rearming", day: 21, amount: -2_500, events: []string{"THAWED"}},
		{name: "raise budget and rearm", budget: 20_000},
		{name: "cross only 50% again", day: 22, amount: 2_600, events: []string{"LADDER(50)"}},
	}

	for _, operation := range operations {
		var events []Event
		var callErr error
		if operation.budget != 0 {
			events, callErr = guard.AdjustBudget(operation.budget)
		} else {
			events, callErr = guard.Spend(operation.day, operation.amount)
		}

		snapshot := guard.Snapshot()
		t.Logf("%s: input day=%d amount=%d budget=%d; output events=%v err=%v; state S=%d cur=%d B=%d fired=%v f=%t frozen=%t",
			operation.name, operation.day, operation.amount, operation.budget, eventNames(events), callErr,
			snapshot.Spent, snapshot.CurrentDay, snapshot.Budget, snapshot.LadderFired, snapshot.Forecast, snapshot.Frozen)

		if operation.err != 0 {
			if callErr == nil {
				t.Fatalf("%s error = nil, want %v", operation.name, operation.err)
			}
			reject, ok := callErr.(RejectError)
			if !ok || reject.Reason != operation.err {
				t.Fatalf("%s error = %v, want reason %v", operation.name, callErr, operation.err)
			}
			continue
		}
		if callErr != nil {
			t.Fatalf("%s error = %v", operation.name, callErr)
		}
		assertStrings(t, eventNames(events), operation.events)
	}

	snapshot := guard.Snapshot()
	if snapshot.Spent != 10_100 || snapshot.CurrentDay != 22 || snapshot.Budget != 20_000 {
		t.Fatalf("final snapshot = %+v", snapshot)
	}
	if got := snapshot.LadderFired; len(got) != 3 || !got[0] || got[1] || got[2] {
		t.Fatalf("ladder flags = %v, want [true false false]", got)
	}
	if snapshot.Forecast || snapshot.Frozen {
		t.Fatalf("forecast=%t frozen=%t, want both false", snapshot.Forecast, snapshot.Frozen)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	valid := []int{50, 80}
	cases := []struct {
		name       string
		days       int
		percent    []int
		forecast   int
		minElapsed int
		budget     int64
	}{
		{"period too small", 0, valid, 100, 1, 100},
		{"period too large", 367, valid, 100, 1, 100},
		{"no ladders", 30, nil, 100, 1, 100},
		{"too many ladders", 30, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, 100, 1, 100},
		{"ladder not increasing", 30, []int{80, 80}, 100, 1, 100},
		{"ladder too large", 30, []int{1001}, 100, 1, 100},
		{"forecast too small", 30, valid, 0, 1, 100},
		{"minimum elapsed beyond period", 30, valid, 100, 31, 100},
		{"budget zero", 30, valid, 100, 1, 0},
		{"budget too large", 30, valid, 100, 1, maxBudgetCents + 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.days, tc.percent, tc.forecast, tc.minElapsed, tc.budget)
			if err == nil || err.(RejectError).Reason != InvalidArgument {
				t.Fatalf("New() error = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestLadderEqualityAndMultipleCrossings(t *testing.T) {
	guard, err := New(10, []int{50, 80}, 1000, 10, 100)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := guard.Spend(4, 49)
	if err != nil {
		t.Fatalf("Spend(4, 49) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)

	events, err = guard.Spend(5, 1)
	if err != nil {
		t.Fatalf("Spend(5, 1) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"LADDER(50)"})

	events, err = guard.Spend(6, 30)
	if err != nil {
		t.Fatalf("Spend(6, 30) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"LADDER(80)"})

	events, err = guard.Spend(7, -31)
	if err != nil {
		t.Fatalf("Spend(7, -31) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)

	events, err = guard.Spend(8, 31)
	if err != nil {
		t.Fatalf("Spend(8, 31) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)
}

func TestFrozenAcceptsZeroAndNegativeSpend(t *testing.T) {
	guard, err := New(10, []int{100}, 1000, 10, 100)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := guard.Spend(0, 99)
	if err != nil {
		t.Fatalf("Spend(0, 99) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)

	events, err = guard.Spend(1, 1)
	if err != nil {
		t.Fatalf("Spend(1, 1) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"LADDER(100)", "FROZE"})

	if _, err := guard.Spend(1, 1); err == nil || err.(RejectError).Reason != Frozen {
		t.Fatalf("positive frozen spend error = %v, want Frozen", err)
	}

	events, err = guard.Spend(1, 0)
	if err != nil {
		t.Fatalf("zero spend while frozen error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)

	events, err = guard.Spend(2, -1)
	if err != nil {
		t.Fatalf("negative spend while frozen error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"THAWED"})

	events, err = guard.Spend(3, 1)
	if err != nil {
		t.Fatalf("recross after refund error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"FROZE"})
}
