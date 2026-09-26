package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/gk"
)

func TestStructure(t *testing.T) { // invariant 1: ascending tuples, Size == n
	for _, eps := range []float64{0.05, 0.25} {
		for _, m := range []int64{1, 7, 500} {
			s := gk.New(eps)
			for v := m; v > 0; v-- { // descending inserts
				s.Insert(v)
				if s.N()%3 == 0 {
					s.Compress()
				}
			}
			if s.N() != m {
				t.Fatalf("eps=%v m=%d: N=%d", eps, m, s.N())
			}
			ts := s.Tuples()
			for i := 1; i < len(ts); i++ {
				if ts[i-1].V >= ts[i].V {
					t.Fatalf("eps=%v m=%d: tuples not strictly ascending", eps, m)
				}
			}
		}
	}
	a, _ := api.New(0.2)
	for v := int64(0); v < 50; v++ {
		_ = a.Insert(v)
	}
	if a.Size() != 50 {
		t.Errorf("Size()=%d, want 50", a.Size())
	}
}
func TestBand(t *testing.T) { // invariant 2: g+Δ ≤ 2εn after insert and compress
	for _, eps := range []float64{0.05, 0.1, 0.25} {
		s := gk.New(eps)
		for v := int64(0); v < 400; v++ {
			s.Insert(v * 37 % 400) // gcd(37,400)=1: distinct
			if !s.BandOK() {
				t.Fatalf("eps=%v n=%d: band violated after insert", eps, s.N())
			}
			if s.N()%7 == 0 {
				s.Compress()
				if !s.BandOK() {
					t.Fatalf("eps=%v n=%d: band violated after compress", eps, s.N())
				}
			}
		}
	}
}
func TestNaiveConsistency(t *testing.T) { // invariant 3: Query vs exact reference
	for _, eps := range []float64{0.05, 0.1, 0.25} {
		for _, m := range []int{300, 1500} {
			a, _ := api.New(eps)
			for _, p := range rand.New(rand.NewSource(int64(m))).Perm(m) {
				_ = a.Insert(int64(p)) // shuffled 0..m-1: rank of v is v+1
			}
			for k := 1; k < 20; k++ {
				phi := float64(k) / 20
				got, _ := a.Query(phi)
				if d := float64(got+1) - phi*float64(m); d > eps*float64(m) || d < -eps*float64(m) {
					t.Errorf("eps=%v m=%d phi=%v: rank error %v exceeds eps*n", eps, m, phi, d)
				}
			}
		}
	}
}
func TestFaultInjection(t *testing.T) { // invariant 4: distinct errors, no trace
	for _, eps := range []float64{0, -0.5, 1, 1.5} {
		if _, err := api.New(eps); !errors.Is(err, api.ErrEpsilon) {
			t.Errorf("New(%v): want ErrEpsilon, got %v", eps, err)
		}
	}
	a, _ := api.New(0.1)
	for _, v := range []int64{10, 20, 30} {
		_ = a.Insert(v)
	}
	n0 := a.Size()
	q0, _ := a.Query(0.5)
	for _, v := range []int64{10, 20, 30} {
		if err := a.Insert(v); !errors.Is(err, api.ErrDuplicate) {
			t.Errorf("Insert(%d) dup: want ErrDuplicate, got %v", v, err)
		}
	}
	for _, phi := range []float64{0, -0.1, 1, 2} {
		if _, err := a.Query(phi); !errors.Is(err, api.ErrPhi) {
			t.Errorf("Query(%v): want ErrPhi, got %v", phi, err)
		}
	}
	e, _ := api.New(0.1)
	if _, err := e.Query(0.5); !errors.Is(err, api.ErrEmpty) {
		t.Errorf("empty query: want ErrEmpty, got %v", err)
	}
	sents := []error{api.ErrEpsilon, api.ErrDuplicate, api.ErrPhi, api.ErrEmpty}
	for i, x := range sents {
		for j, y := range sents {
			if (i == j) != errors.Is(x, y) {
				t.Errorf("sentinels %d,%d not distinct", i, j)
			}
		}
	}
	if q1, _ := a.Query(0.5); a.Size() != n0 || q0 != q1 {
		t.Error("rejected ops changed state")
	}
	if err := a.Insert(40); err != nil || a.Size() != n0+1 {
		t.Error("summary unusable after rejections")
	}
}
func TestConcurrentQuery(t *testing.T) { // identical results per φ, race-free
	a, _ := api.New(0.1)
	for v := int64(0); v < 2000; v++ {
		_ = a.Insert(v)
	}
	phis := []float64{0.1, 0.3, 0.5, 0.7, 0.9}
	want := make([]int64, len(phis))
	for i, p := range phis {
		want[i], _ = a.Query(p)
	}
	start := make(chan struct{})
	var bad atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i, p := range phis {
				if got, _ := a.Query(p); got != want[i] {
					bad.Add(1)
				}
			}
			_ = a.Size()
			_ = a.SelfCheck()
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d mismatched concurrent query results", bad.Load())
	}
}
