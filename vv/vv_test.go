package vv

import (
	"errors"
	"testing"
)

func vec(pairs ...any) Vector {
	v := Vector{}
	for i := 0; i < len(pairs); i += 2 {
		v[pairs[i].(string)] = pairs[i+1].(uint64)
	}
	return v
}

func TestCompareRelations(t *testing.T) {
	cases := []struct {
		name string
		a, b Vector
		want Relation
	}{
		{"equal-empty", Vector{}, Vector{}, RelEqual},
		{"equal-zero-missing", vec("A", 1), vec("A", 1, "B", 0), RelEqual},
		{"equal-explicit", vec("A", 1, "B", 2), vec("A", 1, "B", 2), RelEqual},
		{"all-zero", vec("A", 0, "B", 0), vec("C", 0), RelEqual},
		{"before-simple", vec("A", 1), vec("A", 2), RelBefore},
		{"before-extra-zero", vec("A", 1, "B", 0), vec("A", 2, "B", 0), RelBefore},
		{"before-dominated", vec("A", 1, "B", 1), vec("A", 2, "B", 3), RelBefore},
		{"after-simple", vec("A", 2), vec("A", 1), RelAfter},
		{"after-dominated", vec("A", 3, "B", 2), vec("A", 2, "B", 1), RelAfter},
		{"concurrent-cross", vec("A", 1, "B", 0), vec("A", 0, "B", 1), RelConcurrent},
		{"concurrent-diverge", vec("A", 2, "B", 1), vec("A", 1, "B", 2), RelConcurrent},
		{"concurrent-third", vec("A", 5, "C", 1), vec("B", 4, "C", 1), RelConcurrent},
		{"concurrent-missing-key", vec("A", 1), vec("B", 1), RelConcurrent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compare(tc.a, tc.b); got != tc.want {
				t.Fatalf("Compare = %s, want %s", got, tc.want)
			}
			inv := map[Relation]Relation{RelBefore: RelAfter, RelAfter: RelBefore}
			if w, ok := inv[tc.want]; ok {
				if got := Compare(tc.b, tc.a); got != w {
					t.Fatalf("inverse Compare = %s, want %s", got, w)
				}
			} else if got := Compare(tc.b, tc.a); got != tc.want {
				t.Fatalf("symmetric Compare = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestCompareCounterBound(t *testing.T) {
	cases := []struct {
		name           string
		a, b           Vector
		wantReadsBound uint64
	}{
		{"disjoint", vec("A", 1), vec("B", 1), 4},
		{"overlap", vec("A", 1, "B", 2), vec("B", 2, "C", 3), 6},
		{"identical", vec("A", 1, "B", 2), vec("A", 1, "B", 2), 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c Counter
			CompareCounted(tc.a, tc.b, &c)
			union := uint64(len(tc.a) + len(tc.b))
			// 2 * |union| 是与全局历史副本无关的上界。
			bound := 2 * union
			for k := range tc.a {
				if _, ok := tc.b[k]; ok {
					bound -= 2
				}
			}
			if c.Value() != bound {
				t.Fatalf("reads = %d, want %d", c.Value(), bound)
			}
			if c.Value() > tc.wantReadsBound {
				t.Fatalf("reads %d > bound %d", c.Value(), tc.wantReadsBound)
			}
		})
	}
}

func TestIncrementAndFaults(t *testing.T) {
	r := NewRegistry("A", "B")
	v := Vector{}
	for i := uint64(1); i <= 3; i++ {
		nv, err := r.Increment(v, "A")
		if err != nil || nv["A"] != i {
			t.Fatalf("increment %d: v=%v err=%v", i, nv, err)
		}
		v = nv
	}
	if _, err := r.Increment(v, "Z"); !errors.Is(err, ErrUnknownReplica) {
		t.Fatalf("unknown = %v", err)
	}
	v["A"] = ^uint64(0)
	if _, err := r.Increment(v, "A"); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow = %v", err)
	}
	if err := r.Validate(vec("A", 1, "Q", 2)); !errors.Is(err, ErrUnknownReplica) {
		t.Fatalf("validate = %v", err)
	}
	water := vec("A", 5, "B", 2)
	rollbacks := []Vector{vec("A", 4, "B", 2), vec("A", 5, "B", 1)}
	for i, got := range rollbacks {
		if !Rollback(got, water) {
			t.Fatalf("case %d not detected", i)
		}
	}
	if Rollback(vec("A", 5, "B", 3), water) {
		t.Fatal("advance misclassified as rollback")
	}
}

func TestProjection(t *testing.T) {
	r := NewRegistry("A", "C")
	got := r.Project(vec("A", 1, "B", 9, "C", 2))
	if !got.Equal(vec("A", 1, "C", 2)) {
		t.Fatalf("project = %v", got)
	}
	retired := map[string]struct{}{"B": {}}
	got2 := ProjectSet(vec("A", 1, "B", 9), retired)
	if !got2.Equal(vec("A", 1)) {
		t.Fatalf("projectset = %v", got2)
	}
}
