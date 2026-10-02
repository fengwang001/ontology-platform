package budgetguard

import "testing"

func TestSpendRejectionPriorityAndStateUnchanged(t *testing.T) {
	guard, err := New(10, []int{100}, 1000, 10, 100)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := guard.Spend(2, 100); err != nil {
		t.Fatalf("Spend(2, 100) error = %v", err)
	}

	cases := []struct {
		name   string
		day    int
		amount int64
		reason RejectReason
	}{
		{"invalid day before regression", -1, 1, InvalidArgument},
		{"invalid amount before regression", 1, minSpendAmount - 1, InvalidArgument},
		{"date regression", 1, 1, DateRegression},
		{"frozen positive spend", 2, 1, Frozen},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := guard.Snapshot()
			events, err := guard.Spend(tc.day, tc.amount)
			after := guard.Snapshot()

			t.Logf("rejection input day=%d amount=%d; output events=%v err=%v; before S=%d cur=%d B=%d frozen=%t, after S=%d cur=%d B=%d frozen=%t; reason=%v",
				tc.day, tc.amount, eventNames(events), err,
				before.Spent, before.CurrentDay, before.Budget, before.Frozen,
				after.Spent, after.CurrentDay, after.Budget, after.Frozen, tc.reason)

			if err == nil {
				t.Fatalf("Spend() error = nil, want %v", tc.reason)
			}
			reject, ok := err.(RejectError)
			if !ok || reject.Reason != tc.reason {
				t.Fatalf("Spend() error = %v, want %v", err, tc.reason)
			}
			if len(events) != 0 {
				t.Fatalf("rejected operation produced events %v", events)
			}
			if !snapshotsEqual(before, after) {
				t.Fatalf("snapshot changed: before=%+v after=%+v", before, after)
			}
		})
	}

	events, err := guard.Spend(3, -100)
	if err != nil {
		t.Fatalf("Spend(3, -100) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"THAWED"})

	before := guard.Snapshot()
	events, err = guard.Spend(4, -1)
	after := guard.Snapshot()
	t.Logf("rejection input day=4 amount=-1; output events=%v err=%v; before S=%d cur=%d B=%d, after S=%d cur=%d B=%d; reason=%v",
		eventNames(events), err, before.Spent, before.CurrentDay, before.Budget, after.Spent, after.CurrentDay, after.Budget, AmountOutOfRange)
	if err == nil || err.(RejectError).Reason != AmountOutOfRange {
		t.Fatalf("Spend() error = %v, want AmountOutOfRange", err)
	}
	if !snapshotsEqual(before, after) {
		t.Fatalf("snapshot changed: before=%+v after=%+v", before, after)
	}
}

func TestAmountAboveMaximumSpendIsRejected(t *testing.T) {
	guard, err := New(10, []int{1000}, 1000, 10, maxBudgetCents)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	before := guard.Snapshot()
	_, err = guard.Spend(0, maxSpendAmount+1)
	if err == nil || err.(RejectError).Reason != InvalidArgument {
		t.Fatalf("Spend() error = %v, want InvalidArgument", err)
	}
	if !snapshotsEqual(before, guard.Snapshot()) {
		t.Fatal("rejected spend changed state")
	}

	before = guard.Snapshot()
	_, err = guard.Spend(0, maxSpendAmount)
	if err != nil {
		t.Fatalf("valid maximum cumulative spend error = %v", err)
	}
	if guard.Snapshot().Spent != maxSpendAmount {
		t.Fatalf("spent = %d, want %d", guard.Snapshot().Spent, maxSpendAmount)
	}
}
