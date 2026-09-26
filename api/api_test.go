package api

import "errors"
import "math/rand"
import "reflect"
import "sync"
import "testing"

// must fails the test when cond is false; it turns each 3-line fatal guard
// into one simple call that gofmt keeps on a single line.
func must(t *testing.T, cond bool, format string, args ...any) {
	t.Helper()
	if !cond {
		t.Fatalf(format, args...)
	}
}

// driveWellFormed generates protocol-shaped traffic (an accept uses only a
// prepared round, the condition for promised>=accepted) and verifies each step.
func driveWellFormed(t *testing.T, m int, seed int64, verify func(int, []snapshot)) {
	t.Helper()
	p, rng := New(m), rand.New(rand.NewSource(seed))
	promised := make([]int, m)
	for s := 0; s < 1500; s++ {
		acc := rng.Intn(m)
		if rng.Intn(2) == 0 {
			n := promised[acc] + 1 + rng.Intn(3)
			if _, _, e := p.Prepare(acc, n); e == nil {
				promised[acc] = n
			}
		} else if promised[acc] > 0 {
			_, _ = p.Accept(acc, promised[acc], rng.Intn(3)+1)
		}
		verify(s, p.dump())
	}
}
func TestTenStepScenario(t *testing.T) {
	p := New(3)
	steps := [][5]int{
		{'p', 0, 2, 0, 0}, {'p', 1, 2, 0, 0}, {'p', 2, 2, 0, 0},
		{'a', 0, 2, 10, 0}, {'a', 1, 2, 10, 10},
		{'p', 0, 3, 0, 10}, {'p', 1, 3, 0, 10}, {'p', 2, 3, 0, 10},
		{'a', 0, 3, 10, 10}, {'a', 1, 3, 10, 10},
	}
	for i, s := range steps {
		if s[0] == 'p' {
			_, _, e := p.Prepare(s[1], s[2])
			must(t, e == nil, "S%d prepare: %v", i+1, e)
		} else {
			ok, e := p.Accept(s[1], s[2], s[3])
			must(t, ok && e == nil, "S%d accept ok=%v %v", i+1, ok, e)
		}
		v, chosen := p.Chosen()
		must(t, chosen == (s[4] != 0) && (s[4] == 0 || v == s[4]),
			"S%d Chosen=(%d,%v), want value %d", i+1, v, chosen, s[4])
	}
}
func TestRejectionLeavesNoTrace(t *testing.T) {
	distinct := ErrInvalidAcceptor != ErrInvalidProposal && ErrInvalidAcceptor != ErrStalePrepare &&
		ErrInvalidProposal != ErrStalePrepare
	must(t, distinct, "sentinel errors must be pairwise distinct")
	baseline := func() *Paxos {
		p := New(3)
		_, _, _ = p.Prepare(0, 2)
		_, _ = p.Accept(0, 2, 10)
		_, _, _ = p.Prepare(1, 3)
		return p
	}
	cases := []struct {
		name string
		want error
		f    func(*Paxos) error
	}{
		{"prepare bad index", ErrInvalidAcceptor, func(p *Paxos) error { _, _, e := p.Prepare(-1, 1); return e }},
		{"accept bad index", ErrInvalidAcceptor, func(p *Paxos) error { _, e := p.Accept(3, 1, 1); return e }},
		{"prepare non-positive", ErrInvalidProposal, func(p *Paxos) error { _, _, e := p.Prepare(2, 0); return e }},
		{"accept non-positive", ErrInvalidProposal, func(p *Paxos) error { _, e := p.Accept(2, -1, 1); return e }},
		{"stale prepare", ErrStalePrepare, func(p *Paxos) error { _, _, e := p.Prepare(0, 2); return e }},
	}
	for _, tc := range cases {
		p := baseline()
		before := p.dump()
		must(t, errors.Is(tc.f(p), tc.want) && reflect.DeepEqual(p.dump(), before),
			"%s: wrong error or rejected call changed state", tc.name)
	}
	p := baseline()
	before := p.dump()
	ok, e := p.Accept(1, 2, 99)
	must(t, !ok && e == nil && reflect.DeepEqual(p.dump(), before), "normal refusal must leave no trace")
	okAfter, _ := p.Accept(1, 3, 77)
	must(t, okAfter, "instance must stay usable after a refusal")
}
func TestBookkeepingInvariant(t *testing.T) {
	for _, m := range []int{3, 5, 9} {
		prev := make([]int, m)
		driveWellFormed(t, m, int64(m*7+1), func(s int, d []snapshot) {
			for i, x := range d {
				must(t, x[0] >= x[1] && x[0] >= prev[i],
					"m=%d s=%d acc=%d: promised=%d accepted=%d prev=%d", m, s, i, x[0], x[1], prev[i])
				prev[i] = x[0]
			}
		})
	}
}
func TestSafetyAtMostOneValue(t *testing.T) {
	for _, m := range []int{3, 7, 11} {
		maj := m/2 + 1
		driveWellFormed(t, m, int64(m*13+5), func(s int, d []snapshot) {
			votes, atMaj := map[int]int{}, 0
			for _, x := range d {
				if x[1] > 0 {
					votes[x[2]]++
					if votes[x[2]] == maj {
						atMaj++
					}
				}
			}
			must(t, atMaj <= 1, "m=%d s=%d: %d values at majority", m, s, atMaj)
		})
	}
}
func TestConcurrentChosenReaders(t *testing.T) {
	p := New(5)
	for i := 0; i < 3; i++ {
		_, _, _ = p.Prepare(i, 1)
		_, _ = p.Accept(i, 1, 88)
	}
	const G, R = 32, 200
	var wg sync.WaitGroup
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < R; r++ {
				v, ok := p.Chosen()
				if v != 88 || !ok { // every read field-for-field equal to (88,true)
					t.Errorf("Chosen=(%d,%v), want (88,true)", v, ok)
				}
			}
		}()
	}
	wg.Wait()
}
func TestSelfCheck(t *testing.T) { must(t, New(3).SelfCheck(), "SelfCheck failed") }
