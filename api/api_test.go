package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func rec(p int, o int64, k string, v int64) api.Rec {
	return api.Rec{Partition: p, Offset: o, Key: k, Val: v}
}

func TestSection3(t *testing.T) {
	s := api.New(4)
	batches := [][]api.Rec{
		{rec(0, 0, "a", 1), rec(0, 1, "a", 2), rec(1, 0, "b", 5)},
		{rec(1, 1, "b", 3), rec(0, 1, "a", 2), rec(0, 3, "a", 4)},
		{rec(1, 1, "b", 3), rec(0, 3, "a", 4), rec(1, 2, "a", 6), rec(0, 4, "b", 8)},
	}
	for i, b := range batches {
		if i == 2 {
			s.Restart()
		}
		if err := s.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	tab := s.Table()
	got := []int64{tab["a"], tab["b"], s.Watermark(0), s.Watermark(1), s.Duplicates()}
	if want := []int64{13, 16, 4, 2, 3}; !slices.Equal(got, want) {
		t.Fatalf("a,b,w0,w1,dup = %v, want %v", got, want)
	}
}

func TestRejectedBatchNoTrace(t *testing.T) {
	s := api.New(2)
	if err := s.Write([]api.Rec{rec(0, 0, "a", 1)}); err != nil {
		t.Fatal(err)
	}
	distinct := map[error]bool{api.ErrInvalidRecord: true,
		api.ErrOutOfOrder: true, api.ErrTooManyPartitions: true}
	if len(distinct) != 3 {
		t.Fatal("sentinel errors must be distinct")
	}
	cases := []struct {
		name string
		b    []api.Rec
		want error
	}{
		{"bad-partition", []api.Rec{rec(-1, 0, "a", 1)}, api.ErrInvalidRecord},
		{"bad-key", []api.Rec{rec(0, 1, "", 1)}, api.ErrInvalidRecord},
		{"out-of-order", []api.Rec{rec(0, 2, "a", 1), rec(0, 2, "a", 1)}, api.ErrOutOfOrder},
		{"too-many", []api.Rec{rec(1, 0, "a", 1), rec(2, 0, "a", 1)}, api.ErrTooManyPartitions},
		{"precedence", []api.Rec{rec(0, 2, "a", 1), rec(0, 1, "", 1)}, api.ErrInvalidRecord},
	}
	for _, c := range cases {
		if err := s.Write(c.b); !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if s.Table()["a"] != 1 || s.Watermark(0) != 0 || s.Duplicates() != 0 ||
		s.Write([]api.Rec{rec(0, 1, "a", 2)}) != nil {
		t.Fatal("rejected batch left trace or sink unusable")
	}
}

// TestNaiveReference: random resend segments + restarts vs naive sum.
func TestNaiveReference(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		rng := rand.New(rand.NewSource(seed))
		s := api.New(8)
		logs := make([][]api.Rec, 4)
		for p := range logs {
			for i := int64(0); i < 25; i++ {
				logs[p] = append(logs[p], rec(p, i*2, fmt.Sprintf("k%d", (p+int(i))%3), i+1))
			}
		}
		naive := map[string]int64{}
		seen := map[[2]int64]bool{}
		applied := make([]int, 4)
		for b := 0; b < 50; b++ {
			var batch []api.Rec
			for p := range logs {
				if rng.Intn(2) == 0 {
					continue
				}
				lo := rng.Intn(applied[p] + 1)
				hi := lo + rng.Intn(len(logs[p])-lo+1)
				for _, r := range logs[p][lo:hi] {
					batch = append(batch, r)
					if k := [2]int64{int64(p), r.Offset}; !seen[k] {
						seen[k] = true
						naive[r.Key] += r.Val
					}
				}
				applied[p] = max(applied[p], hi)
			}
			if err := s.Write(batch); err != nil {
				t.Fatal(err)
			}
			if rng.Intn(3) == 0 {
				s.Restart()
			}
		}
		if tab := s.Table(); !maps.Equal(tab, naive) {
			t.Fatalf("seed %d: table=%v, want %v", seed, tab, naive)
		}
	}
}

// TestConcurrentDuplicateWrites: N goroutines, same batch, one takes effect.
func TestConcurrentDuplicateWrites(t *testing.T) {
	const n = 8
	batch := []api.Rec{rec(0, 0, "a", 1), rec(0, 1, "a", 2), rec(1, 0, "b", 5)}
	s := api.New(4)
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = s.Write(batch)
		}()
	}
	close(start)
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if tab := s.Table(); tab["a"] != 3 || tab["b"] != 5 {
		t.Fatalf("table=%v, want a=3 b=5", tab)
	}
	if want := int64((n - 1) * len(batch)); s.Duplicates() != want {
		t.Fatalf("dups=%d, want %d", s.Duplicates(), want)
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New(4).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
