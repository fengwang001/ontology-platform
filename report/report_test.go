package report

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/filter"
	"ontology/policy"
	"ontology/predicate"
)

func TestReportDeterminism(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		hidden     []string
		pred       predicate.Node
		wantDenied bool
		wantRemoved []string
		wantRefCols []string
	}{
		{
			name:         "denied with duplicate column paths",
			role:         "user",
			hidden:       []string{"secret", "secret", "zz"},
			pred: predicate.Or{Xs: []predicate.Node{
				predicate.Cmp{Col: "secret", Op: predicate.OpEq, Value: 1},
				predicate.Not{X: predicate.Cmp{Col: "secret", Op: predicate.OpIsNull}},
			}},
			wantDenied:   true,
			wantRemoved:  []string{"secret", "zz"},
			wantRefCols:  []string{"secret", "secret"},
		},
		{
			name:         "allowed query still reports removed columns",
			role:         "user",
			hidden:       []string{"secret"},
			pred:         predicate.Cmp{Col: "a", Op: predicate.OpEq, Value: 1},
			wantDenied:   false,
			wantRemoved:  []string{"secret"},
			wantRefCols:  nil,
		},
	}
	p := policy.New([]string{"a", "b", "secret", "zz"}, map[string][]string{"user": {"a", "b"}})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var baseline string
			rng := rand.New(rand.NewSource(1))
			for iter := 0; iter < 20; iter++ {
				hs := shuffled(tt.hidden, rng)
				_, err := filter.Compile(p, tt.role, tt.pred)
				r := Build(tt.role, hs, err)
				if r.Denied != tt.wantDenied {
					t.Fatalf("Denied = %v, want %v", r.Denied, tt.wantDenied)
				}
				if !reflect.DeepEqual(r.Removed, tt.wantRemoved) {
					t.Fatalf("Removed = %v, want %v", r.Removed, tt.wantRemoved)
				}
				var cols []string
				for _, ref := range r.Rejected {
					cols = append(cols, ref.Col)
				}
				if !reflect.DeepEqual(cols, tt.wantRefCols) {
					t.Fatalf("ref cols = %v, want %v", cols, tt.wantRefCols)
				}
				if baseline == "" {
					baseline = r.Marshal()
				} else if r.Marshal() != baseline {
					t.Fatalf("report bytes differ across shuffled construction:\n%s\n%s", r.Marshal(), baseline)
				}
			}
		})
	}
}

func TestReportRefPathsSorted(t *testing.T) {
	p := policy.New([]string{"a", "secret"}, map[string][]string{"user": {"a"}})
	_, err := filter.Compile(p, "user", predicate.Or{Xs: []predicate.Node{
		predicate.Not{X: predicate.Cmp{Col: "secret", Op: predicate.OpIsNull}},
		predicate.Cmp{Col: "secret", Op: predicate.OpEq, Value: 1},
	}})
	if !errors.Is(err, filter.ErrHiddenColumn) {
		t.Fatalf("want hidden error, got %v", err)
	}
	r := Build("user", []string{"secret"}, err)
	wantPaths := []string{"$/or[0]/not/cmp", "$/or[1]/cmp"}
	for i, ref := range r.Rejected {
		if ref.Path != wantPaths[i] {
			t.Fatalf("path %d = %s, want %s", i, ref.Path, wantPaths[i])
		}
	}
	if !r.Denied {
		t.Fatalf("Denied must be true")
	}
}

func shuffled(in []string, rng *rand.Rand) []string {
	out := append([]string(nil), in...)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}
