package compens

import (
	"reflect"
	"testing"

	"ontology/step"
)

func mk(n int) []step.Step {
	s := make([]step.Step, n)
	for i := range s {
		s[i] = step.Step{ID: "s", Forward: func() (step.Outcome, error) { return step.Success, nil }}
	}
	return s
}

func TestBuild(t *testing.T) {
	cases := []struct {
		name       string
		n          int
		succeeded  []bool
		comped     []bool
		wantOrder  []int
		wantSkip   []int
	}{
		{
			name:      "reverse over 1..k only, failed step excluded",
			n:         5,
			succeeded: []bool{true, true, true, false, false},
			comped:    nil,
			wantOrder: []int{2, 1, 0},
		},
		{
			name:       "already compensated are skipped, order remains strict descending",
			n:          4,
			succeeded:  []bool{true, true, true, true},
			comped:     []bool{false, true, false, true},
			wantOrder:  []int{2, 0},
			wantSkip:   []int{3, 1},
		},
		{
			name:       "nothing succeeded",
			n:          3,
			succeeded:  []bool{false, false, false},
			wantOrder:  nil,
		},
		{
			name:       "resume after comp failed at index 2 redoes only 2..0",
			n:          4,
			succeeded:  []bool{true, true, true, false},
			comped:     []bool{false, false, true, false},
			wantOrder:  []int{1, 0},
			wantSkip:   []int{2},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Build(mk(tc.n), tc.succeeded, tc.comped)
			if !reflect.DeepEqual(got.Order, tc.wantOrder) {
				t.Fatalf("Order = %v, want %v", got.Order, tc.wantOrder)
			}
			if !reflect.DeepEqual(got.Skip, tc.wantSkip) {
				t.Fatalf("Skip = %v, want %v", got.Skip, tc.wantSkip)
			}
			for i := 1; i < len(got.Order); i++ {
				if got.Order[i-1] <= got.Order[i] {
					t.Fatalf("order not strictly descending: %v", got.Order)
				}
			}
		})
	}
}
