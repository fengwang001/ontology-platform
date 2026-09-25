package compact

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync"
	"testing"

	"ontology/rec"
)

// TestFoldComparisonsBounded is a white-box test of the unexported cmps
// counter: after m other keys each contribute one record, folding one
// key with two in-window records must cost a small constant number of
// comparisons independent of m. Keys are fed in random order.
func TestFoldComparisonsBounded(t *testing.T) {
	cases := []struct {
		m     int
		bound int
	}{
		{100, 2}, {1000, 2}, {10000, 2},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("m=%d", tc.m), func(t *testing.T) {
			c := New(0)
			rs := make([]rec.Rec, 0, tc.m+1)
			for i := 0; i < tc.m; i++ {
				rs = append(rs, rec.Rec{Key: fmt.Sprintf("k%05d", i), TS: int64(i + 1)})
			}
			rs = append(rs, rec.Rec{Key: "k00000", Value: 7, TS: int64(tc.m + 1)})
			rng := rand.New(rand.NewPCG(uint64(tc.m), 42))
			rng.Shuffle(len(rs), func(i, j int) { rs[i], rs[j] = rs[j], rs[i] })
			c.Add(rs)

			out, err := c.Compact(0, int64(tc.m)+2)
			if err != nil {
				t.Fatalf("compact: %v", err)
			}
			var surv rec.Rec
			for _, r := range out {
				if r.Key == "k00000" {
					surv = r
				}
			}
			if surv.Value != 7 || surv.TS != int64(tc.m+1) {
				t.Fatalf("survivor = %+v, want {k00000 7 %d}", surv, tc.m+1)
			}
			if got := c.cmps["k00000"]; got > tc.bound {
				t.Fatalf("fold comparisons for k00000 = %d, want <= %d (m=%d)", got, tc.bound, tc.m)
			}
		})
	}
}

// TestConcurrentCompact runs Compact and SelfCheck from many goroutines
// on one instance (no sleeps); every result must be field-identical.
func TestConcurrentCompact(t *testing.T) {
	cases := []struct {
		name string
		n    int
	}{
		{"4", 4}, {"16", 16}, {"64", 64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(5)
			rs := []rec.Rec{
				{Key: "a", Value: 1, TS: 1}, {Key: "b", Value: 10, TS: 2},
				{Key: "a", Value: 2, TS: 4}, {Key: "b", Value: 0, TS: 5, Del: true},
				{Key: "c", Value: 0, TS: 7, Del: true}, {Key: "a", Value: 3, TS: 8},
			}
			c.Add(rs)
			want, err := c.Compact(0, 10)
			if err != nil {
				t.Fatalf("compact: %v", err)
			}
			res := make([][]rec.Rec, tc.n)
			var wg sync.WaitGroup
			for i := 0; i < tc.n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					res[i], _ = c.Compact(0, 10)
					_ = c.SelfCheck()
				}(i)
			}
			wg.Wait()
			for i := range res {
				if !reflect.DeepEqual(res[i], want) {
					t.Fatalf("goroutine %d = %v, want %v", i, res[i], want)
				}
			}
		})
	}
}
