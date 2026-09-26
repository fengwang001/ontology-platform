package repl

import (
	"fmt"
	"math/rand"
)

type op struct {
	kind    string // "append" | "elect" | "repl"
	t, f, r int
	ok      bool
}

// oracle is a naive mirror of Leader that recomputes commitIndex by rescanning
// the whole log: the slow but obviously-correct reference.
type oracle struct {
	n, quorum, term, commit int
	terms, match            []int // terms 1-based, terms[0] is the sentinel
}

func newOracle(n int) *oracle {
	return &oracle{n: n, quorum: n/2 + 1, terms: []int{0}, match: make([]int, n+1)}
}

func (o *oracle) apply(x op) error {
	switch x.kind {
	case "append":
		if x.t != o.term {
			return ErrTermMismatch
		}
		o.terms = append(o.terms, x.t)
	case "elect":
		if x.t <= o.term {
			return ErrStaleTerm
		}
		o.term = x.t
		o.match = make([]int, o.n+1) // all match/next bookkeeping resets
	default:
		if x.f < 2 || x.f > o.n {
			return ErrFollowerIndex
		}
		if x.r < 0 || x.r > len(o.terms)-1 {
			return ErrHintRange
		}
		if x.ok {
			o.match[x.f] = x.r
		}
	}
	for i := 1; i < len(o.terms); i++ { // naive full rescan for commitIndex
		cnt := 1 // the leader itself
		for f := 2; f <= o.n; f++ {
			if o.match[f] >= i {
				cnt++
			}
		}
		if o.terms[i] == o.term && cnt >= o.quorum && i > o.commit {
			o.commit = i
		}
	}
	return nil
}

func exec(l *Leader, x op) error {
	switch x.kind {
	case "append":
		return l.Append(x.t)
	case "elect":
		return l.Elect(x.t)
	default:
		return l.Replicate(x.f, x.ok, x.r)
	}
}

// SelfCheck drives a fresh leader through the spec's eight-step scenario plus
// 200 deterministic randomized follow-ups (every rejection kind included),
// verifying invariants 1-4 against the naive oracle after each step.
func SelfCheck() error {
	l, o := New(3), newOracle(3)
	steps := []op{{"elect", 1, 0, 0, false}}
	for i := 0; i < 5; i++ {
		steps = append(steps, op{"append", 1, 0, 0, false})
	}
	steps = append(steps, op{"elect", 5, 0, 0, false},
		op{"repl", 0, 2, 5, true}, op{"repl", 0, 3, 5, true}, op{"append", 5, 0, 0, false},
		op{"repl", 0, 2, 6, true}, op{"repl", 0, 3, 6, true}, op{"elect", 6, 0, 0, false},
		op{"repl", 0, 2, 6, true}, op{"repl", 0, 3, 2, false})
	prev := 0
	run := func(x op) error {
		before := fmt.Sprint(l.term, l.match, l.next, l.commit)
		if got, want := exec(l, x), o.apply(x); got != want {
			return fmt.Errorf("%+v: got %v want %v", x, got, want)
		} else if got != nil && before != fmt.Sprint(l.term, l.match, l.next, l.commit) {
			return fmt.Errorf("invariant 4: rejected %+v mutated state", x)
		}
		c := l.CommitIndex()
		if c != o.commit || c < prev {
			return fmt.Errorf("invariant 1/3 after %+v: commit=%d naive=%d prev=%d", x, c, o.commit, prev)
		}
		prev = c
		for f := 2; f <= l.n; f++ {
			if m, q, n := l.match[f], l.next[f], l.lg.Len(); m < 0 || m > n || q < 1 || q > n+1 {
				return fmt.Errorf("invariant 2: f=%d match=%d next=%d len=%d", f, m, q, n)
			}
		}
		return nil
	}
	for _, x := range steps {
		if err := run(x); err != nil {
			return err
		}
	}
	rng := rand.New(rand.NewSource(727))
	for k := 0; k < 200; k++ {
		x := op{"append", l.term, 0, 0, false}
		switch rng.Intn(7) {
		case 1:
			x = op{"elect", l.term + 1 + rng.Intn(3), 0, 0, false}
		case 2:
			x = op{"repl", 0, 2 + rng.Intn(l.n-1), rng.Intn(l.lg.Len() + 1), rng.Intn(2) == 0}
		case 3, 4, 5, 6: // one of the four rejection kinds
			x = []op{
				{"repl", 0, 1, 0, true},
				{"append", l.term + 1, 0, 0, false},
				{"elect", l.term, 0, 0, false},
				{"repl", 0, 2, l.lg.Len() + 1, false},
			}[rng.Intn(4)]
		}
		if err := run(x); err != nil {
			return err
		}
	}
	return checkReads()
}

// checkReads verifies CommitIndex reads O(1) log terms regardless of log size.
func checkReads() error {
	for _, m := range []int{100, 1000, 10000} {
		l := New(3)
		_ = l.Elect(1)
		for i := 0; i < m; i++ {
			_ = l.Append(1)
		}
		_ = l.Replicate(2, true, m)
		_ = l.Replicate(3, true, m)
		if c := l.CommitIndex(); c != m || l.reads > 1 {
			return fmt.Errorf("complexity: m=%d commit=%d reads=%d", m, c, l.reads)
		}
	}
	return nil
}
