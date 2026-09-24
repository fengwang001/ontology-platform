package stream_test

import (
	"slices"
	"sync"
	"testing"

	"ontology/bound"
	"ontology/sketch"
	"ontology/stream"
)

func TestBoundParams(t *testing.T) {
	valid := []struct {
		eps, delta float64
		w, d       int
	}{
		{0.1, 0.1, 28, 3},
		{0.01, 0.01, 272, 5},
		{0.5, 0.5, 6, 1},
	}
	for _, tc := range valid {
		w, d, err := bound.Params(tc.eps, tc.delta)
		if err != nil || w != tc.w || d != tc.d {
			t.Errorf("Params(%v,%v)=(%d,%d,%v), want (%d,%d,nil)", tc.eps, tc.delta, w, d, err, tc.w, tc.d)
		}
		if bound.Epsilon(w) > tc.eps || bound.Delta(d) > tc.delta {
			t.Errorf("(%d,%d) does not meet (%v,%v)", w, d, tc.eps, tc.delta)
		}
	}
	invalid := []struct{ eps, delta float64 }{{0, 0.5}, {1, 0.5}, {-0.1, 0.5}, {0.5, 0}, {0.5, 1}, {0.5, -0.2}}
	for _, tc := range invalid {
		if _, _, err := bound.Params(tc.eps, tc.delta); err != bound.ErrInvalidProb {
			t.Errorf("Params(%v,%v): err=%v, want ErrInvalidProb", tc.eps, tc.delta, err)
		}
		if _, err := stream.New(tc.eps, tc.delta, 1, 0); err != bound.ErrInvalidProb {
			t.Errorf("stream.New(%v,%v): err=%v, want ErrInvalidProb", tc.eps, tc.delta, err)
		}
	}
}

func TestStreamErrorsAndSelfCheck(t *testing.T) {
	if _, err := stream.NewWithDims(0, 3, 1, 0); err != sketch.ErrInvalidDims {
		t.Errorf("NewWithDims(0,3): err=%v, want ErrInvalidDims", err)
	}
	st, err := stream.NewWithDims(16, 4, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SelfCheck(); err != nil {
		t.Fatalf("empty stream SelfCheck: %v", err)
	}
	if err := st.Add("", 1); err != sketch.ErrEmptyKey {
		t.Errorf("Add empty key: err=%v, want ErrEmptyKey", err)
	}
	if _, err := st.Estimate(""); err != sketch.ErrEmptyKey {
		t.Errorf("Estimate empty key: err=%v, want ErrEmptyKey", err)
	}
	counts := map[string]uint64{"a": 10, "b": 4, "c": 1}
	for k, n := range counts {
		if err := st.Add(k, n); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck after inserts: %v", err)
	}
	for k, n := range counts {
		if est, _ := st.Estimate(k); est < n {
			t.Errorf("Estimate(%q)=%d < true=%d", k, est, n)
		}
	}
	if hh := st.HeavyHitters(); !slices.Contains(hh, "a") || !slices.Contains(hh, "b") {
		t.Errorf("HeavyHitters=%v, want a and b included", hh)
	}
}

func TestStreamMerge(t *testing.T) {
	cases := []struct {
		seqA, seqB map[string]uint64
	}{
		{map[string]uint64{"a": 3}, map[string]uint64{"a": 1, "b": 2}},
		{map[string]uint64{"x": 5, "y": 1}, map[string]uint64{"z": 4}},
	}
	for _, tc := range cases {
		s1, _ := stream.NewWithDims(16, 4, 2, 0)
		s2, _ := stream.NewWithDims(16, 4, 2, 0)
		for k, n := range tc.seqA {
			_ = s1.Add(k, n)
		}
		for k, n := range tc.seqB {
			_ = s2.Add(k, n)
		}
		if err := s1.Merge(s2); err != nil {
			t.Fatal(err)
		}
		for k, n := range tc.seqB {
			if est, _ := s1.Estimate(k); est < n {
				t.Errorf("after merge Estimate(%q)=%d < %d", k, est, n)
			}
		}
		if err := s1.SelfCheck(); err != nil {
			t.Errorf("SelfCheck after merge: %v", err)
		}
	}
	bad, _ := stream.NewWithDims(16, 4, 9, 0)
	ok, _ := stream.NewWithDims(16, 4, 2, 0)
	if err := ok.Merge(bad); err != sketch.ErrIncompatible {
		t.Errorf("threshold mismatch merge: err=%v, want ErrIncompatible", err)
	}
}

func TestConcurrentQueriesIdentical(t *testing.T) {
	st, _ := stream.New(0.01, 0.01, 5, 0)
	counts := map[string]uint64{"hot1": 9, "hot2": 7, "cold": 1}
	for k, n := range counts {
		_ = st.Add(k, n)
	}
	const g = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([][]uint64, g)
	for i := range g {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			row := []uint64{uint64(len(st.HeavyHitters()))}
			for _, k := range []string{"hot1", "hot2", "cold"} {
				est, _ := st.Estimate(k)
				row = append(row, est)
			}
			if st.SelfCheck() != nil {
				row = append(row, 1)
			}
			results[i] = row
		}()
	}
	close(start)
	wg.Wait()
	for i := 1; i < g; i++ {
		if !slices.Equal(results[0], results[i]) {
			t.Fatalf("goroutine %d got %v, want %v", i, results[i], results[0])
		}
	}
}
