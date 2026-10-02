package budgetguard

import (
	"sync"
	"testing"
)

func TestForecastComparisonUses128Bits(t *testing.T) {
	guard, err := New(366, []int{1000}, 1000, 1, 1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := guard.Spend(0, maxSpendAmount)
	if err != nil {
		t.Fatalf("Spend(0, max) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"LADDER(1000)", "FORECAST", "FROZE"})

	projection := maxSpendAmount * 366
	if !productGE(projection, 100, 1, 1000) {
		t.Fatal("projection*100 >= budget*1000 was false; 128-bit comparison failed")
	}
}

func TestConcurrentOperationsAreSerialized(t *testing.T) {
	guard, err := New(366, []int{1, 1000}, 1000, 1, maxBudgetCents)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	const goroutines = 16
	const operations = 40
	var waitGroup sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for index := 0; index < operations; index++ {
				day := (worker + index) % 366
				switch index % 4 {
				case 0:
					_, _ = guard.Spend(day, 0)
				case 1:
					_, _ = guard.AdjustBudget(maxBudgetCents)
				case 2:
					_ = guard.Frozen()
				default:
					_ = guard.Snapshot()
				}
			}
		}(worker)
	}
	waitGroup.Wait()

	snapshot := guard.Snapshot()
	if snapshot.Spent != 0 || snapshot.CurrentDay < 0 || snapshot.CurrentDay >= 366 || snapshot.Frozen {
		t.Fatalf("unexpected snapshot after concurrent operations: %+v", snapshot)
	}
}
