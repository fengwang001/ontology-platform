package api_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"ontology/api"
)

func hashRNG(i int) int { return int((uint(i)*2654435761)>>16)%i + 1 }

func elems(n int) []string {
	es := make([]string, n)
	for i := range es {
		es[i] = fmt.Sprintf("e%d", i+1)
	}
	return es
}

// naiveReplay 朴素重放：保留全部历史，按同一 rng 重放第 k+1..N 步决策。
func naiveReplay(es []string, k int, rng func(int) int) []string {
	var slots []string
	for idx, e := range es {
		if i := idx + 1; i <= k {
			slots = append(slots, e)
		} else if j := rng(i); j <= k {
			slots[j-1] = e
		}
	}
	return slots
}

func newFed(t *testing.T, k, n int, rng func(int) int) *api.Sampler {
	t.Helper()
	s, err := api.New(k, rng)
	if err == nil {
		err = s.Feed(elems(n))
	}
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSampleSize(t *testing.T) {
	for _, c := range []struct{ k, n int }{{1, 0}, {1, 7}, {3, 2}, {3, 3}, {3, 100}, {5, 1000}, {10, 10000}} {
		s := newFed(t, c.k, c.n, hashRNG)
		if len(s.Sample()) != min(c.k, c.n) || s.Size() != c.n {
			t.Fatalf("k=%d n=%d: |sample|=%d size=%d", c.k, c.n, len(s.Sample()), s.Size())
		}
	}
}

func TestFirstKRetained(t *testing.T) {
	for _, k := range []int{1, 3, 8} {
		for _, n := range []int{0, 1, k/2 + 1, k} {
			if got := newFed(t, k, n, hashRNG).Sample(); !slices.Equal(got, elems(n)) {
				t.Fatalf("k=%d n=%d: %v", k, n, got)
			}
		}
	}
}

func TestMatchesNaiveReplay(t *testing.T) {
	rngs := map[string]func(int) int{
		"always1": func(i int) int { return 1 },
		"alwaysI": func(i int) int { return i },
		"hash":    hashRNG,
	}
	for name, rng := range rngs {
		for _, k := range []int{1, 3, 7} {
			for _, n := range []int{k, k + 1, 5*k + 3} {
				s, want := newFed(t, k, n, rng), naiveReplay(elems(n), k, rng)
				if !slices.Equal(s.Sample(), want) {
					t.Fatalf("%s k=%d n=%d: %v != %v", name, k, n, s.Sample(), want)
				}
			}
		}
	}
}

func TestErrorsDistinct(t *testing.T) {
	newErr := func(k int, rng func(int) int) error { _, err := api.New(k, rng); return err }
	sb, _ := api.New(1, func(i int) int { return i + 1 })
	se, _ := api.New(2, hashRNG)
	cases := []struct{ err, want error }{
		{newErr(0, hashRNG), api.ErrBadCapacity},
		{newErr(1, nil), api.ErrNilRNG},
		{sb.Feed([]string{"a", "b"}), api.ErrBadDraw},
		{se.Feed([]string{"ok", ""}), api.ErrEmptyElement},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("err=%v, want %v", c.err, c.want)
		}
	}
	for a, ca := range cases {
		for b, cb := range cases {
			if a != b && errors.Is(ca.err, cb.want) {
				t.Fatalf("not distinct: %v matches %v", ca.err, cb.want)
			}
		}
	}
}

func TestFailureLeavesNoTrace(t *testing.T) {
	s := newFed(t, 3, 4, hashRNG)
	before, n := s.Sample(), s.Size()
	if err := s.Feed([]string{"x", "", "y"}); !errors.Is(err, api.ErrEmptyElement) {
		t.Fatalf("err=%v", err)
	}
	if !slices.Equal(s.Sample(), before) || s.Size() != n {
		t.Fatal("rejected feed changed state")
	}
	bad, _ := api.New(2, func(i int) int { return i + 1 })
	if err := bad.Feed([]string{"a", "b", "c"}); !errors.Is(err, api.ErrBadDraw) {
		t.Fatalf("err=%v", err)
	}
	if bad.Size() != 0 || len(bad.Sample()) != 0 {
		t.Fatal("bad draw left trace")
	}
	if err := s.Feed([]string{"z"}); err != nil || s.Size() != n+1 {
		t.Fatal("unusable after rejection")
	}
}

func TestConcurrentReadOnly(t *testing.T) {
	s := newFed(t, 7, 5000, hashRNG)
	want := s.Sample()
	const g = 32
	start := make(chan struct{})
	res := make(chan []string, g)
	for i := 0; i < g; i++ {
		go func() { <-start; res <- s.Sample() }()
	}
	close(start)
	for i := 0; i < g; i++ {
		if got := <-res; !slices.Equal(got, want) {
			t.Fatalf("concurrent sample differs: %v", got)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := newFed(t, 3, 0, hashRNG).SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
