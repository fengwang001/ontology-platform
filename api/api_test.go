package api

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, v *View, key, id string, sc int) {
	t.Helper()
	if err := v.Add(key, id, sc); err != nil {
		t.Fatal(err)
	}
}

func ids(xs []Item) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = x.ItemID
	}
	return out
}

// TestFaultsDistinct: the four fault classes are decidable and pairwise distinct,
// and each rejected operation leaves the view usable with unchanged state.
func TestFaultsDistinct(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("New(0) err = %v, want ErrInvalidK", err)
	}
	v, err := New(2)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, v, "g", "b", 20)
	mustAdd(t, v, "g", "c", 15)
	before := v.TopK("g")
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"empty key", func() error { return v.Add("", "x", 1) }, ErrEmptyArgument},
		{"empty id", func() error { return v.Add("g", "", 1) }, ErrEmptyArgument},
		{"empty remove", func() error { return v.Remove("g", "") }, ErrEmptyArgument},
		{"duplicate", func() error { return v.Add("g", "c", 999) }, ErrDuplicate},
		{"not found", func() error { return v.Remove("g", "ghost") }, ErrNotFound},
	}
	seen := map[error]bool{ErrInvalidK: true}
	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v, want %v", tc.name, err, tc.want)
		}
		seen[tc.want] = true
		if !reflect.DeepEqual(v.TopK("g"), before) {
			t.Fatalf("%s left a trace", tc.name)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("fault classes not pairwise distinct: %d", len(seen))
	}
	mustAdd(t, v, "g", "n", 5) // view still usable after rejections
}

// TestSelfCheck passes on a healthy view.
func TestSelfCheck(t *testing.T) {
	v, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestAPIBatchRecompute checks multiple groups through the public API against
// the independent batch oracle under a random Add/Remove stream.
func TestAPIBatchRecompute(t *testing.T) {
	for _, seed := range []int64{7, 8} {
		v, _ := New(5)
		rng := rand.New(rand.NewSource(seed))
		live := map[string]map[string]int{}
		for step := 0; step < 1500; step++ {
			key := fmt.Sprintf("k%d", rng.Intn(4))
			id := fmt.Sprintf("i%03d", rng.Intn(80))
			g := live[key]
			if g == nil {
				g = map[string]int{}
				live[key] = g
			}
			_, exists := g[id]
			switch {
			case exists && rng.Intn(3) == 0:
				if err := v.Remove(key, id); err != nil {
					t.Fatal(err)
				}
				delete(g, id)
			case !exists:
				sc := rng.Intn(40)
				mustAdd(t, v, key, id, sc)
				g[id] = sc
			default:
				continue // exists and kept this round: no mutation
			}
			if got, want := ids(v.TopK(key)), ids(batch(g, 5)); !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d step %d key %s: %v want %v", seed, step, key, got, want)
			}
		}
	}
}

// TestConcurrentTopK: many readers of one populated group get identical lists.
func TestConcurrentTopK(t *testing.T) {
	v, _ := New(10)
	for i := 0; i < 200; i++ { // 10 in-list, 190 below-line
		mustAdd(t, v, "g", fmt.Sprintf("i%03d", i), i)
	}
	want := v.TopK("g")
	const n = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < 200; r++ {
				if got := v.TopK("g"); !reflect.DeepEqual(got, want) {
					errs <- fmt.Errorf("reader saw %v want %v", ids(got), ids(want))
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
