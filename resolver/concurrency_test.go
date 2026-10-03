package resolver

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// Concurrent resolves against a fixed instance set must each produce the
// same result as a serial resolve.
func TestConcurrentResolve(t *testing.T) {
	r := mustResolver(t, 16)
	mustAdd(t, r, "Show", Con("Int"))
	mustAdd(t, r, "Show", Con("List", Var(0)), Constraint{Trait: "Show", Type: Var(0)})
	mustAdd(t, r, "Show", Con("List", Con("Char")))
	mustAdd(t, r, "Eq", Con("Pair", Var(0), Var(1)),
		Constraint{Trait: "Show", Type: Var(0)},
		Constraint{Trait: "Show", Type: Var(1)},
	)
	goals := []struct {
		trait string
		ty    *Type
	}{
		{"Show", Con("Int")},
		{"Show", Con("List", Con("Int"))},
		{"Show", Con("List", Con("List", Con("Char")))},
		{"Show", Con("Char")},
		{"Eq", Con("Pair", Con("Int"), Con("List", Con("Char")))},
		{"Eq", Con("Pair", Con("Char"), Con("Int"))},
	}
	want := make([]string, len(goals))
	for i, g := range goals {
		tree, f, err := r.Resolve(g.trait, g.ty)
		if err != nil {
			t.Fatalf("Resolve(%s, %s): %v", g.trait, g.ty, err)
		}
		want[i] = renderResult(tree, f)
	}
	var wg sync.WaitGroup
	errs := make(chan string, 1024)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				idx := rng.Intn(len(goals))
				g := goals[idx]
				tree, f, err := r.Resolve(g.trait, g.ty)
				if err != nil {
					errs <- fmt.Sprintf("Resolve(%s, %s): %v", g.trait, g.ty, err)
					return
				}
				if got := renderResult(tree, f); got != want[idx] {
					errs <- fmt.Sprintf("Resolve(%s, %s) = %s, want %s", g.trait, g.ty, got, want[idx])
					return
				}
			}
		}(int64(worker) + 1)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	if err := r.selfCheck(); err != nil {
		t.Fatalf("selfCheck: %v", err)
	}
}

// Concurrent registrations and resolves must be equivalent to some serial
// order: instance ids form exactly 1..N and the final state resolves like
// the naive reference on the final instance set.
func TestConcurrentAddAndResolve(t *testing.T) {
	r := mustResolver(t, 16)
	const workers = 8
	const perWorker = 10
	var wg sync.WaitGroup
	ids := make(chan int, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				var head *Type
				if i%2 == 0 {
					head = Con(fmt.Sprintf("C%d_%d", w, i))
				} else {
					head = Con(fmt.Sprintf("W%d_%d", w, i), Var(0))
				}
				id, err := r.AddInstance("T", head, nil)
				if err != nil {
					t.Errorf("AddInstance: %v", err)
					return
				}
				ids <- id
				if _, _, err := r.Resolve("T", Con(fmt.Sprintf("C%d_%d", w, i))); err != nil {
					t.Errorf("Resolve: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(ids)
	var got []int
	for id := range ids {
		got = append(got, id)
	}
	sort.Ints(got)
	if len(got) != workers*perWorker {
		t.Fatalf("got %d instance ids, want %d", len(got), workers*perWorker)
	}
	for i, id := range got {
		if id != i+1 {
			t.Fatalf("instance ids are not a permutation of 1..N: %v", got)
		}
	}
	if got := r.InstanceCount(); got != workers*perWorker {
		t.Fatalf("instance count: got %d, want %d", got, workers*perWorker)
	}
	if err := r.selfCheck(); err != nil {
		t.Fatalf("selfCheck: %v", err)
	}
	naive := &nResolver{d: 16}
	for _, inst := range r.instances {
		naive.insts = append(naive.insts, nInstance{id: inst.ID, trait: inst.Trait, head: inst.Head, context: inst.Context})
	}
	for w := 0; w < workers; w++ {
		for _, name := range []string{fmt.Sprintf("C%d_0", w), fmt.Sprintf("W%d_1", w)} {
			mtree, mf, merr := r.Resolve("T", Con(name))
			ntree, nf, _, _ := naive.resolve("T", Con(name))
			if merr != nil {
				t.Fatalf("Resolve(T, %s): %v", name, merr)
			}
			if got, want := renderResult(mtree, mf), renderResult(ntree, nf); got != want {
				t.Fatalf("Resolve(T, %s) = %s, want %s", name, got, want)
			}
		}
	}
}
