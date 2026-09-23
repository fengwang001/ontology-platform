package vv

import (
	"errors"
	"math"
	"testing"
)

func TestRelationTable(t *testing.T) {
	cases := []struct {
		name string
		a    Vector
		b    Vector
		want Relation
	}{
		{"both empty", Vector{}, Vector{}, Equal},
		{"missing zero is equal", Vector{"A": 1}, Vector{"A": 1, "B": 0}, Equal},
		{"one event each", Vector{"A": 1, "B": 0}, Vector{"A": 0, "B": 1}, Concurrent},
		{"same replica advance", Vector{"A": 1, "B": 0}, Vector{"A": 2, "B": 0}, Before},
		{"same replica reverse", Vector{"A": 2, "B": 0}, Vector{"A": 1, "B": 0}, After},
		{"strict after sparse", Vector{"A": 2, "B": 2}, Vector{"A": 1, "B": 1}, After},
		{"strict before sparse", Vector{"A": 1, "B": 1}, Vector{"A": 2, "B": 2}, Before},
		{"mixed counters", Vector{"A": 1, "B": 2}, Vector{"A": 2, "B": 1}, Concurrent},
		{"explicit zero equal", Vector{"A": 0}, Vector{"A": 0, "B": 0}, Equal},
		{"missing counter before", Vector{"A": 1}, Vector{"A": 1, "B": 1}, Before},
		{"missing counter after", Vector{"A": 1, "B": 1}, Vector{"A": 1}, After},
		{"disjoint replicas", Vector{"A": 3}, Vector{"B": 3}, Concurrent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, stats := CompareStats(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("Compare() = %s, want %s", got, tc.want)
			}
			union := len(Keys(tc.a, tc.b))
			if stats.Comparisons > 2*union {
				t.Fatalf("comparisons = %d, bound = %d", stats.Comparisons, 2*union)
			}
		})
	}
}

func TestIncrementAndRegistryTable(t *testing.T) {
	registered := map[string]struct{}{"A": {}}
	cases := []struct {
		name    string
		v       Vector
		id      string
		wantErr error
		want    uint64
	}{
		{"increment existing", Vector{"A": 1}, "A", nil, 2},
		{"increment missing", Vector{}, "A", nil, 1},
		{"overflow", Vector{"A": math.MaxUint64}, "A", ErrOverflow, math.MaxUint64},
		{"unknown", Vector{"B": 1}, "B", ErrUnknownReplica, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.wantErr == ErrUnknownReplica {
				err = ValidateRegistry(tc.v, registered)
			} else {
				var got Vector
				got, err = Increment(tc.v, tc.id)
				if err == nil && got[tc.id] != tc.want {
					t.Fatalf("counter = %d, want %d", got[tc.id], tc.want)
				}
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
