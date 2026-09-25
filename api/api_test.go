package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"sync"
	"testing"

	"ontology/api"
	"ontology/topk"
)

func it(id string, s int) api.Item { return api.Item{ItemID: id, Score: s} }

// batchTopK is the independent reference: sort survivors, take first k.
func batchTopK(model map[string]api.Item, k int) []api.Item {
	all := make([]api.Item, 0, len(model))
	for _, x := range model {
		all = append(all, x)
	}
	sort.Slice(all, func(i, j int) bool { return topk.Less(all[i], all[j]) })
	return all[:min(k, len(all))]
}

// TestAgainstBatchRecompute pins invariant 1 over random op sequences.
func TestAgainstBatchRecompute(t *testing.T) {
	for _, tc := range []struct{ k, seed, ops, ids, scores int }{
		{1, 1, 500, 10, 20}, {2, 7, 1000, 30, 50},
		{5, 9, 2000, 60, 100}, {10, 3, 2000, 100, 200},
	} {
		v, _ := api.New(tc.k)
		r, model := rand.New(rand.NewSource(int64(tc.seed))), map[string]api.Item{}
		for i := 0; i < tc.ops; i++ {
			id, sc := fmt.Sprintf("id%03d", r.Intn(tc.ids)), r.Intn(tc.scores)
			if r.Intn(2) == 0 {
				if err := v.Add("g", id, sc); err == nil {
					model[id] = it(id, sc)
				} else if !errors.Is(err, api.ErrDuplicate) {
					t.Fatalf("k=%d: add: %v", tc.k, err)
				}
			} else if err := v.Remove("g", id); err == nil {
				delete(model, id)
			} else if !errors.Is(err, api.ErrNotFound) {
				t.Fatalf("k=%d: remove: %v", tc.k, err)
			}
		}
		if got, want := v.TopK("g"), batchTopK(model, tc.k); !slices.Equal(got, want) {
			t.Fatalf("k=%d: board %v, batch %v", tc.k, got, want)
		}
	}
}

// TestRejectedOpsNoSideEffect pins invariant 4 and sentinel distinctness.
func TestRejectedOpsNoSideEffect(t *testing.T) {
	for _, k := range []int{0, -5} {
		if _, err := api.New(k); !errors.Is(err, api.ErrNonPositiveK) {
			t.Fatalf("New(%d): %v", k, err)
		}
	}
	v, _ := api.New(2)
	for _, x := range []api.Item{it("a", 10), it("b", 20), it("c", 15)} {
		v.Add("g", x.ItemID, x.Score)
	}
	v.Add("h", "x", 1)
	snapG, snapH := v.TopK("g"), v.TopK("h")
	sents := []error{api.ErrNonPositiveK, api.ErrEmpty, api.ErrDuplicate, api.ErrNotFound}
	rejects := []struct {
		err  error
		want error
	}{
		{v.Add("", "x", 1), api.ErrEmpty},
		{v.Add("g", "", 1), api.ErrEmpty},
		{v.Remove("", "x"), api.ErrEmpty},
		{v.Add("g", "c", 999), api.ErrDuplicate},
		{v.Remove("g", "zz"), api.ErrNotFound},
		{v.Remove("nope", "x"), api.ErrNotFound},
	}
	for _, tc := range rejects {
		if !errors.Is(tc.err, tc.want) {
			t.Errorf("got %v, want %v", tc.err, tc.want)
		}
		for _, s := range sents {
			if s != tc.want && errors.Is(tc.err, s) {
				t.Errorf("%v unexpectedly matches %v", tc.err, s)
			}
		}
	}
	if !slices.Equal(v.TopK("g"), snapG) || !slices.Equal(v.TopK("h"), snapH) {
		t.Fatal("rejected ops changed state")
	}
	if err := errors.Join(v.Add("g", "d", 15), v.Remove("g", "b")); err != nil {
		t.Fatalf("view unusable after rejects: %v", err)
	}
	if got, want := v.TopK("g"), []api.Item{it("c", 15), it("d", 15)}; !slices.Equal(got, want) {
		t.Fatalf("post-reject board %v, want %v", got, want)
	}
}

// TestConcurrentTopK: many goroutines read the same filled group; all boards
// must be identical. Barrier channel instead of sleeps.
func TestConcurrentTopK(t *testing.T) {
	v, _ := api.New(5)
	for i := 0; i < 50; i++ {
		v.Add("g", fmt.Sprintf("w%02d", i), (i*13)%40)
	}
	want, start, errs := v.TopK("g"), make(chan struct{}), make(chan error, 16)
	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := v.SelfCheck(); err != nil {
				errs <- err
				return
			}
			for i := 0; i < 300; i++ {
				if got := v.TopK("g"); !slices.Equal(got, want) {
					errs <- fmt.Errorf("diverged: %v", got)
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
