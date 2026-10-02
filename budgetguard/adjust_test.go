package budgetguard

import "testing"

func TestAdjustBudgetRearmsOnlyLaddersThatNoLongerHold(t *testing.T) {
	guard, err := New(10, []int{50, 75, 100}, 1000, 10, 100)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := guard.Spend(0, 100)
	if err != nil {
		t.Fatalf("Spend(0, 100) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"LADDER(50)", "LADDER(75)", "LADDER(100)", "FROZE"})

	events, err = guard.AdjustBudget(120)
	if err != nil {
		t.Fatalf("AdjustBudget(120) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"THAWED"})

	fired := guard.Snapshot().LadderFired
	if len(fired) != 3 || !fired[0] || !fired[1] || fired[2] {
		t.Fatalf("ladder flags = %v, want [true true false]", fired)
	}

	events, err = guard.AdjustBudget(100)
	if err != nil {
		t.Fatalf("AdjustBudget(100) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"LADDER(100)", "FROZE"})
}

func TestLowerBudgetEmitsLadderForecastBeforeFroze(t *testing.T) {
	guard, err := New(10, []int{50, 100}, 50, 1, 1000)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := guard.Spend(0, 10)
	if err != nil {
		t.Fatalf("Spend(0, 10) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)

	events, err = guard.AdjustBudget(10)
	if err != nil {
		t.Fatalf("AdjustBudget(10) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"LADDER(50)", "LADDER(100)", "FORECAST", "FROZE"})
}

func TestFrozenStateUnchangedEmitsNoFreezeEvent(t *testing.T) {
	guard, err := New(10, []int{100}, 1000, 10, 100)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := guard.Spend(0, 100); err != nil {
		t.Fatalf("Spend(0, 100) error = %v", err)
	}

	events, err := guard.AdjustBudget(90)
	if err != nil {
		t.Fatalf("AdjustBudget(90) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)
}

func TestAdjustBudgetRejectsOutOfRangeWithoutStateChange(t *testing.T) {
	guard, err := New(10, []int{50}, 100, 1, 100)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := guard.Spend(0, 50); err != nil {
		t.Fatalf("Spend(0, 50) error = %v", err)
	}

	before := guard.Snapshot()
	for _, budget := range []int64{0, maxBudgetCents + 1} {
		if _, err := guard.AdjustBudget(budget); err == nil || err.(RejectError).Reason != InvalidArgument {
			t.Fatalf("AdjustBudget(%d) error = %v, want InvalidArgument", budget, err)
		}
	}
	after := guard.Snapshot()
	if !snapshotsEqual(before, after) {
		t.Fatalf("snapshot changed after rejection: before=%+v after=%+v", before, after)
	}
}

func snapshotsEqual(left, right Snapshot) bool {
	if left.Spent != right.Spent || left.CurrentDay != right.CurrentDay || left.Budget != right.Budget ||
		left.Forecast != right.Forecast || left.Frozen != right.Frozen ||
		len(left.LadderFired) != len(right.LadderFired) {
		return false
	}
	for index := range left.LadderFired {
		if left.LadderFired[index] != right.LadderFired[index] {
			return false
		}
	}
	return true
}
