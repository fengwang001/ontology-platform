package nge_test

import (
	"errors"
	"ontology/nge"
	"slices"
	"testing"
)

func naive(a []int) []int {
	ans := make([]int, len(a))
	for i := range a {
		ans[i] = nge.None
		for j := len(a) - 1; j > i; j-- {
			if a[j] > a[i] {
				ans[i] = j
			}
		}
	}
	return ans
}

func TestNextGreaterMatchesNaive(t *testing.T) {
	cases := [][]int{{2, 2, 3}, {7, 7, 7, 7}, {1, 2, 3, 4, 5}, {5, 4, 3, 2, 1}, {3, 1, 4, 1, 5, 9, 2, 6}, {}, {42}}
	s, _ := nge.New(1024)
	for _, a := range cases {
		got, err := s.NextGreater(a)
		if err != nil || !slices.Equal(got, naive(a)) || s.SelfCheck(a, got) != nil {
			t.Errorf("a=%v: got=%v err=%v", a, got, err)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	s, _ := nge.New(16)
	a := []int{2, 2, 3}
	if ans, _ := s.NextGreater(a); s.SelfCheck(a, ans) != nil {
		t.Fatal("valid answer rejected")
	}
	bad := [][]int{{1, 2, nge.None}, {2, 2, 2}, {2, 1, nge.None}, {0, 2, nge.None}, {2, 2}}
	for _, b := range bad {
		if err := s.SelfCheck(a, b); !errors.Is(err, nge.ErrSelfCheck) {
			t.Errorf("ans=%v: got %v", b, err)
		}
	}
}

func TestNoneSentinel(t *testing.T) {
	s, _ := nge.New(8)
	ans, err := s.NextGreater([]int{9})
	if nge.None >= 0 || err != nil || !slices.Equal(ans, []int{nge.None}) {
		t.Fatalf("None=%d ans=%v err=%v", nge.None, ans, err)
	}
}

func TestRejections(t *testing.T) {
	for _, limit := range []int{0, -3} {
		if _, err := nge.New(limit); !errors.Is(err, nge.ErrBadLimit) {
			t.Fatalf("limit=%d: %v", limit, err)
		}
	}
	s, _ := nge.New(2)
	in := []int{3, 1, 2}
	_, e1 := s.NextGreater(nil)
	_, e2 := s.NextGreater([]int{1, 2, 3})
	if !errors.Is(e1, nge.ErrNilInput) || !errors.Is(e2, nge.ErrTooLong) ||
		e1 == e2 || e1 == nge.ErrBadLimit || e2 == nge.ErrBadLimit {
		t.Fatalf("e1=%v e2=%v", e1, e2)
	}
	got, err := s.NextGreater([]int{1, 2})
	if !slices.Equal(in, []int{3, 1, 2}) || err != nil || !slices.Equal(got, []int{1, nge.None}) {
		t.Fatal("input modified or scanner unusable after rejection")
	}
}

func TestConcurrentNextGreater(t *testing.T) {
	s, _ := nge.New(256)
	a := make([]int, 200)
	for i := range a {
		a[i] = (i * 37) % 50
	}
	want, _ := s.NextGreater(a)
	done, start := make(chan []int, 16), make(chan struct{})
	for range 16 {
		go func() {
			<-start
			got, _ := s.NextGreater(a)
			if s.SelfCheck(a, got) != nil {
				t.Error("selfcheck failed")
			}
			done <- got
		}()
	}
	close(start)
	for range 16 {
		if got := <-done; !slices.Equal(got, want) {
			t.Fatal("concurrent results differ")
		}
	}
}
