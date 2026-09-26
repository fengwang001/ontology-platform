package wsampler

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/wrs"
)

func detRNG(i int) float64 { return float64((i*37)%101+1) / 102.0 }

func genItems(n int) []wrs.Item {
	items := make([]wrs.Item, n)
	for i := range items {
		items[i] = wrs.Item{Val: string(rune('a'+i%26)) + string(rune('A'+i/26%26)) + string(rune('0'+i%10)), Weight: float64(i%5 + 1)}
	}
	return items
}

// TestKeptCounterConstantK proves memory stays O(k): the unexported kept
// counter must equal k no matter how many elements were fed.
func TestKeptCounterConstantK(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		for _, k := range []int{1, 7, 50} {
			s, err := New(k, detRNG)
			if err != nil {
				t.Fatal(err)
			}
			items := genItems(m)
			rand.New(rand.NewSource(int64(m*10+k))).Shuffle(m, func(a, b int) { items[a], items[b] = items[b], items[a] })
			if err := s.Feed(items); err != nil {
				t.Fatalf("m=%d k=%d: %v", m, k, err)
			}
			if s.kept != k {
				t.Errorf("m=%d k=%d: kept=%d, want %d", m, k, s.kept, k)
			}
			if s.seen != m {
				t.Errorf("m=%d k=%d: seen=%d, want %d", m, k, s.seen, m)
			}
		}
	}
}

// TestRejectedFeedLeavesStateUntouched pins invariant 4: any rejected
// operation must not touch slots, kept, or seen, and the sampler stays usable.
func TestRejectedFeedLeavesStateUntouched(t *testing.T) {
	uBadOnce := func(bad float64) func(int) float64 {
		calls := 0
		return func(i int) float64 {
			calls++
			if calls == 6 { // 5 good draws, then the bad batch's first draw
				return bad
			}
			return detRNG(i)
		}
	}
	cases := []struct {
		name  string
		rng   func(int) float64
		batch []wrs.Item
		want  error
	}{
		{"empty val", detRNG, []wrs.Item{{Val: "", Weight: 1}}, ErrEmptyVal},
		{"zero weight", detRNG, []wrs.Item{{Val: "x", Weight: 0}}, ErrBadWeight},
		{"neg weight", detRNG, []wrs.Item{{Val: "x", Weight: -2}}, ErrBadWeight},
		{"u zero at 6", uBadOnce(0), []wrs.Item{{Val: "x", Weight: 1}}, ErrBadU},
		{"u one at 6", uBadOnce(1), []wrs.Item{{Val: "x", Weight: 1}}, ErrBadU},
		{"bad mid batch", detRNG, []wrs.Item{{Val: "ok", Weight: 1}, {Val: "", Weight: 1}}, ErrEmptyVal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := New(3, c.rng)
			if err := s.Feed(genItems(5)); err != nil {
				t.Fatal(err)
			}
			beforeSeen, beforeKept := s.seen, s.kept
			before := s.Sample()
			if err := s.Feed(c.batch); !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if s.seen != beforeSeen || s.kept != beforeKept {
				t.Errorf("counters changed: seen=%d kept=%d", s.seen, s.kept)
			}
			after := s.Sample()
			if len(after) != len(before) {
				t.Fatalf("sample size changed: %d -> %d", len(before), len(after))
			}
			for i := range before {
				if before[i] != after[i] {
					t.Errorf("slot %d changed: %v -> %v", i, before[i], after[i])
				}
			}
			usable := false
			for try := 0; try < 3 && !usable; try++ { // u-cases fail once more at the same step
				usable = s.Feed(genItems(1)) == nil
			}
			if !usable {
				t.Errorf("sampler unusable after rejection")
			}
		})
	}
}

// TestErrorsDistinct checks every failure class maps to its own sentinel.
func TestErrorsDistinct(t *testing.T) {
	_, errK := New(0, detRNG)
	_, errN := New(1, nil)
	s, _ := New(2, detRNG)
	errE := s.Feed([]wrs.Item{{Val: "", Weight: 1}})
	errW := s.Feed([]wrs.Item{{Val: "x", Weight: -1}})
	badU, _ := New(1, func(int) float64 { return 1.5 })
	errU := badU.Feed([]wrs.Item{{Val: "x", Weight: 1}})
	got := []error{errK, errN, errE, errW, errU}
	want := []error{ErrBadCapacity, ErrNilRNG, ErrEmptyVal, ErrBadWeight, ErrBadU}
	for i := range want {
		if !errors.Is(got[i], want[i]) {
			t.Errorf("error %d = %v, want %v", i, got[i], want[i])
		}
		for j := range want {
			if i != j && errors.Is(got[i], want[j]) {
				t.Errorf("error %d (%v) must not match %v", i, got[i], want[j])
			}
		}
	}
}
