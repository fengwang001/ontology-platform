package stats

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

func batch(xs []float64) (int64, float64, float64) {
	if len(xs) == 0 {
		return 0, 0, 0
	}
	var sum, m2 float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	for _, x := range xs {
		m2 += (x - mean) * (x - mean)
	}
	return int64(len(xs)), mean, m2
}
func eqf(a, b float64) bool {
	if math.Abs(b) < 1e-12 {
		return math.Abs(a-b) < 1e-9
	}
	return math.Abs(a-b)/math.Abs(b) < 1e-9
}
func replay(ref map[string][]float64, o Op) {
	switch o.Kind {
	case OpAdd:
		ref[o.Key] = append(ref[o.Key], o.Value)
	case OpRemove:
		x := ref[o.Key]
		for i, v := range x {
			if v == o.Value {
				ref[o.Key] = append(x[:i], x[i+1:]...)
				return
			}
		}
	case OpMerge:
		ref[o.Key] = append(ref[o.Key], ref[o.Other]...)
	}
}

func TestApplyMatchesBatch(t *testing.T) {
	ops := []Op{
		{OpAdd, "a", "", 1}, {OpAdd, "a", "", 2}, {OpAdd, "a", "", 2},
		{OpAdd, "b", "", 10}, {OpAdd, "b", "", 20},
		{OpRemove, "a", "", 2}, {OpMerge, "a", "b", 0},
	}
	s := NewStore()
	ref := map[string][]float64{}
	for _, o := range ops {
		if err := s.Apply(o); err != nil {
			t.Fatalf("apply %v: %v", o, err)
		}
		replay(ref, o)
	}
	for _, k := range []string{"a", "b"} {
		n, mean, m2 := batch(ref[k])
		v := s.View(k)
		if v.N != n || !eqf(v.Mean, mean) || !eqf(v.M2, m2) {
			t.Fatalf("%s: got (%d,%v,%v) want (%d,%v,%v)", k, v.N, v.Mean, v.M2, n, mean, m2)
		}
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	names := []string{"empty key", "absent value", "empty merge", "self merge"}
	bad := []Op{{OpAdd, "", "", 1}, {OpRemove, "G", "", 999}, {OpMerge, "G", "VOID", 0}, {OpMerge, "G", "G", 0}}
	wantErr := []error{ErrEmptyKey, ErrValueAbsent, ErrMergeEmpty, ErrMergeSelf}
	s := NewStore()
	for _, x := range []float64{1, 2, 3} {
		s.Apply(Op{OpAdd, "G", "", x})
	}
	before := s.View("G")
	seen := map[error]bool{}
	for i, op := range bad {
		t.Run(names[i], func(t *testing.T) {
			err := s.Apply(op)
			if !errors.Is(err, wantErr[i]) || seen[err] || s.View("G") != before {
				t.Fatalf("err=%v want=%v duplicate=%v changed=%v", err, wantErr[i], seen[err], s.View("G") != before)
			}
			seen[err] = true
		})
	}
	if err := s.Apply(Op{OpAdd, "G", "", 4}); err != nil {
		t.Fatalf("store unusable after rejects: %v", err)
	}
}

func TestRemoveProbesConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := NewStore()
		k := fmt.Sprintf("g%d", m)
		for i := 0; i < m; i++ {
			s.Apply(Op{OpAdd, k, "", float64(i)})
		}
		if err := s.Apply(Op{OpRemove, k, "", 42}); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if s.probes > 2 { // small constant, independent of m
			t.Fatalf("m=%d: probes=%d, expected O(1)", m, s.probes)
		}
		if err := s.Apply(Op{OpRemove, k, "", 42}); !errors.Is(err, ErrValueAbsent) {
			t.Fatalf("m=%d: second remove: %v", m, err)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	s := NewStore()
	for i := 0; i < 500; i++ {
		s.Apply(Op{OpAdd, "K", "", float64(i) * 0.5})
	}
	want := s.View("K")
	var wg sync.WaitGroup
	var bad atomic.Int64
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if s.View("K") != want {
					bad.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("%d readers saw divergent views", bad.Load())
	}
}

func TestSelfCheck(t *testing.T) {
	s := NewStore()
	s.Apply(Op{OpAdd, "mine", "", 9})
	before := s.View("mine")
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	if s.View("mine") != before {
		t.Fatal("SelfCheck mutated the receiver")
	}
}
