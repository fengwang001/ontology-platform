package topk

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

func ids(xs []Item) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.ItemID
	}
	return out
}

func add(t *testing.T, s *Set, it Item) {
	t.Helper()
	if err := s.Add(it); err != nil {
		t.Fatal(err)
	}
}

func TestSevenSteps(t *testing.T) {
	s := NewSet(2)
	ops := []Item{{"a", 10}, {"b", 20}, {"c", 15}, {"d", 15}, {"e", 20}}
	wantTop := [][]string{{"a"}, {"b", "a"}, {"b", "c"}, {"b", "c"}, {"b", "e"}}
	for i := range ops {
		add(t, s, ops[i])
		if got := ids(s.Top()); !reflect.DeepEqual(got, wantTop[i]) {
			t.Fatalf("step %d top = %v, want %v", i+1, got, wantTop[i])
		}
	}
	for i, want := range [][]string{{"e", "c"}, {"c", "d"}} {
		if err := s.Remove([]string{"b", "e"}[i]); err != nil {
			t.Fatal(err)
		}
		if got := ids(s.Top()); !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d top = %v, want %v", i+6, got, want)
		}
	}
}

// TestPromotion: in-list removal promotes best below-line item; below removal is a no-op.
func TestPromotion(t *testing.T) {
	cases := []struct {
		name       string
		seed       []Item
		del        string
		want       []string
		listStable bool
	}{
		{"in-list promotes", []Item{{"a", 1}, {"b", 2}, {"c", 3}, {"d", 4}}, "d", []string{"c", "b"}, false},
		{"below no change", []Item{{"a", 1}, {"b", 2}, {"c", 3}, {"d", 4}}, "a", []string{"d", "c"}, true},
		{"ties id order", []Item{{"c", 15}, {"d", 15}, {"e", 20}, {"b", 20}}, "b", []string{"e", "c"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSet(2)
			for _, it := range tc.seed {
				add(t, s, it)
			}
			before := ids(s.Top())
			if err := s.Remove(tc.del); err != nil {
				t.Fatal(err)
			}
			got := ids(s.Top())
			if tc.listStable && !reflect.DeepEqual(got, before) {
				t.Fatalf("below removal changed list: %v -> %v", before, got)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("after remove %s: %v, want %v", tc.del, got, tc.want)
			}
		})
	}
}

func bruteTop(live map[string]int, k int) []string {
	all := make([]Item, 0, len(live))
	for id, sc := range live {
		all = append(all, Item{id, sc})
	}
	sort.Slice(all, func(i, j int) bool { return less(all[i], all[j]) })
	if len(all) > k {
		all = all[:k]
	}
	return ids(all)
}

// TestBatchRecompute: random Add/Remove sequences match batch recompute every op.
func TestBatchRecompute(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		rng := rand.New(rand.NewSource(seed))
		s, live := NewSet(7), map[string]int{}
		for step := 0; step < 2000; step++ {
			id := fmt.Sprintf("i%03d", rng.Intn(120))
			_, exists := live[id]
			if exists && rng.Intn(3) == 0 {
				if err := s.Remove(id); err != nil {
					t.Fatal(err)
				}
				delete(live, id)
			} else if !exists {
				sc := rng.Intn(50)
				add(t, s, Item{id, sc})
				live[id] = sc
			}
			if got, want := ids(s.Top()), bruteTop(live, 7); !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d step %d: got %v want %v", seed, step, got, want)
			}
		}
	}
}

// TestAdmissionCheckBounded: admission stays constant independent of group size.
func TestAdmissionCheckBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := NewSet(10)
		for i := 0; i < m; i++ {
			add(t, s, Item{fmt.Sprintf("x%05d", i), i})
		}
		add(t, s, Item{"newcomer", m + 1})
		if s.lastCheck > 2 {
			t.Fatalf("m=%d admission not bounded by constant", m)
		}
	}
}

// TestRejectedOpsNoTrace: rejected Add/Remove leave no trace.
func TestRejectedOpsNoTrace(t *testing.T) {
	s := NewSet(2)
	for _, it := range []Item{{"b", 20}, {"c", 15}, {"a", 10}} {
		add(t, s, it)
	}
	before := ids(s.Top())
	if err := s.Add(Item{"c", 999}); err != ErrDuplicate {
		t.Fatalf("dup add err = %v, want ErrDuplicate", err)
	}
	if err := s.Remove("ghost"); err != ErrNotFound {
		t.Fatalf("missing remove err = %v, want ErrNotFound", err)
	}
	if after := ids(s.Top()); !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected ops changed top: %v -> %v", before, after)
	}
	if err := s.Add(Item{"c", 1}); err != ErrDuplicate {
		t.Fatalf("c must still exist once, got %v", err)
	}
}
