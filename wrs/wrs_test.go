package wrs_test

import (
	"math"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/wrs"
)

func TestKey(t *testing.T) {
	cases := []struct {
		u, w, want float64
	}{
		{0.9, 1, 0.9},
		{0.64, 2, 0.8},
		{0.343, 3, 0.7},
		{0.25, 2, 0.5},
		{0.5, 2, math.Sqrt(0.5)},
	}
	for _, c := range cases {
		if got := wrs.Key(c.u, c.w); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("Key(%v,%v)=%v want %v", c.u, c.w, got, c.want)
		}
	}
}

func TestOffer(t *testing.T) {
	type step struct {
		val        string
		key        float64
		retained   bool
		evicted    string
		wantSample []string // descending key after this step
	}
	cases := []struct {
		name  string
		k     int
		steps []step
	}{
		{
			"notes-five-steps", 2,
			[]step{
				{"A", 0.9, true, "", []string{"A"}},
				{"B", 0.8, true, "", []string{"A", "B"}},
				{"C", 0.75, false, "", []string{"A", "B"}},
				{"D", 0.7, false, "", []string{"A", "B"}},
				{"E", 0.1, false, "", []string{"A", "B"}},
			},
		},
		{
			"replaces-min-only", 2,
			[]step{
				{"A", 0.3, true, "", []string{"A"}},
				{"B", 0.4, true, "", []string{"B", "A"}},
				{"C", 0.5, true, "A", []string{"C", "B"}}, // evicts the min A
				{"D", 0.2, false, "", []string{"C", "B"}}, // below min: dropped
			},
		},
		{
			"equal-key-does-not-replace", 1,
			[]step{
				{"A", 0.5, true, "", []string{"A"}},
				{"B", 0.5, false, "", []string{"A"}}, // strictly greater required
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := wrs.New(tc.k)
			for i, st := range tc.steps {
				got, ev := r.Offer(st.val, 1, st.key)
				if got != st.retained || ev != st.evicted {
					t.Fatalf("step %d: retained=%v evicted=%q want %v,%q", i+1, got, ev, st.retained, st.evicted)
				}
				var samp []string
				for _, sl := range r.Snapshot() {
					samp = append(samp, sl.Val)
				}
				if !reflect.DeepEqual(samp, st.wantSample) {
					t.Fatalf("step %d: sample=%v want %v", i+1, samp, st.wantSample)
				}
			}
		})
	}
}

// TestConcurrentReaders floods one sampler through the public API, then has
// many goroutines read Sample/Size and run SelfCheck concurrently. No sleeps
// are used to manufacture timing; -race must stay clean and every Sample must
// be element-wise identical.
func TestConcurrentReaders(t *testing.T) {
	const k, m, readers = 8, 200, 32
	rng := func(i int) float64 { return float64(uint64(i)*2654435761%999983+1) / 999984 }
	s, err := api.New(k, rng)
	if err != nil {
		t.Fatal(err)
	}
	batch := make([]api.Item, m)
	for i := range batch {
		batch[i] = api.Item{Val: string(rune('a'+i%26)) + string(rune('a'+i/26)), Weight: float64(1 + i%7)}
	}
	if err := s.Feed(batch); err != nil {
		t.Fatal(err)
	}
	want := s.Sample()

	var wg sync.WaitGroup
	results := make([][]api.Item, readers)
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			if err := s.SelfCheck(); err != nil {
				t.Errorf("SelfCheck: %v", err)
			}
			if s.Size() != k {
				t.Errorf("Size=%d want %d", s.Size(), k)
			}
			results[g] = s.Sample()
		}(g)
	}
	wg.Wait()
	for g, got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("reader %d sample=%v want %v", g, got, want)
		}
	}
}
