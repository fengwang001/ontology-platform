package wsampler

import (
	"errors"
	"math"
	"reflect"
	"sort"
	"testing"
)

func detRNG(seed uint64) func(int) float64 {
	return func(i int) float64 {
		x := uint64(i)*2654435761 + seed*1000003
		x = (x ^ (x >> 17)) * 0x9E3779B97F4A7C15
		return float64(x%999983+1) / 999984
	}
}

func valAt(i int) string { return string(rune('a'+i%26)) + string(rune('a'+i/26%26)) }

func gotVals(s *Sampler) []string {
	out := []string{}
	for _, q := range s.Sample() {
		out = append(out, q.Val)
	}
	return out
}

// TestInvariants pins invariants 1 (size==min(k,N)) and 2 (first k all kept).
func TestInvariants(t *testing.T) {
	for _, c := range [][2]int{{1, 1}, {2, 2}, {3, 5}, {5, 3}, {10, 50}, {1, 100}, {7, 7}} {
		k, n := c[0], c[1]
		s, _ := New(k, detRNG(1))
		items := make([]Input, n)
		for i := range items {
			items[i] = Input{Val: valAt(i), Weight: 1 + float64(i%5)}
		}
		if err := s.Feed(items); err != nil {
			t.Fatal(err)
		}
		if s.Size() != min(k, n) || s.Seen() != n || len(gotVals(s)) != min(k, n) {
			t.Fatalf("k=%d n=%d size=%d seen=%d", k, n, s.Size(), s.Seen())
		}
	}
}

// offlineTop is the naive reference: collect all, key=U^(1/w), take top k.
func offlineTop(us []float64, items []Input, k int) []string {
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		xa := math.Pow(us[idx[a]], 1/items[idx[a]].Weight)
		xb := math.Pow(us[idx[b]], 1/items[idx[b]].Weight)
		return xa > xb || (xa == xb && items[idx[a]].Val < items[idx[b]].Val)
	})
	out := []string{}
	for _, j := range idx[:min(k, len(idx))] {
		out = append(out, items[j].Val)
	}
	return out
}

// TestOfflineReference pins invariant 3 across sizes and deterministic seeds.
func TestOfflineReference(t *testing.T) {
	for _, c := range []struct {
		k, n int
		seed uint64
	}{
		{2, 5, 1}, {3, 20, 2}, {5, 100, 3}, {10, 500, 4}, {1, 50, 5}, {4, 4, 6},
	} {
		rng := detRNG(c.seed)
		s, _ := New(c.k, rng)
		items := make([]Input, c.n)
		us := make([]float64, c.n)
		for i := range items {
			items[i] = Input{Val: valAt(i), Weight: 1 + float64((i*3)%7)}
			us[i] = rng(i + 1)
		}
		if err := s.Feed(items); err != nil {
			t.Fatal(err)
		}
		if want := offlineTop(us, items, c.k); !reflect.DeepEqual(gotVals(s), want) {
			t.Errorf("k=%d n=%d seed=%d: online=%v offline=%v", c.k, c.n, c.seed, gotVals(s), want)
		}
	}
}

// TestRetainedCounterOK is white-box: the unexported counter n must equal
// min(k,seen) after every offer and stay exactly k for m in 100..10000,
// proving retained memory is O(k) rather than O(N).
func TestRetainedCounterOK(t *testing.T) {
	const k = 5
	for _, m := range []int{100, 1000, 10000} {
		s, _ := New(k, detRNG(7))
		for i := 1; i <= m; i++ {
			it := Input{Val: string(rune('a' + i%26)), Weight: 1 + float64(i%4)}
			if err := s.Feed([]Input{it}); err != nil || s.n != min(k, i) {
				t.Fatalf("m=%d i=%d n=%d err=%v", m, i, s.n, err)
			}
		}
	}
}

// TestRejectionAtomic pins invariant 4: the four errors are distinct
// sentinels, any rejected batch changes nothing, and use can continue.
func TestRejectionAtomic(t *testing.T) {
	if _, err := New(0, detRNG(1)); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("k<=0: %v", err)
	} else if _, err := New(2, nil); !errors.Is(err, ErrNilRNG) {
		t.Fatalf("nil rng: %v", err)
	}
	s, _ := New(2, func(int) float64 { return 0.5 })
	if err := s.Feed([]Input{{Val: "a", Weight: 1}, {Val: "b", Weight: 1}}); err != nil {
		t.Fatal(err)
	}
	want := gotVals(s)
	for _, items := range [][]Input{
		{{Val: "c", Weight: 1}, {Val: "", Weight: 1}},
		{{Val: "c", Weight: 1}, {Val: "d", Weight: 0}},
		{{Val: "c", Weight: 1}, {Val: "d", Weight: -2}},
	} {
		if err := s.Feed(items); !errors.Is(err, ErrInvalidItem) || s.Seen() != 2 ||
			!reflect.DeepEqual(gotVals(s), want) {
			t.Fatalf("bad item rejected atomically? err=%v state=%v", err, gotVals(s))
		}
	}
	// Both open-interval boundaries (0 and 1) are rejected, atomically.
	for _, bad := range []float64{0, 1} {
		s2, _ := New(2, func(i int) float64 {
			if i == 2 {
				return bad
			}
			return 0.5
		})
		_ = s2.Feed([]Input{{Val: "a", Weight: 1}})
		err := s2.Feed([]Input{{Val: "b", Weight: 1}})
		if !errors.Is(err, ErrUniformOutOfRange) || s2.Seen() != 1 ||
			!reflect.DeepEqual(gotVals(s2), []string{"a"}) {
			t.Fatalf("U=%v: err=%v state=%v", bad, err, gotVals(s2))
		}
	}
	if len(map[error]int{ErrInvalidK: 0, ErrNilRNG: 1, ErrUniformOutOfRange: 2, ErrInvalidItem: 3}) != 4 {
		t.Fatal("sentinel errors are not all distinct")
	}
	if err := s.Feed([]Input{{Val: "e", Weight: 100}}); err != nil || s.Seen() != 3 || s.Size() != 2 {
		t.Fatalf("use after reject: err=%v seen=%d size=%d", err, s.Seen(), s.Size())
	}
}
