package budgetguard

import "testing"

func TestForecastMinimumDayFloorEqualityAndRearm(t *testing.T) {
	guard, err := New(10, []int{1000}, 50, 3, 1000)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := guard.Spend(0, 100)
	if err != nil {
		t.Fatalf("Spend(0, 100) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)
	if guard.Snapshot().Forecast {
		t.Fatal("forecast became true one day before minimum elapsed day")
	}

	events, err = guard.Spend(1, 200)
	if err != nil {
		t.Fatalf("Spend(1, 200) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)
	if guard.Snapshot().Forecast {
		t.Fatal("forecast became true at e=2; want e>=3")
	}

	events, err = guard.Spend(2, 100)
	if err != nil {
		t.Fatalf("Spend(2, 100) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"FORECAST"})

	events, err = guard.Spend(3, -300)
	if err != nil {
		t.Fatalf("Spend(3, -300) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)
	if guard.Snapshot().Forecast {
		t.Fatal("forecast stayed true after condition became false")
	}

	events, err = guard.Spend(4, 300)
	if err != nil {
		t.Fatalf("Spend(4, 300) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"FORECAST"})

	events, err = guard.Spend(5, 0)
	if err != nil {
		t.Fatalf("Spend(5, 0) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)
}

func TestForecastFloorAndEquality(t *testing.T) {
	guard, err := New(10, []int{1000}, 100, 4, 300)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := guard.Spend(3, 119)
	if err != nil {
		t.Fatalf("Spend(3, 119) error = %v", err)
	}
	assertStrings(t, eventNames(events), nil)

	events, err = guard.Spend(3, 1)
	if err != nil {
		t.Fatalf("Spend(3, 1) error = %v", err)
	}
	assertStrings(t, eventNames(events), []string{"FORECAST"})
}
