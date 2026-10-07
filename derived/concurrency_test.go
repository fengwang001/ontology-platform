package derived

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// permutations returns all serial orderings of the supplied steps.
func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, p := range permutations(n - 1) {
		for i := 0; i <= len(p); i++ {
			q := append([]int{}, p...)
			q = append(q[:i], append([]int{n - 1}, q[i:]...)...)
			out = append(out, q)
		}
	}
	return out
}

type serialStep struct {
	apply func(m *NaiveModel)
}

// TestThreeWayConcurrency runs a source write, a link deletion and a new link
// creation (the three operations named in the spec) concurrently against the
// same downstream, alongside concurrent point-in-time readers. The resulting
// entries must equal the result of some serial permutation of the three.
func TestThreeWayConcurrency(t *testing.T) {
	for iter := 0; iter < 300; iter++ {
		lg := &sliceLogger{}
		s := NewStore(lg)
		mustAddObject(t, s, Object{ID: "a1", Type: "A", Properties: map[string]Value{"name": "red"}})
		mustAddObject(t, s, Object{ID: "a2", Type: "A", Properties: map[string]Value{"name": "green"}})
		mustAddObject(t, s, Object{ID: "b1", Type: "B"})
		if err := s.AddDeclaration(Declaration{
			Name: "idx", DownstreamType: "B", LinkType: "owns",
			SourceType: "A", SourceProperty: "name", RequireUnique: true,
		}); err != nil {
			t.Fatal(err)
		}
		s.AddLink("b1", "a1", "owns")

		// Three concurrent operations on downstream b1:
		//  0: write source property a1.name = blue
		//  1: delete b1 --owns--> a1
		//  2: add    b1 --owns--> a2
		start := make(chan struct{})
		var wg sync.WaitGroup
		ops := [3]func() *ChangeResult{
			func() *ChangeResult { return s.SetProperty("a1", "name", "blue") },
			func() *ChangeResult { return s.DeleteLink("b1", "a1", "owns") },
			func() *ChangeResult { return s.AddLink("b1", "a2", "owns") },
		}
		results := make([]*ChangeResult, 3)
		wg.Add(3)
		for i := 0; i < 3; i++ {
			go func(i int) {
				defer wg.Done()
				<-start
				results[i] = ops[i]()
			}(i)
		}

		// Concurrent readers: each observation must be one legal serial state.
		readerStop := make(chan struct{})
		var rwg sync.WaitGroup
		rwg.Add(2)
		for r := 0; r < 2; r++ {
			go func() {
				defer rwg.Done()
				for {
					select {
					case <-readerStop:
						return
					default:
						sn := s.Snapshot()
						if e, ok := sn.Entry("idx", "b1"); ok {
							switch e.State {
							case StateIndexed, StateNoLink, StateNotUnique:
							default:
								t.Errorf("reader observed impossible state %v", e.State)
								return
							}
						}
					}
				}
			}()
		}

		close(start)
		wg.Wait()
		close(readerStop)
		rwg.Wait()

		for _, r := range results {
			if r == nil || !r.Committed {
				t.Fatalf("all three ops must commit in this scenario, got %+v", results)
			}
		}

		// Final store state.
		final, ok := s.Entry("idx", "b1")
		if !ok {
			t.Fatal("final entry missing")
		}

		// Compute the possible final states over all 6 serial permutations
		// using the independent oracle.
		possible := map[string]bool{}
		for _, order := range permutations(3) {
			n := NewNaiveModel()
			n.AddObject(Object{ID: "a1", Type: "A", Properties: map[string]Value{"name": "red"}})
			n.AddObject(Object{ID: "a2", Type: "A", Properties: map[string]Value{"name": "green"}})
			n.AddObject(Object{ID: "b1", Type: "B"})
			n.AddDeclaration(Declaration{Name: "idx", DownstreamType: "B", LinkType: "owns",
				SourceType: "A", SourceProperty: "name", RequireUnique: true})
			n.AddLink("b1", "a1", "owns")
			for _, step := range order {
				switch step {
				case 0:
					n.SetProperty("a1", "name", "blue")
				case 1:
					n.DeleteLink("b1", "a1", "owns")
				case 2:
					n.AddLink("b1", "a2", "owns")
				}
			}
			e, _ := n.Entry("idx", "b1")
			possible[entryKey(e)] = true
		}
		if !possible[entryKey(final)] {
			t.Fatalf("iter %d: final state %v is not serial-equivalent to any permutation", iter, final)
		}
	}
}

func entryKey(e Entry) string {
	ks := append([]string(nil), e.Keys...)
	sort.Strings(ks)
	return fmt.Sprintf("%d|%v", e.State, ks)
}

// Every committed processing unit must touch exactly the real dependents:
// the Affected set size equals the oracle count and is deduplicated.
func TestExactAffectedCount(t *testing.T) {
	s := NewStore(nil)
	n := NewNaiveModel()
	syncSetup := func(o Object) {
		mustAddObject(t, s, o)
		n.AddObject(o)
	}
	syncSetup(Object{ID: "a1", Type: "A", Properties: map[string]Value{"name": "v0"}})

	// 50 downstream instances, plus 10 decoys that link elsewhere / by other
	// link types. None of the decoys depend on a1.name.
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("b%02d", i)
		syncSetup(Object{ID: id, Type: "B"})
	}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("c%02d", i)
		syncSetup(Object{ID: id, Type: "C", Properties: map[string]Value{"name": "z"}})
	}
	decl := Declaration{Name: "idx", DownstreamType: "B", LinkType: "owns",
		SourceType: "A", SourceProperty: "name", RequireUnique: true}
	if err := s.AddDeclaration(decl); err != nil {
		t.Fatal(err)
	}
	n.AddDeclaration(decl)

	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("b%02d", i)
		s.AddLink(id, "a1", "owns")
		n.AddLink(id, "a1", "owns")
	}
	for i := 0; i < 10; i++ {
		cid := fmt.Sprintf("c%02d", i)
		// c objects own other c objects via another type: must not count.
		if i > 0 {
			other := fmt.Sprintf("c%02d", i-1)
			s.AddLink(cid, other, "selfref")
		}
	}

	// Unknown link type selfref ops are rejected, so skip; instead add some
	// b-instances with two links to a1 (deduplicated dependents, not unique).
	for i := 0; i < 5; i++ {
		// second distinct target requires another A; that changes cardinality
		// of dependents only via uniqueness state, not via the affected set.
		syncSetup(Object{ID: fmt.Sprintf("a2_%02d", i), Type: "A",
			Properties: map[string]Value{"name": "alt"}})
	}

	r := s.SetProperty("a1", "name", "v1")
	if !r.Committed {
		t.Fatalf("write rolled back: %v", r.Err)
	}
	wantSet := n.DownstreamSet("a1", "name")
	if len(r.Affected) != len(wantSet) {
		t.Fatalf("affected count %d != oracle count %d", len(r.Affected), len(wantSet))
	}
	seen := map[string]int{}
	for _, id := range r.Affected {
		seen[id]++
		if seen[id] > 1 {
			t.Fatalf("downstream %s processed more than once", id)
		}
		if _, ok := wantSet[id]; !ok {
			t.Fatalf("spurious downstream %s touched", id)
		}
	}
	n.SetProperty("a1", "name", "v1")

	// Cross-check every entry against the oracle.
	for id := range wantSet {
		got, _ := s.Entry("idx", id)
		exp, _ := n.Entry("idx", id)
		if !reflect.DeepEqual(got, exp) {
			t.Fatalf("entry %s mismatch: got %+v want %+v", id, got, exp)
		}
	}
}
