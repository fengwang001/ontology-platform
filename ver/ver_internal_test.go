package ver

import (
	"errors"
	"testing"
)

// naiveLatest is the definition by brute force: max Ev, ties broken by max In.
func naiveLatest(vs []Version) (Version, bool) {
	b, ok := Version{}, false
	for _, v := range vs {
		if !ok || v.Ev > b.Ev || (v.Ev == b.Ev && v.In > b.In) {
			b, ok = v, true
		}
	}
	return b, ok
}

func naiveAtEvent(vs []Version, T int64) (Version, bool) {
	var sub []Version
	for _, v := range vs {
		if v.Ev <= T {
			sub = append(sub, v)
		}
	}
	return naiveLatest(sub)
}

func naiveAtIngest(vs []Version, T int64) (Version, bool) {
	b, ok := Version{}, false
	for _, v := range vs {
		if v.In <= T && (!ok || v.In > b.In) {
			b, ok = v, true
		}
	}
	return b, ok
}

// genVersions builds m versions with strictly increasing In and shuffled,
// duplicate-prone Ev (LCG): ties and out-of-order arrivals both appear.
func genVersions(m int) []Version {
	vs := make([]Version, m)
	seed := int64(2654435761)
	for i := range vs {
		seed = seed*6364136223846793005 + 1442695040888963407
		vs[i] = Version{Value: string(rune('A' + i%26)), Ev: int64(seed>>33)%31 - 15, In: int64(i + 1)}
	}
	return vs
}

func TestNaiveConsistency(t *testing.T) {
	for _, m := range []int{1, 2, 17, 100} {
		all := genVersions(m)
		h := New()
		for step, v := range all {
			h.Append(v)
			prefix := all[:step+1]
			le, e1 := h.LatestEvent()
			li, e2 := h.LatestIngest()
			nle, _ := naiveLatest(prefix)
			if e1 != nil || le != nle {
				t.Fatalf("m=%d step=%d LatestEvent=%v,%v want %v", m, step, le, e1, nle)
			}
			if e2 != nil || li != prefix[step] {
				t.Fatalf("m=%d step=%d LatestIngest=%v want %v", m, step, li, prefix[step])
			}
			cands := []int64{0, all[step].In + 1}
			for _, w := range prefix {
				cands = append(cands, w.Ev, w.In)
			}
			for _, c := range cands {
				got, gErr := h.AtEvent(c)
				want, wOK := naiveAtEvent(prefix, c)
				if (gErr == nil) != wOK || (wOK && got != want) {
					t.Fatalf("m=%d AtEvent(%d)=%v,%v want %v,%v", m, c, got, gErr, want, wOK)
				}
				g2, g2Err := h.AtIngest(c)
				w2, w2OK := naiveAtIngest(prefix, c)
				if (g2Err == nil) != w2OK || (w2OK && g2 != w2) {
					t.Fatalf("m=%d AtIngest(%d)=%v,%v want %v,%v", m, c, g2, g2Err, w2, w2OK)
				}
			}
		}
	}
}

func TestEventLatestMonotonic(t *testing.T) {
	if _, err := New().LatestEvent(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty history must yield ErrNotFound, got %v", err)
	}
	h := New()
	var prev int64
	for i, v := range genVersions(300) {
		h.Append(v)
		le, err := h.LatestEvent()
		if err != nil || (i > 0 && le.Ev < prev) {
			t.Fatalf("step %d LatestEvent Ev decreased: %v", i, err)
		}
		prev = le.Ev
	}
}

func TestLatestEventTieBreak(t *testing.T) {
	cases := []struct {
		name string
		vs   []Version
		want string
	}{
		{"equal Ev later In wins", []Version{{"X", 10, 1}, {"Y", 10, 2}}, "Y"},
		{"low Ev after tie keeps pointer", []Version{{"X", 10, 1}, {"Y", 10, 2}, {"C", 1, 3}}, "Y"},
	}
	for _, tc := range cases {
		h := New()
		for _, v := range tc.vs {
			h.Append(v)
		}
		got, err := h.LatestEvent()
		if err != nil || got.Value != tc.want {
			t.Fatalf("%s: got %q,%v want %q", tc.name, got.Value, err, tc.want)
		}
	}
}

// TestLatestEventO1 reads the unexported counter directly (same-package test).
func TestLatestEventO1(t *testing.T) {
	firstN := int64(-1)
	for _, m := range []int{100, 1000, 10000} {
		h := New()
		for _, v := range genVersions(m) {
			h.Append(v)
		}
		got, err := h.LatestEvent()
		if err != nil {
			t.Fatal(err)
		}
		n := h.scanN.Load()
		if n > 2 {
			t.Fatalf("m=%d inspected %d versions, want <= 2", m, n)
		}
		if firstN < 0 {
			firstN = n
		} else if n != firstN { // must not grow with m at all
			t.Fatalf("inspected count grew with m: %d then %d", firstN, n)
		}
		want, _ := naiveLatest(genVersions(m))
		if got != want {
			t.Fatalf("m=%d O(1) result %v != naive %v", m, got, want)
		}
	}
}
