package api

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"

	"ontology/casc"
	"ontology/ent"
)

// TestRejectedOpsNoTrace pins invariant 4 through the public facade: the
// four failure cases are mutually distinct sentinels, rejected ops leave
// no trace, and the set stays usable afterwards.
func TestRejectedOpsNoTrace(t *testing.T) {
	a := New()
	mustAdd(t, a,
		[2]int{1, 0}, [2]int{2, 1}, [2]int{8, 0}, [2]int{9, 8})
	snap := func() string {
		b := ""
		for id := 0; id <= 12; id++ {
			b += strconv.FormatBool(a.Exists(id))
		}
		return b + fmt.Sprint(a.Orphans())
	}
	cases := []struct {
		name string
		id   int
		par  int
		want error
	}{
		{"zero id", 0, 0, ent.ErrInvalidID},
		{"negative id", -3, 0, ent.ErrInvalidID},
		{"duplicate id", 8, 0, ent.ErrDuplicateID},
		{"missing parent", 11, 98, ent.ErrInvalidParent},
	}
	before := snap()
	for _, c := range cases {
		err := a.Add(c.id, c.par)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v want %v", c.name, err, c.want)
		}
		if snap() != before {
			t.Fatalf("%s mutated state", c.name)
		}
	}
	if _, err := a.Delete(404); !errors.Is(err, casc.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if snap() != before {
		t.Fatal("delete-missing mutated state")
	}
	for i, e1 := range []error{ent.ErrInvalidID, ent.ErrDuplicateID, ent.ErrInvalidParent, casc.ErrNotFound} {
		for _, e2 := range []error{ent.ErrInvalidID, ent.ErrDuplicateID, ent.ErrInvalidParent, casc.ErrNotFound}[i+1:] {
			if errors.Is(e1, e2) {
				t.Fatalf("sentinels %v and %v not distinct", e1, e2)
			}
		}
	}
	if err := a.Add(11, 8); err != nil { // still usable after rejects
		t.Fatalf("add after rejects: %v", err)
	}
	if !a.Exists(11) {
		t.Fatal("Add after rejects did not persist")
	}
}

func mustAdd(t *testing.T, a *API, pairs ...[2]int) {
	t.Helper()
	for _, p := range pairs {
		if err := a.Add(p[0], p[1]); err != nil {
			t.Fatalf("seed %v: %v", p, err)
		}
	}
}

// TestFacadeFlow checks the public Delete/Orphans/CleanupOrphans outputs on
// the NOTES.md scenario (orphan 10 seeded via casc.Insert is not available
// here, so the orphan is created by deleting its parent).
func TestFacadeFlow(t *testing.T) {
	a := New()
	mustAdd(t, a,
		[2]int{1, 0}, [2]int{2, 1}, [2]int{3, 1}, [2]int{4, 2}, [2]int{5, 2},
		[2]int{6, 3}, [2]int{7, 4}, [2]int{8, 0}, [2]int{9, 8})
	got, err := a.Delete(1)
	if err != nil || !slices.Equal(got, []int{7, 4, 5, 2, 6, 3, 1}) {
		t.Fatalf("Delete(1)=%v,%v", got, err)
	}
	if !a.Exists(8) || a.Exists(1) {
		t.Fatal("existence wrong after delete")
	}
	if o := a.Orphans(); len(o) != 0 {
		t.Fatalf("unexpected orphans %v", o)
	}
	if got := a.CleanupOrphans(); len(got) != 0 {
		t.Fatalf("cleanup on clean set = %v", got)
	}
}

// TestSelfCheck runs the built-in four-invariant self-check repeatedly and
// concurrently; it must always pass and never mutate receiver state.
func TestSelfCheck(t *testing.T) {
	a := New()
	const n = 16
	errs := make(chan error, n*20)
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			for range 20 {
				errs <- a.SelfCheck()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
