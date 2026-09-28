package txnset

import (
	"reflect"
	"testing"
)

func TestMergeIntervals(t *testing.T) {
	cases := []struct {
		name string
		in   []Interval
		want []Interval
	}{
		{"empty", nil, nil},
		{"single", []Interval{{3, 3}}, []Interval{{3, 3}}},
		{"adjacent", []Interval{{1, 2}, {3, 4}}, []Interval{{1, 4}}},
		{"overlapping", []Interval{{1, 5}, {4, 8}}, []Interval{{1, 8}}},
		{"contained", []Interval{{1, 10}, {3, 4}}, []Interval{{1, 10}}},
		{"touching point", []Interval{{1, 3}, {3, 7}}, []Interval{{1, 7}}},
		{"disjoint", []Interval{{1, 2}, {5, 6}}, []Interval{{1, 2}, {5, 6}}},
		{"unordered duplicates",
			[]Interval{{9, 9}, {1, 2}, {3, 4}, {1, 2}, {9, 10}, {2, 3}},
			[]Interval{{1, 4}, {9, 10}}},
		{"max uint64 adjacency safe",
			[]Interval{{1, 5}, {6, 18446744073709551615}},
			[]Interval{{1, 18446744073709551615}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeIntervals(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("mergeIntervals(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestMergeIntervalsDoesNotMutateInput(t *testing.T) {
	in := []Interval{{5, 6}, {1, 2}}
	orig := append([]Interval(nil), in...)
	_ = mergeIntervals(in)
	if !reflect.DeepEqual(in, orig) {
		t.Fatalf("input mutated: %v", in)
	}
}

func TestSubtractIntervals(t *testing.T) {
	const max = ^uint64(0)
	cases := []struct {
		name string
		a, b []Interval
		want []Interval
	}{
		{"empty both", nil, nil, nil},
		{"empty right", []Interval{{1, 5}}, nil, []Interval{{1, 5}}},
		{"fully removed", []Interval{{1, 10}}, []Interval{{1, 10}}, nil},
		{"remove head", []Interval{{1, 10}}, []Interval{{1, 4}}, []Interval{{5, 10}}},
		{"remove tail", []Interval{{1, 10}}, []Interval{{7, 10}}, []Interval{{1, 6}}},
		{"remove middle splits",
			[]Interval{{1, 10}}, []Interval{{4, 6}},
			[]Interval{{1, 3}, {7, 10}}},
		{"remove endpoint single points",
			[]Interval{{1, 5}}, []Interval{{1, 1}, {5, 5}},
			[]Interval{{2, 4}}},
		{"multiple cutters",
			[]Interval{{1, 20}}, []Interval{{3, 5}, {8, 8}, {12, 14}},
			[]Interval{{1, 2}, {6, 7}, {9, 11}, {15, 20}}},
		{"closed boundary no gap left",
			[]Interval{{1, 10}}, []Interval{{1, 5}},
			[]Interval{{6, 10}}},
		{"cutter beyond", []Interval{{1, 5}}, []Interval{{6, 100}}, []Interval{{1, 5}}},
		{"multi left",
			[]Interval{{1, 3}, {7, 9}}, []Interval{{2, 7}},
			[]Interval{{1, 1}, {8, 9}}},
		{"cutter up to max", []Interval{{1, max}}, []Interval{{100, max}}, []Interval{{1, 99}}},
		{"max point removed", []Interval{{max, max}}, []Interval{{max, max}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := subtractIntervals(tc.a, tc.b)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("subtractIntervals(%v,%v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestSubtractIntervalsDoesNotMutateInputs(t *testing.T) {
	a := []Interval{{1, 10}}
	b := []Interval{{4, 6}}
	aOrig := append([]Interval(nil), a...)
	bOrig := append([]Interval(nil), b...)
	_ = subtractIntervals(a, b)
	if !reflect.DeepEqual(a, aOrig) || !reflect.DeepEqual(b, bOrig) {
		t.Fatalf("input mutated: a=%v b=%v", a, b)
	}
}
