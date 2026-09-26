package repl

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

// at5 builds the spec's start state: term 5, five term-1 entries, next=[1,1].
func at5(t *testing.T) *Leader {
	l, s := New(3), []op{{"elect", 1, 0, 0, false}}
	for range 5 {
		s = append(s, op{"append", 1, 0, 0, false})
	}
	s = append(s, op{"elect", 5, 0, 0, false}, op{"repl", 0, 2, 0, false}, op{"repl", 0, 3, 0, false})
	for _, x := range s {
		if err := exec(l, x); err != nil {
			t.Fatal(err)
		}
	}
	return l
}
func TestEightSteps(t *testing.T) {
	l := at5(t)
	steps := []struct {
		x                 op
		m2, m3, n2, n3, c int
	}{
		{op{"repl", 0, 2, 5, true}, 5, 0, 6, 1, 0},
		{op{"repl", 0, 3, 5, true}, 5, 5, 6, 6, 0},
		{op{"append", 5, 0, 0, false}, 5, 5, 6, 6, 0},
		{op{"repl", 0, 2, 6, true}, 6, 5, 7, 6, 6},
		{op{"repl", 0, 3, 6, true}, 6, 6, 7, 7, 6},
		{op{"elect", 6, 0, 0, false}, 0, 0, 7, 7, 6},
		{op{"repl", 0, 2, 6, true}, 6, 0, 7, 7, 6},
		{op{"repl", 0, 3, 2, false}, 6, 0, 7, 3, 6},
	}
	for i, s := range steps {
		if err := exec(l, s.x); err != nil {
			t.Fatalf("S%d: %v", i+1, err)
		}
		got := []int{l.MatchIndex(2), l.MatchIndex(3), l.NextIndex(2), l.NextIndex(3), l.CommitIndex()}
		if want := []int{s.m2, s.m3, s.n2, s.n3, s.c}; !slices.Equal(got, want) {
			t.Fatalf("S%d: got %v want %v", i+1, got, want)
		}
	}
}
func TestNaiveRecompute(t *testing.T) {
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{3, 5, 7} {
		for seed := int64(0); seed < 5; seed++ {
			l, o, rng, prev := New(n), newOracle(n), rand.New(rand.NewSource(seed)), 0
			for k := 0; k < 100; k++ {
				x := op{"append", l.Term(), 0, 0, false}
				switch rng.Intn(3) {
				case 1:
					x = op{"elect", l.Term() + 1, 0, 0, false}
				case 2:
					x = op{"repl", 0, 2 + rng.Intn(n-1), rng.Intn(l.Len() + 1), rng.Intn(2) == 0}
				}
				if exec(l, x) != o.apply(x) {
					t.Fatalf("n=%d seed=%d k=%d: error mismatch", n, seed, k)
				}
				c := l.CommitIndex()
				if c != o.commit || c < prev { // naive agreement + monotonicity
					t.Fatalf("n=%d seed=%d k=%d: commit=%d naive=%d prev=%d", n, seed, k, c, o.commit, prev)
				}
				prev = c
			}
		}
	}
}
func TestBookkeeping(t *testing.T) {
	l := at5(t)
	for f := 2; f <= 3; f++ {
		for _, r := range []int{0, 1, 3, 5} {
			if err := l.Replicate(f, true, r); err != nil {
				t.Fatal(err)
			}
			m, q, n := l.MatchIndex(f), l.NextIndex(f), l.Len()
			if q != m+1 || m < 0 || m > n || q < 1 || q > n+1 {
				t.Fatalf("f=%d r=%d: %d %d %d", f, r, m, q, n)
			}
		}
	}
}
func TestRejectedOpsNoTrace(t *testing.T) {
	cases := []struct {
		x   op
		err error
	}{
		{op{"repl", 0, 1, 0, true}, ErrFollowerIndex},
		{op{"repl", 0, 4, 0, true}, ErrFollowerIndex},
		{op{"append", 4, 0, 0, false}, ErrTermMismatch},
		{op{"append", 6, 0, 0, false}, ErrTermMismatch},
		{op{"elect", 5, 0, 0, false}, ErrStaleTerm},
		{op{"repl", 0, 2, -1, true}, ErrHintRange},
		{op{"repl", 0, 2, 6, false}, ErrHintRange},
	}
	for i, c := range cases {
		l := at5(t)
		before := fmt.Sprint(l.term, l.match, l.next, l.commit)
		if err := exec(l, c.x); !errors.Is(err, c.err) ||
			fmt.Sprint(l.term, l.match, l.next, l.commit) != before {
			t.Fatalf("case %d: wrong error or rejected op mutated state", i)
		}
	}
	s := []error{ErrFollowerIndex, ErrTermMismatch, ErrStaleTerm, ErrHintRange}
	if errors.Is(s[0], s[1]) || errors.Is(s[0], s[2]) || errors.Is(s[0], s[3]) ||
		errors.Is(s[1], s[2]) || errors.Is(s[1], s[3]) || errors.Is(s[2], s[3]) {
		t.Fatal("sentinels not mutually distinct")
	}
}
func TestCommitReadComplexity(t *testing.T) {
	if err := checkReads(); err != nil { // m=100/1000/10000, reads must stay O(1)
		t.Fatal(err)
	}
}
func TestConcurrentReaders(t *testing.T) {
	l := at5(t)
	_ = l.Append(5)
	_ = l.Replicate(2, true, 6)
	_ = l.Replicate(3, true, 6)
	want := []int{l.CommitIndex(), l.MatchIndex(2), l.MatchIndex(3)}
	var wg sync.WaitGroup
	read := func() {
		defer wg.Done()
		for range 1000 {
			if got := []int{l.CommitIndex(), l.MatchIndex(2), l.MatchIndex(3)}; !slices.Equal(got, want) {
				t.Errorf("got %v want %v", got, want)
				return
			}
		}
	}
	for range 16 {
		wg.Add(1)
		go read()
	}
	wg.Wait()
}
