package api_test

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

type act = func(*api.API) error

func term(s string, i int) act  { return func(a *api.API) error { return a.SetTerm(s, i) } }
func ap(s, c string) act        { return func(a *api.API) error { return a.Append(s, c) } }
func rp(l, f string, i int) act { return func(a *api.API) error { return a.Replicate(l, f, i) } }

func run(t *testing.T, a *api.API, ss []act) {
	for _, f := range ss {
		if err := f(a); err != nil {
			t.Fatal(err)
		}
	}
}

func okf(t *testing.T, cond bool, f string, args ...any) {
	t.Helper()
	if !cond {
		t.Fatalf(f, args...)
	}
}

var six = []act{
	term("S1", 1), ap("S1", "a"), rp("S1", "S2", 0), ap("S1", "b"), rp("S1", "S2", 1),
	term("S3", 2), ap("S3", "x"), ap("S3", "y"), term("S1", 3), rp("S1", "S2", 1),
	ap("S1", "z"), rp("S1", "S2", 2), rp("S1", "S3", 0),
}

var finalLog = []api.Entry{{Term: 1, Index: 1, Cmd: "a"}, {Term: 1, Index: 2, Cmd: "b"}, {Term: 3, Index: 3, Cmd: "z"}}

func TestLogMatching(t *testing.T) {
	a := api.New()
	run(t, a, six) // S3's term-2 fork is truncated away at step 6
	l := [3][]api.Entry{a.Log("S1"), a.Log("S2"), a.Log("S3")}
	for _, p := range [][2]int{{0, 1}, {0, 2}, {1, 2}} {
		for k := 1; k <= min(len(l[p[0]]), len(l[p[1]])); k++ {
			okf(t, l[p[0]][k-1].Term != l[p[1]][k-1].Term || slices.Equal(l[p[0]][:k], l[p[1]][:k]), "matching violated at %d", k)
		}
	}
}

func TestLeaderCompleteness(t *testing.T) {
	a := api.New()
	run(t, a, six)
	for _, n := range [3]string{"S1", "S2", "S3"} {
		okf(t, n == "S1" || a.Replicate("S1", n, 0) == nil, "re-replication to %s failed", n)
		okf(t, slices.Equal(a.Log(n), finalLog), "committed entries overwritten on %s: %v", n, a.Log(n))
	}
}

func TestCommitIndexQuorum(t *testing.T) {
	cases := []struct {
		ss   []act
		lead string
		want int
	}{
		{[]act{term("S3", 2), ap("S3", "x"), ap("S3", "y")}, "S3", 0},
		{[]act{term("S1", 1), ap("S1", "a"), ap("S1", "b"), rp("S1", "S2", 0), term("S1", 3)}, "S1", 0},
		{six[:12], "S1", 3}, // through step 5: z commits, old-term b rides along
	}
	for i, c := range cases {
		a := api.New()
		run(t, a, c.ss)
		okf(t, a.CommitIndex(c.lead) == c.want, "case %d: got %d want %d", i, a.CommitIndex(c.lead), c.want)
	}
	// Random arrival: shuffled prevIndex attempts, failures interspersed.
	for seed := range int64(5) {
		a, rnd := api.New(), rand.New(rand.NewSource(seed))
		run(t, a, append([]act{term("S1", 1)}, slices.Repeat([]act{ap("S1", "c")}, 20)...))
		for _, fi := range rnd.Perm(2) {
			f := []string{"S2", "S3"}[fi]
			for _, pi := range rnd.Perm(20) {
				_ = a.Replicate("S1", f, pi)
			}
		}
		okf(t, len(a.Log("S2")) == 20 && len(a.Log("S3")) == 20 && a.CommitIndex("S1") == 20, "seed %d order-dependent", seed)
	}
}

func TestFailureLeavesNoTrace(t *testing.T) {
	// Last act is the rejected op; its prefix is setup that must persist.
	cases := []struct {
		ss   []act
		want error
		foll string
	}{
		{[]act{term("S1", 1), ap("S1", "")}, api.ErrEmptyCmd, "S1"},
		{[]act{term("S1", 1), ap("S1", "a"), rp("S1", "S2", -1)}, api.ErrPrevIndexOutOfRange, "S2"},
		{[]act{term("S1", 1), ap("S1", "a"), rp("S1", "S2", 5)}, api.ErrPrevIndexOutOfRange, "S2"},
		{[]act{term("S1", 1), term("S2", 2), ap("S1", "a"), ap("S2", "x"), rp("S1", "S2", 1)}, api.ErrPrevTermMismatch, "S2"},
	}
	for i, c := range cases {
		a := api.New()
		run(t, a, c.ss[:len(c.ss)-1])
		before := slices.Clone(a.Log(c.foll))
		okf(t, errors.Is(c.ss[len(c.ss)-1](a), c.want), "case %d: wrong error", i)
		okf(t, slices.Equal(a.Log(c.foll), before), "case %d: state changed after rejection", i)
		okf(t, a.Append(c.foll, "q") == nil, "case %d: unusable after rejection", i)
	}
	okf(t, api.ErrEmptyCmd != api.ErrPrevIndexOutOfRange && api.ErrEmptyCmd != api.ErrPrevTermMismatch && api.ErrPrevIndexOutOfRange != api.ErrPrevTermMismatch, "sentinel errors not distinct")
}

// spawn runs fn under wg; readers and SelfCheck race on one cluster.
func spawn(wg *sync.WaitGroup, fn func()) {
	wg.Add(1)
	go func() { defer wg.Done(); fn() }()
}

func TestConcurrentReaders(t *testing.T) {
	a := api.New()
	run(t, a, six)
	base := a.Log("S1")
	var wg sync.WaitGroup
	for range 20 {
		spawn(&wg, func() {
			for range 50 {
				if g := a.Log("S1"); !slices.Equal(g, base) {
					t.Error("snapshot drift")
				}
			}
		})
	}
	for range 4 {
		spawn(&wg, func() { _ = api.SelfCheck() })
	}
	wg.Wait()
}

func TestSelfCheck(t *testing.T) {
	okf(t, api.SelfCheck(), "api.SelfCheck failed")
}
