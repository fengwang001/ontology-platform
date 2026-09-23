package budget

import (
	"errors"
	"testing"
)

func TestBudget(t *testing.T) {
	tests := []struct {
		name    string
		budget  *Budget
		adds    []int
		steps   int
		wantErr error
	}{
		{name: "nil ignored", budget: nil, adds: []int{1, 2}, steps: 0},
		{name: "within limit", budget: New(5), adds: []int{2, 3}, steps: 5},
		{name: "over limit", budget: New(2), adds: []int{3}, steps: 3, wantErr: ErrLimitExceeded},
		{name: "unlimited", budget: Unlimited(), adds: []int{1000000}, steps: 1000000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			for _, amount := range tt.adds {
				if addErr := tt.budget.Add(amount); addErr != nil {
					err = addErr
				}
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if got := tt.budget.Steps(); got != tt.steps {
				t.Fatalf("Steps() = %d, want %d", got, tt.steps)
			}
		})
	}
}

func TestBudgetDeterministic(t *testing.T) {
	first := New(100)
	second := New(100)
	for i := 0; i < 10; i++ {
		if err := first.Add(1); err != nil {
			t.Fatal(err)
		}
		if err := second.Add(1); err != nil {
			t.Fatal(err)
		}
	}
	if first.Steps() != second.Steps() {
		t.Fatal("identical budgets counted differently")
	}
}
