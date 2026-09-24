package det

import (
	"math/rand"
	"sort"
	"testing"
)

func eq(a, b []int64) bool {
	for i := range a {
		if i >= len(b) || a[i] != b[i] {
			return false
		}
	}
	return len(a) == len(b)
}

// TestNoEarlyGap walks steps 1-6 of the mandated sequence: early 6 must
// not make 5 a gap; after 9 only 5,7 are gaps (6 seen, 8 in window).
func TestNoEarlyGap(t *testing.T) {
	d := New(2)
	for _, s := range []int64{1, 2, 3, 4, 6} {
		if err := d.Feed(s); err != nil {
			t.Fatal(err)
		}
	}
	if d.h != 4 || len(d.Gaps()) != 0 || !eq(d.Seen(), []int64{6}) {
		t.Fatalf("after Feed(6): h=%d gaps=%v seen=%v", d.h, d.Gaps(), d.Seen())
	}
	if err := d.Feed(9); err != nil {
		t.Fatal(err)
	}
	if d.h != 7 || !eq(d.Gaps(), []int64{5, 7}) || !eq(d.Seen(), []int64{9}) {
		t.Fatalf("after Feed(9): h=%d gaps=%v seen=%v", d.h, d.Gaps(), d.Seen())
	}
}

// TestDuplicateIgnored walks steps 7-11: covered redeliveries are
// ignored; then 8 is judged and 9,10 join the prefix.
func TestDuplicateIgnored(t *testing.T) {
	d := New(2)
	for _, s := range []int64{1, 2, 3, 4, 6, 9, 2, 3, 6} {
		_ = d.Feed(s)
	}
	if d.h != 7 || !eq(d.Gaps(), []int64{5, 7}) || !eq(d.Seen(), []int64{9}) {
		t.Fatalf("duplicates changed state: h=%d gaps=%v seen=%v", d.h, d.Gaps(), d.Seen())
	}
	for _, s := range []int64{10, 11} {
		_ = d.Feed(s)
	}
	if d.h != 11 || !eq(d.Gaps(), []int64{5, 7, 8}) || len(d.Seen()) != 0 {
		t.Fatalf("after 10,11: h=%d gaps=%v seen=%v", d.h, d.Gaps(), d.Seen())
	}
}

// TestNaiveEquivalence replays generated streams against an independent
// full-set reference. Equal (H,gaps,seen) after every event is exactly
// invariant 1: each number in [1,max] belongs to exactly one of the
// contiguous prefix, the gap set or the in-flight set.
func TestNaiveEquivalence(t *testing.T) {
	for _, w := range []int64{1, 2, 3, 5} {
		for seed := int64(0); seed < 8; seed++ {
			d, arr := New(w), map[int64]struct{}{}
			var nh int64
			var ng []int64
			for _, s := range mixedStream(seed) {
				if err := d.Feed(s); err != nil {
					t.Fatal(err)
				}
				naiveStep(arr, &nh, &ng, s, w)
				if d.h != nh || !eq(d.Gaps(), ng) || !eq(d.Seen(), refSeen(arr, nh)) {
					t.Fatalf("w=%d seed=%d seq=%d: got (%d,%v,%v) ref (%d,%v,%v)",
						w, seed, s, d.h, d.Gaps(), d.Seen(), nh, ng, refSeen(arr, nh))
				}
			}
		}
	}
}

// TestConvergenceBounded proves Feed after m in-order events is O(1):
// the unexported counter stays below a constant independent of m.
func TestConvergenceBounded(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		d := New(2)
		for s := int64(1); s <= m; s++ {
			_ = d.Feed(s)
		}
		if err := d.Feed(m + 1); err != nil {
			t.Fatal(err)
		}
		if d.steps > 4 || d.h != m+1 || len(d.gaps) != 0 {
			t.Fatalf("m=%d: steps=%d h=%d gaps=%d", m, d.steps, d.h, len(d.gaps))
		}
	}
}

// --- independent naive reference and stream generator ---

func naiveStep(arr map[int64]struct{}, nh *int64, ng *[]int64, s, w int64) {
	if s <= *nh {
		return
	}
	arr[s] = struct{}{}
	for {
		next := *nh + 1
		if _, ok := arr[next]; ok {
			*nh++
			continue
		}
		var mx int64
		for k := range arr {
			if k > *nh && k > mx {
				mx = k
			}
		}
		if mx-next >= w {
			*ng = append(*ng, next)
			*nh++
			continue
		}
		return
	}
}

func refSeen(arr map[int64]struct{}, h int64) []int64 {
	out := []int64{}
	for k := range arr {
		if k > h {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func mixedStream(seed int64) []int64 {
	r := rand.New(rand.NewSource(seed + 1))
	var evs []int64
	for _, i := range r.Perm(24) {
		s := int64(i) + 1
		if s%7 == 0 { // never delivered -> eventual gap
			continue
		}
		evs = append(evs, s)
		if s <= 6 && seed%2 == 0 { // redelivery of covered numbers
			evs = append(evs, s)
		}
	}
	return evs
}
