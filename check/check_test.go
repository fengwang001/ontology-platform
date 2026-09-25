package check_test

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"ontology/bit"
	"ontology/check"
	"ontology/idx"
)

func mustErr[V any](_ V, e error) error { return e }

func TestMatchesNaive(t *testing.T) {
	for _, seed := range []uint64{1, 2} {
		const n = 1000
		tree, _ := idx.New(n)
		ref, rng := check.NewNaive(n), rand.New(rand.NewPCG(seed, 1))
		for range 10000 {
			i, d := rng.IntN(n), int64(rng.IntN(21)-10)
			tree.Add(idx.Index(i), d)
			ref.Add(i, d)
		}
		for i := -1; i < n; i++ {
			got, _ := tree.PrefixSum(idx.Index(i))
			if got != ref.PrefixSum(i) {
				t.Fatalf("PrefixSum(%d)=%d want %d", i, got, ref.PrefixSum(i))
			}
		}
		for range 100 {
			l, r := min(rng.IntN(n), rng.IntN(n)), max(rng.IntN(n), rng.IntN(n))
			got, _ := tree.RangeSum(idx.Index(l), idx.Index(r))
			if got != ref.RangeSum(l, r) {
				t.Fatalf("RangeSum(%d,%d)=%d want %d", l, r, got, ref.RangeSum(l, r))
			}
		}
	}
}

// buggyZeroBasedAdd inlines the "0-based without +1 shift" mistake.
func buggyZeroBasedAdd(a []int64, i, d int64) {
	for ; i < int64(len(a)); i += i & -i {
		a[i] += d
	}
}

func TestZeroBasedSpins(t *testing.T) {
	done := make(chan struct{}) // at i==0: i += lowbit(0)==0
	go func() { buggyZeroBasedAdd(make([]int64, 16), 0, 1); close(done) }()
	select {
	case <-done:
		t.Fatal("0-based add at index 0 must spin")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestVisitedBound(t *testing.T) {
	const n, bound = 100000, 18
	tree, _ := bit.New(n)
	for i := range n {
		if e := tree.Add(i, 1); e != nil || tree.Visited() > bound {
			t.Fatalf("Add(%d) e=%v visited=%d", i, e, tree.Visited())
		}
		if _, e := tree.PrefixSum(i); e != nil || tree.Visited() > bound {
			t.Fatalf("PrefixSum(%d) e=%v visited=%d", i, e, tree.Visited())
		}
	}
}

func TestErrorsAndBounds(t *testing.T) {
	empty, _ := bit.New(0)
	three, _ := bit.New(3)
	_, negErr := bit.New(-1)
	for _, c := range []struct {
		name string
		err  error
		want error
	}{
		{"new-neg", negErr, bit.ErrBadSize},
		{"add-empty", empty.Add(0, 1), bit.ErrBadIndex},
		{"add-neg", empty.Add(-1, 1), bit.ErrBadIndex},
		{"ps-empty", mustErr(empty.PrefixSum(0)), bit.ErrBadIndex},
		{"ps-neg2", mustErr(empty.PrefixSum(-2)), bit.ErrBadIndex},
		{"ps-neg1", mustErr(empty.PrefixSum(-1)), nil},
		{"rs-empty", mustErr(empty.RangeSum(0, 0)), bit.ErrBadIndex},
		{"rs-swapped", mustErr(three.RangeSum(2, 1)), bit.ErrBadRange},
	} {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: want %v", c.name, c.want)
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	tree, _ := bit.New(100)
	for i := range 100 {
		tree.Add(i, 1)
	}
	const readers = 16
	start, results := make(chan struct{}), make(chan int64, readers)
	for range readers {
		go func() {
			<-start
			v, e := tree.PrefixSum(99)
			if e != nil {
				t.Error(e)
			}
			results <- v
		}()
	}
	close(start)
	for range readers {
		if v := <-results; v != 100 {
			t.Fatalf("got %d want 100", v)
		}
	}
}
