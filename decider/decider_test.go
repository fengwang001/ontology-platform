package decider_test

import (
	"testing"

	"ontology/decider"
)

func TestDecideOrderedVetoes(t *testing.T) {
	cases := []struct {
		name string
		f    decider.Facts
		want decider.Reason
	}{
		{
			"pass primary high",
			decider.Facts{PerZone: 2, Used: 60, Total: 100, Size: 30, LowPct: 80, HighPct: 90, Primary: true},
			decider.OK,
		},
		{
			"primary exactly high passes",
			decider.Facts{PerZone: 2, Used: 60, Total: 100, Size: 30, HighPct: 90, Primary: true},
			decider.OK,
		},
		{
			"primary one over high vetoed",
			decider.Facts{PerZone: 2, Used: 61, Total: 100, Size: 30, HighPct: 90, Primary: true},
			decider.D4Disk,
		},
		{
			"replica exactly low passes",
			decider.Facts{PerZone: 2, Used: 50, Total: 100, Size: 30, LowPct: 80, HighPct: 90},
			decider.OK,
		},
		{
			"replica one over low vetoed",
			decider.Facts{PerZone: 2, Used: 51, Total: 100, Size: 30, LowPct: 80, HighPct: 90},
			decider.D4Disk,
		},
		{
			"migration primary uses low",
			decider.Facts{Moving: true, PerZone: 2, Used: 60, Total: 100, Size: 30, LowPct: 80, HighPct: 90, Primary: true},
			decider.D4Disk,
		},
		{
			"zone at capacity passes equality",
			decider.Facts{ZoneCopies: 1, PerZone: 2, Used: 0, Total: 100, Size: 1, HighPct: 90, Primary: true},
			decider.OK,
		},
		{
			"zone over capacity vetoed",
			decider.Facts{ZoneCopies: 2, PerZone: 2, Used: 0, Total: 100, Size: 1, HighPct: 90, Primary: true},
			decider.D3Awareness,
		},
		{
			"moving same zone decrements then passes",
			decider.Facts{Moving: true, ZoneCopies: 2, ZoneCopiesMinusOne: true, PerZone: 2,
				Used: 0, Total: 100, Size: 1, LowPct: 80, HighPct: 90},
			decider.OK,
		},
		{
			"moving other zone does not decrement",
			decider.Facts{Moving: true, ZoneCopies: 2, ZoneCopiesMinusOne: false, PerZone: 2,
				Used: 0, Total: 100, Size: 1, LowPct: 80, HighPct: 90},
			decider.D3Awareness,
		},
		{
			"same shard veto D2",
			decider.Facts{HasShardCopy: true, PerZone: 9, HighPct: 90, Total: 100, Size: 1, Primary: true},
			decider.D2SameShard,
		},
		{
			"excluded veto D1 first even when all fail",
			decider.Facts{Excluded: true, HasShardCopy: true, ZoneCopies: 9, PerZone: 1,
				Used: 99, Total: 100, Size: 99, LowPct: 1, HighPct: 1},
			decider.D1Excluded,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var evals uint64
			if got := decider.Decide(tc.f, &evals); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestEvalCounterStopsAtFirstVeto(t *testing.T) {
	var evals uint64
	_ = decider.Decide(decider.Facts{
		Excluded: true, HasShardCopy: true, ZoneCopies: 9, PerZone: 1,
		Used: 99, Total: 100, Size: 99, LowPct: 1, HighPct: 1,
	}, &evals)
	if evals != 1 {
		t.Fatalf("evals after D1 = %d, want 1", evals)
	}

	evals = 0
	_ = decider.Decide(decider.Facts{
		HasShardCopy: false, ZoneCopies: 0, PerZone: 2,
		Used: 0, Total: 100, Size: 1, LowPct: 80, HighPct: 90, Primary: true,
	}, &evals)
	if evals != 4 {
		t.Fatalf("evals after pass = %d, want 4", evals)
	}

	evals = 0
	_ = decider.Decide(decider.Facts{Excluded: true}, nil)
	if evals != 0 {
		t.Fatalf("nil counter must stay untouched, got %d", evals)
	}
}

func TestCeilExamples(t *testing.T) {
	ceil := func(a, b int) int { return (a + b - 1) / b }
	if ceil(3, 2) != 2 || ceil(2, 2) != 1 || ceil(5, 3) != 2 || ceil(1, 3) != 1 {
		t.Fatal("ceil wrong")
	}
}
