package shard

import (
	"reflect"
	"testing"
)

func TestEstimateCeiling(t *testing.T) {
	if got := Estimate([]int{10, 11, 14}); got != 12 {
		t.Fatalf("Estimate = %d, want 12", got)
	}
	if got := Estimate([]int{1, 2}); got != 2 {
		t.Fatalf("Estimate = %d, want 2", got)
	}
}

func TestBuild(t *testing.T) {
	tests := []struct {
		name       string
		cases      []Case
		shards     int
		regular    [][]string
		quarantine []string
	}{
		{
			name: "odd lower median and greedy example",
			cases: []Case{
				{Name: "a", Samples: []int{9}},
				{Name: "b", Samples: []int{7}},
				{Name: "c", Samples: []int{6}},
				{Name: "d", Samples: []int{5}},
				{Name: "e", Samples: []int{5}},
				{Name: "f"},
			},
			shards:  2,
			regular: [][]string{{"a", "f", "e"}, {"b", "c", "d"}},
		},
		{
			name: "even lower median",
			cases: []Case{
				{Name: "a", Samples: []int{5}},
				{Name: "b", Samples: []int{6}},
				{Name: "c", Samples: []int{7}},
				{Name: "d", Samples: []int{9}},
				{Name: "e"},
			},
			shards:  2,
			regular: [][]string{{"d", "e"}, {"c", "b", "a"}},
		},
		{
			name: "name tie and equal sums prefer lower shard index",
			cases: []Case{
				{Name: "b", Samples: []int{4}},
				{Name: "a", Samples: []int{4}},
				{Name: "c", Samples: []int{4}},
				{Name: "d", Samples: []int{4}},
			},
			shards:  2,
			regular: [][]string{{"a", "c"}, {"b", "d"}},
		},
		{
			name: "unknown defaults to one without known active sample",
			cases: []Case{
				{Name: "z", Quarantined: true},
				{Name: "a"},
			},
			shards:     1,
			regular:    [][]string{{"a"}},
			quarantine: []string{"z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.cases, tt.shards)
			t.Logf("input cases=%+v shards=%d", tt.cases, tt.shards)
			t.Logf("output plan=%+v", got)
			if !reflect.DeepEqual(got.Regular, tt.regular) {
				t.Fatalf("regular = %v, want %v", got.Regular, tt.regular)
			}
			if len(got.Quarantine) != len(tt.quarantine) {
				t.Fatalf("quarantine = %v, want %v", got.Quarantine, tt.quarantine)
			}
			for i := range tt.quarantine {
				if got.Quarantine[i] != tt.quarantine[i] {
					t.Fatalf("quarantine = %v, want %v", got.Quarantine, tt.quarantine)
				}
			}
		})
	}
}

func TestBuildBalanceInvariant(t *testing.T) {
	cases := []Case{
		{Name: "a", Samples: []int{10}},
		{Name: "b", Samples: []int{9}},
		{Name: "c", Samples: []int{8}},
		{Name: "d", Samples: []int{3}},
		{Name: "e", Samples: []int{2}},
	}
	plan := Build(cases, 3)
	sums := make([]int, len(plan.Regular))
	estimates := map[string]int{"a": 10, "b": 9, "c": 8, "d": 3, "e": 2}
	for index, names := range plan.Regular {
		for _, name := range names {
			sums[index] += estimates[name]
		}
	}
	min, max := sums[0], sums[0]
	for _, value := range sums[1:] {
		if value < min {
			min = value
		}
		if value > max {
			max = value
		}
	}
	if max-min > 10 {
		t.Fatalf("difference %d exceeds maximum estimate 10; sums=%v", max-min, sums)
	}
}
