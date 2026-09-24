package sketch

import (
	"slices"
	"sync"
	"testing"
)

func feed(t *testing.T, s *Sketch, seq map[string]uint64) {
	for k, n := range seq {
		if err := s.Add(k, n); err != nil {
			t.Fatalf("Add(%q,%d): %v", k, n, err)
		}
	}
}

func TestNeverUnderestimate(t *testing.T) {
	cases := []struct {
		w, d int
		seq  map[string]uint64
	}{
		{1, 1, map[string]uint64{"a": 3, "b": 2}},
		{4, 2, map[string]uint64{"x": 5, "y": 1, "z": 7}},
		{64, 5, map[string]uint64{"p": 100, "q": 1, "r": 9}},
	}
	for _, tc := range cases {
		s, _ := New(tc.w, tc.d, 0)
		feed(t, s, tc.seq)
		var sum uint64
		for k, want := range tc.seq {
			sum += want
			if got, _ := s.Estimate(k); got < want {
				t.Errorf("w=%d d=%d: Estimate(%q)=%d < true=%d", tc.w, tc.d, k, got, want)
			}
		}
		for r := 0; r < tc.d; r++ {
			if s.RowSum(r) != sum {
				t.Errorf("row %d sums to %d, want %d", r, s.RowSum(r), sum)
			}
		}
		if s.Total() != sum {
			t.Errorf("w=%d d=%d: Total()=%d, want %d", tc.w, tc.d, s.Total(), sum)
		}
	}
}

func TestDeterminismAndMerge(t *testing.T) {
	cases := []struct {
		w, d       int
		seqA, seqB map[string]uint64
	}{
		{8, 3, map[string]uint64{"a": 2, "b": 1}, map[string]uint64{"b": 3, "c": 1}},
		{1, 1, map[string]uint64{"a": 5}, map[string]uint64{"a": 1, "b": 2}},
		{33, 4, map[string]uint64{"k": 7}, map[string]uint64{"k": 2, "j": 4}},
	}
	for _, tc := range cases {
		build := func() *Sketch { s, _ := New(tc.w, tc.d, 0); return s }
		a1, a2, b, joint := build(), build(), build(), build()
		feed(t, a1, tc.seqA)
		feed(t, a2, tc.seqA)
		feed(t, b, tc.seqB)
		feed(t, joint, tc.seqA)
		feed(t, joint, tc.seqB)
		if !a1.Equal(a2) {
			t.Errorf("w=%d d=%d: two identical builds differ", tc.w, tc.d)
		}
		if err := a1.Merge(b); err != nil {
			t.Fatal(err)
		}
		if !a1.Equal(joint) { // Equal compares every cell
			t.Errorf("w=%d d=%d: merge != joint feed", tc.w, tc.d)
		}
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	dimCases := []struct{ w, d int }{{0, 1}, {1, 0}, {-2, 3}, {3, -2}}
	for _, tc := range dimCases {
		if _, err := New(tc.w, tc.d, 0); err != ErrInvalidDims {
			t.Errorf("New(%d,%d): err=%v, want ErrInvalidDims", tc.w, tc.d, err)
		}
	}
	s, _ := New(8, 3, 10)
	feed(t, s, map[string]uint64{"a": 4, "b": 2})
	before := s.Clone()
	ops := []struct {
		name string
		op   func() error
		want error
	}{
		{"empty key add", func() error { return s.Add("", 1) }, ErrEmptyKey},
		{"empty estimate", func() error { _, e := s.Estimate(""); return e }, ErrEmptyKey},
		{"overflow add", func() error { return s.Add("a", 5) }, ErrOverflow},
		{"incompatible merge", func() error { o, _ := New(9, 3, 0); return s.Merge(o) }, ErrIncompatible},
		{"incompatible depth", func() error { o, _ := New(8, 4, 0); return s.Merge(o) }, ErrIncompatible},
	}
	for _, tc := range ops {
		if err := tc.op(); err != tc.want {
			t.Errorf("%s: err=%v, want %v", tc.name, err, tc.want)
		}
		if !s.Equal(before) {
			t.Errorf("%s: sketch changed after rejection", tc.name)
		}
	}
	if err := s.Add("c", 4); err != nil || s.Total() != 10 {
		t.Errorf("sketch unusable after rejections: err=%v total=%d", err, s.Total())
	}
}

func TestAccessCounter(t *testing.T) {
	for _, w := range []int{1000, 100000} {
		s, _ := New(w, 4, 0)
		_ = s.Add("key", 1)
		if got := s.accessed.Load(); got != 4 {
			t.Errorf("w=%d: Add touched %d cells, want exactly d=4", w, got)
		}
		_, _ = s.Estimate("key")
		if got := s.accessed.Load(); got != 4 {
			t.Errorf("w=%d: Estimate touched %d cells, want exactly d=4", w, got)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	s, _ := New(64, 4, 0)
	feed(t, s, map[string]uint64{"a": 5, "b": 3, "c": 8})
	const g = 16 // goroutines released simultaneously via the start barrier
	var wg sync.WaitGroup
	start, results := make(chan struct{}), make([][]uint64, g)
	for i := range g {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var row []uint64
			for _, k := range []string{"a", "b", "c"} {
				est, _ := s.Estimate(k)
				row = append(row, est)
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
