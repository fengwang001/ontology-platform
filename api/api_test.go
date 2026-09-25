package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
)

type opf = func(*api.Service) error

func feed(t *testing.T, s *api.Service, o opf) {
	t.Helper()
	if e := o(s); e != nil {
		t.Fatal(e)
	}
}
func D(a, v int64) opf { return func(s *api.Service) error { return s.Feed(api.Data{Seq: a, Val: v}) } }
func M(u int64) opf    { return func(s *api.Service) error { return s.Feed(api.Watermark{UpTo: u}) } }
func H() opf           { return func(s *api.Service) error { return s.Feed(api.Heartbeat{}) } }
func T(x int64) opf    { return func(s *api.Service) error { return s.Tick(x) } }

var eightops = []opf{D(1, 10), M(4), D(2, 20), D(3, 30), D(4, 40), T(5), T(6), H()}

func mustDistinct(t *testing.T, es ...error) {
	seen := map[error]bool{}
	for _, e := range es {
		if seen[e] {
			t.Fatal("sentinel errors must be pairwise distinct")
		}
		seen[e] = true
	}
}
func TestViewEqualsAppliedSum(t *testing.T) {
	for _, vals := range [][]int64{nil, {7}, {10, 20, 30, 40}, {-5, 5, -100}} {
		s, _ := api.New(5)
		var sum int64
		for i, v := range vals {
			feed(t, s, D(int64(i+1), v))
			sum += v
		}
		if s.View() != sum || s.Applied() != int64(len(vals)) {
			t.Fatalf("vals=%v view=%d sum=%d applied=%d", vals, s.View(), sum, s.Applied())
		}
	}
}
func TestEightStepTable(t *testing.T) {
	s, _ := api.New(5)
	a := []int64{1, 1, 2, 3, 4, 4, 4, 4}
	w := []int64{0, 4, 4, 4, 4, 4, 4, 4}
	st := []bool{false, true, true, true, false, false, true, false}
	for i, o := range eightops {
		feed(t, s, o)
		if s.Applied() != a[i] || s.Watermark() != w[i] || s.Stale() != st[i] {
			t.Fatalf("step %d: A=%d W=%d stale=%t want %d/%d/%t", i+1, s.Applied(), s.Watermark(), s.Stale(), a[i], w[i], st[i])
		}
	}
}
func TestBoundaryBooleans(t *testing.T) {
	s, _ := api.New(5)
	for i := 0; i < 5; i++ {
		feed(t, s, eightops[i])
	}
	if s.Stale() || !(s.Applied() <= s.Watermark()) { // 甲: correct false, wrong <= true
		t.Fatal("甲 boundary wrong")
	}
	feed(t, s, eightops[5]) // 乙: diff==timeout, correct false, wrong >= true
	if s.Stale() {
		t.Fatal("乙 boundary wrong")
	}
	feed(t, s, eightops[6]) // 丙: 6>timeout, correct true, wrong lag-only false
	if !s.Stale() {
		t.Fatal("丙 boundary wrong")
	}
}
func TestMonotonicNonDecreasing(t *testing.T) {
	s, _ := api.New(5)
	pw := s.Watermark()
	for i, o := range []opf{M(4), M(4), T(5), T(5), H(), H(), M(9), T(9)} {
		feed(t, s, o)
		if s.Watermark() < pw {
			t.Fatalf("step %d: W regressed", i)
		}
		pw = s.Watermark()
	}
	if !s.Stale() {
		t.Fatal("W=9 > A=0 must be stale")
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	s, _ := api.New(5)
	feed(t, s, D(1, 10))
	feed(t, s, M(1))
	mustDistinct(t, api.ErrDataGap, api.ErrWatermarkBacktrack, api.ErrTickBacktrack, api.ErrInvalidTimeout)
	ws := []error{api.ErrDataGap, api.ErrWatermarkBacktrack, api.ErrTickBacktrack}
	fs := []opf{D(9, 1), M(0), T(-1)}
	snap := func() [3]int64 { return [3]int64{s.Applied(), s.Watermark(), s.View()} }
	for i := range ws {
		b := snap()
		if !errors.Is(fs[i](s), ws[i]) || snap() != b {
			t.Fatalf("rejection %v left a trace or wrong error", ws[i])
		}
	}
	if _, e := api.New(0); !errors.Is(e, api.ErrInvalidTimeout) {
		t.Fatal("timeout<=0 must return ErrInvalidTimeout")
	}
	feed(t, s, D(2, 20))
	if s.View() != 30 {
		t.Fatal("service must stay usable after rejections")
	}
}
func TestConcurrentReadOnly(t *testing.T) {
	s, _ := api.New(5)
	for i := 1; i <= 4; i++ {
		feed(t, s, D(int64(i), int64(i)))
	}
	feed(t, s, M(4))
	const N = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	got := make([][3]int64, N)
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func(g int) {
			defer wg.Done()
			<-start
			z := int64(0)
			if s.Stale() {
				z = 1
			}
			for k := 0; k < 128; k++ {
				got[g] = [3]int64{z, s.Applied(), s.View()}
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < N; g++ {
		if got[g] != got[0] {
			t.Fatalf("reader %d saw %v want %v", g, got[g], got[0])
		}
	}
}
func TestSelfCheck(t *testing.T) {
	s, _ := api.New(5)
	if !s.SelfCheck() {
		t.Fatal("built-in SelfCheck must pass")
	}
}
