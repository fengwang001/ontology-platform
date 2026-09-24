package snap

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/state"
)

type op struct {
	k string
	v int64
} // v<0 means Delete(k)
type tcCase struct {
	ops []op
	cps []int // 1-based op indices after which a checkpoint fires
}

// replay rebuilds the last-checkpoint state on a plain empty map.
func replay(c tcCase) map[string]int64 {
	ref, last, at := map[string]int64{}, map[string]int64{}, map[int]bool{}
	for _, i := range c.cps {
		at[i] = true
	}
	for i, o := range c.ops {
		if o.v < 0 {
			delete(ref, o.k)
		} else {
			ref[o.k] = o.v
		}
		if at[i+1] {
			last = map[string]int64{}
			for k, v := range ref {
				last[k] = v
			}
		}
	}
	return last
}

// TestMergeMatchesReplay pins invariant 2: overlaying base+deltas in order
// equals replaying all changes from empty to the last checkpoint.
func TestMergeMatchesReplay(t *testing.T) {
	cases := []tcCase{
		{[]op{{"a", 1}, {"b", 2}, {"c", 3}, {"a", 10}, {"b", -1}, {"d", 4},
			{"a", 1}, {"c", 30}, {"e", 5}, {"c", 3}}, []int{3, 6, 9, 10}},
		{[]op{{"a", 1}, {"a", 2}, {"a", 1}, {"b", 5}, {"b", -1}}, []int{1, 5}},
		{[]op{{"a", 1}, {"a", -1}, {"a", 9}}, []int{1, 3}},
	}
	for seed := int64(0); seed < 20; seed++ { // random arrival orders
		r := rand.New(rand.NewSource(seed))
		ops := make([]op, 30)
		for i := range ops {
			v := int64(r.Intn(4))
			if r.Intn(3) == 0 {
				v = -1
			}
			ops[i] = op{fmt.Sprintf("k%d", r.Intn(6)), v}
		}
		cases = append(cases, tcCase{ops, []int{10, 20, 30}})
	}
	for i, c := range cases {
		st, s := state.New(), New()
		for j, o := range c.ops {
			if o.v < 0 {
				st.Delete(o.k)
			} else {
				st.Set(o.k, o.v)
			}
			for _, cp := range c.cps {
				if cp == j+1 {
					s.Checkpoint(st)
				}
			}
		}
		got, err := s.Recover()
		if err != nil {
			t.Fatal(err)
		}
		if want := replay(c); !reflect.DeepEqual(got, want) {
			t.Fatalf("case %d: got %v want %v", i, got, want)
		}
	}
}

// TestDeltaMinimalAndTombs pins invariant 3: a delta holds exactly the keys
// with a net change vs the previous checkpoint, deletions as tombstones.
func TestDeltaMinimalAndTombs(t *testing.T) {
	cases := []struct {
		run  func(*state.State, *Store)
		want map[string]Change
	}{
		{func(st *state.State, s *Store) {
			st.Set("a", 1)
			st.Set("b", 2)
			s.Checkpoint(st)
			st.Set("a", 10)
			st.Delete("b")
			st.Set("c", 3)
		}, map[string]Change{"a": {Val: 10}, "b": {Tomb: true}, "c": {Val: 3}}},
		{func(st *state.State, s *Store) { // changed back: omitted
			st.Set("a", 1)
			s.Checkpoint(st)
			st.Set("a", 2)
			st.Set("a", 1)
		}, map[string]Change{}},
		{func(st *state.State, s *Store) { // added+removed, missing delete: omitted
			st.Set("a", 1)
			s.Checkpoint(st)
			st.Set("ghost", 9)
			st.Delete("ghost")
			st.Delete("never")
		}, map[string]Change{}},
	}
	for i, c := range cases {
		st, s := state.New(), New()
		c.run(st, s)
		s.Checkpoint(st)
		got := s.deltas[len(s.deltas)-1]
		if !reflect.DeepEqual(got, c.want) {
			t.Fatalf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

// TestDeltaTraversalIsDirtyBound pins the complexity claim: the unexported
// counter equals the dirty-key count (1) with no m-scan term, across m tiers
// and random targets; read directly here as a white box.
func TestDeltaTraversalIsDirtyBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := rand.New(rand.NewSource(int64(m)))
		for iter := 0; iter < 5; iter++ {
			st, s := state.New(), New()
			for i := 0; i < m; i++ {
				st.Set(fmt.Sprintf("k%05d", i), int64(i))
			}
			s.Checkpoint(st)
			if s.scanned != 0 {
				t.Fatalf("base touched counter: %d", s.scanned)
			}
			st.Set(fmt.Sprintf("k%05d", r.Intn(m)), -1)
			s.Checkpoint(st)
			if s.scanned != 1 {
				t.Fatalf("m=%d iter=%d scanned=%d, want 1", m, iter, s.scanned)
			}
		}
	}
}
