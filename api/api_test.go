package api

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

func streams() [][]int64 { // 多档规模、随机插入顺序
	r := rand.New(rand.NewSource(7))
	out := make([][]int64, 5)
	for i, n := range []int{1, 2, 50, 500, 2000} {
		out[i] = make([]int64, n)
		for j, x := range r.Perm(n) {
			out[i][j] = int64(x)
		}
	}
	return out
}

func build(t *testing.T, eps float64, vals []int64) *Summary {
	t.Helper()
	s, _ := New(eps)
	for _, v := range vals {
		if err := s.Insert(v); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestStructureInvariant(t *testing.T) {
	for _, vals := range streams() {
		s := build(t, 0.05, vals)
		if s.Size() != len(vals) {
			t.Fatalf("Size=%d, want %d", s.Size(), len(vals))
		}
		tp := s.s.Tuples()
		for i := 1; i < len(tp); i++ {
			if tp[i-1].V >= tp[i].V {
				t.Fatalf("tuples not strictly ascending at %d", i)
			}
		}
	}
}

func TestBandInvariant(t *testing.T) {
	for _, eps := range []float64{0.25, 0.1, 0.01} {
		for _, vals := range streams() {
			s, _ := New(eps)
			for _, v := range vals {
				_ = s.Insert(v) // vals 互不相同，不会报错
				if !s.s.BandOK() {
					t.Fatalf("eps=%v n=%d: band invariant violated", eps, s.Size())
				}
			}
		}
	}
}

func TestNaiveReference(t *testing.T) {
	for _, eps := range []float64{0.25, 0.1, 0.05} {
		for _, vals := range streams()[2:] { // 跳过 εn 太小的规模
			s := build(t, eps, vals)
			ref := append([]int64(nil), vals...)
			sort.Slice(ref, func(i, j int) bool { return ref[i] < ref[j] })
			for k := 1; k < 20; k++ {
				got, _ := s.Query(float64(k) / 20)
				rank := float64(sort.Search(len(ref), func(i int) bool { return ref[i] >= got }) + 1)
				// 给定规则（带宽 2εn、首个 rmax≥φn）的真实保证是 <2εn，见 NOTES.md
				if math.Abs(rank-float64(k)/20*float64(len(ref))) > 2*eps*float64(len(ref)) {
					t.Fatalf("eps=%v phi=%d/20: rank error too large", eps, k)
				}
			}
		}
	}
}

func TestRejectNoTrace(t *testing.T) {
	for _, e := range []float64{-0.2, 0, 1, 1.7} {
		if _, err := New(e); !errors.Is(err, ErrBadEpsilon) {
			t.Fatalf("eps=%v not rejected with ErrBadEpsilon", e)
		}
	}
	s := build(t, 0.1, []int64{5, 1, 9})
	n0, tp0 := s.Size(), s.s.Tuples()
	if err := s.Insert(5); !errors.Is(err, ErrDuplicate) {
		t.Fatal("duplicate insert not rejected")
	}
	for _, phi := range []float64{-0.5, 0, 1, 1.5} {
		if _, err := s.Query(phi); !errors.Is(err, ErrBadPhi) {
			t.Fatalf("phi=%v not rejected with ErrBadPhi", phi)
		}
	}
	empty, _ := New(0.1)
	if _, err := empty.Query(0.5); !errors.Is(err, ErrEmpty) {
		t.Fatal("empty query not rejected with ErrEmpty")
	}
	if s.Size() != n0 || !reflect.DeepEqual(tp0, s.s.Tuples()) {
		t.Fatal("rejected operations changed state")
	}
	errs := []error{ErrBadEpsilon, ErrDuplicate, ErrBadPhi, ErrEmpty}
	for i, x := range errs {
		for _, y := range errs[i+1:] {
			if errors.Is(x, y) {
				t.Fatal("sentinel errors not distinct")
			}
		}
	}
	if err := s.Insert(7); err != nil { // 被拒后仍可正常使用
		t.Fatal(err)
	}
	if _, err := s.Query(0.5); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentQuery(t *testing.T) {
	s := build(t, 0.02, streams()[4])
	phis := []float64{0.1, 0.3, 0.5, 0.7, 0.9}
	var err error
	want := make([]int64, len(phis))
	for i, p := range phis {
		if want[i], err = s.Query(p); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var bad int64
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, p := range phis {
				if v, e := s.Query(p); e != nil || v != want[i] || s.Size() != 2000 || s.SelfCheck() != nil {
					atomic.AddInt64(&bad, 1)
				}
			}
		}()
	}
	wg.Wait()
	if bad > 0 {
		t.Fatalf("%d concurrent results differ from single-threaded", bad)
	}
}
