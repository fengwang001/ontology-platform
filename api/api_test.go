package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

// The eight-step sequence derived in NOTES.md: reads must be 5/8/15/22.
func TestEightSteps(t *testing.T) {
	s := api.New()
	ops := []struct { // write=true 是写；否则按 g 读（g=="" 读 Total），want 为期望读值
		write   bool
		g, k    string
		v, want int64
	}{
		{true, "g0", "a", 5, 0}, {false, "g0", "", 0, 5},
		{true, "g0", "b", 3, 0}, {false, "g0", "", 0, 8},
		{true, "g1", "c", 7, 0}, {false, "", "", 0, 15},
		{true, "g0", "b", 10, 0}, {false, "", "", 0, 22},
	}
	for i, op := range ops {
		if op.write {
			if err := s.Write(op.g, op.k, op.v); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got := s.ReadTotal()
		if op.g != "" {
			got, _ = s.ReadG(op.g)
		}
		if got != op.want {
			t.Fatalf("step %d: read = %d, want %d", i+1, got, op.want)
		}
	}
}

// Invariant 1: View equals batch recompute, over random write sequences.
func TestViewMatchesBatch(t *testing.T) {
	for _, tc := range []struct{ seed, n, keys int }{{1, 200, 20}, {7, 2000, 100}, {42, 5000, 300}} {
		t.Run(fmt.Sprintf("seed%d-n%d", tc.seed, tc.n), func(t *testing.T) {
			s := api.New()
			rng := rand.New(rand.NewSource(int64(tc.seed)))
			bind := map[string]string{} // key -> group; val: key -> value; want: group -> sum
			val, want := map[string]int64{}, map[string]int64{}
			var sum int64
			for i := 0; i < tc.n; i++ {
				k := fmt.Sprintf("k%d", rng.Intn(tc.keys))
				g, ok := bind[k]
				if !ok {
					g = fmt.Sprintf("g%d", rng.Intn(5))
					bind[k] = g
				}
				v := int64(rng.Intn(2001) - 1000)
				if err := s.Write(g, k, v); err != nil {
					t.Fatal(err)
				}
				want[g] += v - val[k]
				sum += v - val[k]
				val[k] = v
			}
			groups, total := s.View()
			if !maps.Equal(groups, want) || total != sum {
				t.Fatalf("View = %v/%d, want %v/%d", groups, total, want, sum)
			}
		})
	}
}

// Invariant 4: rejected ops are distinguishable and leave no trace.
func TestRejectedOpsLeaveState(t *testing.T) {
	cases := []struct {
		name, g, k string
		wantErr    error
	}{
		{"empty group", "", "k1", api.ErrEmptyGroup},
		{"empty key", "g9", "", api.ErrEmptyKey},
		{"key conflict", "g1", "a", api.ErrKeyConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := api.New()
			if err := s.Write("g0", "a", 5); err != nil {
				t.Fatal(err)
			}
			before, totBefore := s.View()
			if err := s.Write(tc.g, tc.k, 99); !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			after, totAfter := s.View()
			if !maps.Equal(before, after) || totBefore != totAfter {
				t.Fatal("rejected write changed state")
			}
			if err := s.Write("g0", "a", 6); err != nil { // still usable
				t.Fatal(err)
			}
		})
	}
	if api.ErrEmptyGroup == api.ErrEmptyKey || api.ErrEmptyKey == api.ErrKeyConflict || api.ErrEmptyGroup == api.ErrKeyConflict {
		t.Fatal("sentinel errors must be distinct")
	}
}

// Concurrent readers (plus value-preserving rewrites that only bump
// versions) must all observe the identical, batch-consistent View.
func TestConcurrentViewConsistent(t *testing.T) {
	s := api.New()
	want := map[string]int64{}
	var wantTotal int64
	for i := 0; i < 300; i++ {
		g, v := fmt.Sprintf("g%d", i%6), int64(i*7-900)
		if err := s.Write(g, fmt.Sprintf("k%d", i), v); err != nil {
			t.Fatal(err)
		}
		want[g] += v
		wantTotal += v
	}
	const n = 32
	views, tots := make([]map[string]int64, n), make([]int64, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%4 == 0 { // rewrite same value: versions move, sums must not
				_ = s.Write("g0", "k0", -900)
			}
			views[i], tots[i] = s.View()
			_ = s.ReadTotal()
			if err := s.SelfCheck(); err != nil {
				t.Error(err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 0; i < n; i++ {
		if !maps.Equal(views[i], want) || tots[i] != wantTotal {
			t.Fatalf("goroutine %d: View = %v/%d, want %v/%d", i, views[i], tots[i], want, wantTotal)
		}
	}
}
