package slide_test

import (
	"errors"
	"ontology/mono"
	"ontology/slide"
	"slices"
	"sync"
	"testing"
)

func mustOK(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
func naiveMaxes(seq []int, w int) (out []int) {
	for j := 0; j+w <= len(seq); j++ {
		m := seq[j]
		for _, v := range seq[j : j+w] {
			m = max(m, v)
		}
		out = append(out, m)
	}
	return
}
func shapes(n int) [][]int {
	inc, dec, eq := make([]int, n), make([]int, n), make([]int, n)
	for i := range inc {
		inc[i], dec[i], eq[i] = i, n-i, 7
	}
	return [][]int{eq, inc, dec}
}
func TestMaxesAgainstNaive(t *testing.T) {
	for i, seq := range append(shapes(60), []int{3, 3, 2}) {
		for _, w := range []int{1, len(seq)/2 + 1, len(seq)} {
			if got, err := slide.Maxes(seq, w); err != nil || !slices.Equal(got, naiveMaxes(seq, w)) {
				t.Errorf("case %d w=%d got=%v err=%v", i, w, got, err)
			}
		}
	}
}
func TestMonotonicQueue(t *testing.T) {
	mq := mono.New()
	for i, v := range []int{3, 3, 2} {
		mustOK(t, mq.Push(i, v))
		mq.Expire(max(0, i-1))
		if !mq.Healthy(max(0, i-1), 2) {
			t.Fatalf("step %d: invariant broken", i)
		}
	}
	idx, val, _ := mq.Max()
	if mq.Len() != 2 || idx != 1 || val != 3 {
		t.Fatalf("len=%d front=(%d,%d)", mq.Len(), idx, val)
	}
}
func TestRejectedOpsNoSideEffect(t *testing.T) {
	_, e1 := slide.New(0)
	_, e2 := slide.Maxes([]int{2, 1, 3}, -1)
	_, e3 := slide.Maxes([]int{2, 1, 3}, 4)
	for i, err := range []error{e1, e2, e3} {
		if !errors.Is(err, []error{slide.ErrNonPositiveWidth, slide.ErrNonPositiveWidth, slide.ErrWidthExceedsLength}[i]) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	mq := mono.New()
	_ = mq.Push(0, 5)
	if err := mq.Push(0, 9); !errors.Is(err, mono.ErrIndexNotIncreasing) || mq.Len() != 1 {
		t.Fatalf("err=%v len=%d", err, mq.Len())
	}
	mustOK(t, mq.Push(1, 7))
}
func TestSelfCheck(t *testing.T) {
	for _, n := range []int{7, 1000, 100000} {
		for _, seq := range shapes(n) {
			s, _ := slide.New(min(n, 64))
			for _, v := range seq {
				s.Feed(v)
			}
			mustOK(t, s.SelfCheck())
		}
	}
}
func TestConcurrentMaxes(t *testing.T) {
	seq := shapes(500)[1]
	want, err := slide.Maxes(seq, 17)
	mustOK(t, err)
	s, _ := slide.New(17)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, e := slide.Maxes(seq, 17); e != nil || !slices.Equal(got, want) || s.SelfCheck() != nil {
				t.Error("concurrent mismatch")
			}
		}()
	}
	wg.Wait()
}
