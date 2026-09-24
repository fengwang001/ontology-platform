package sched_test

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"testing"
	"time"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/sched"
)

func buildGraph(t *testing.T, n int, edges [][2]int) *graph.Graph {
	t.Helper()
	g := graph.New()
	for i := 0; i < n; i++ {
		g.AddTask(fmt.Sprintf("t%04d", i))
	}
	for _, e := range edges {
		if err := g.AddEdge(fmt.Sprintf("t%04d", e[0]), fmt.Sprintf("t%04d", e[1])); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func okFns(g *graph.Graph) map[string]exec.Func {
	fns := map[string]exec.Func{}
	for _, id := range g.Tasks() {
		fns[id] = func(context.Context) error { return nil }
	}
	return fns
}

func TestPeakAndReadyChecks(t *testing.T) {
	g := buildGraph(t, 500, nil)
	s := sched.New(8, sched.FailFast)
	if _, err := s.Run(g, okFns(g)); err != nil {
		t.Fatal(err)
	}
	if s.Peak() != 8 {
		t.Fatalf("peak = %d; want exactly 8", s.Peak())
	}
	n, e := g.Size()
	if s.ReadyChecks() > 4*(n+e) {
		t.Fatalf("readyChecks = %d; want <= %d", s.ReadyChecks(), 4*(n+e))
	}
}

func TestSerialMatchesParallel(t *testing.T) {
	edges := [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}, {4, 5}}
	g := buildGraph(t, 6, edges)
	fns := okFns(g)
	fns["t0004"] = func(context.Context) error { return fmt.Errorf("boom") }
	var reports []string
	for _, conc := range []int{1, 8} {
		rep, err := sched.New(conc, sched.BestEffort).Run(g, fns)
		if err != nil {
			t.Fatal(err)
		}
		reports = append(reports, rep.String())
	}
	if reports[0] != reports[1] {
		t.Fatalf("serial vs parallel report mismatch:\n%s\n%s", reports[0], reports[1])
	}
}

func TestDeterministicReport(t *testing.T) {
	edges := [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}, {4, 5}, {5, 6}}
	mkFns := func(g *graph.Graph, delay bool) map[string]exec.Func {
		fns := okFns(g)
		if delay {
			for _, id := range g.Tasks() {
				fns[id] = func(context.Context) error {
					time.Sleep(time.Duration(rand.Intn(2000)) * time.Microsecond)
					return nil
				}
			}
		}
		fns["t0005"] = func(context.Context) error { return fmt.Errorf("boom") }
		return fns
	}
	base, err := sched.New(8, sched.BestEffort).Run(buildGraph(t, 7, edges), mkFns(buildGraph(t, 7, edges), false))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		shuffled := append([][2]int{}, edges...)
		rand.New(rand.NewSource(int64(i))).Shuffle(len(shuffled), func(a, b int) {
			shuffled[a], shuffled[b] = shuffled[b], shuffled[a]
		})
		g := buildGraph(t, 7, shuffled)
		rep, err := sched.New(8, sched.BestEffort).Run(g, mkFns(g, true))
		if err != nil {
			t.Fatal(err)
		}
		if rep.String() != base.String() {
			t.Fatalf("run %d: report mismatch:\n%s\n%s", i, rep.String(), base.String())
		}
	}
}

func settled(base int) bool {
	for i := 0; i < 200; i++ {
		if runtime.NumGoroutine() <= base {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestNoGoroutineLeak(t *testing.T) {
	errBoom := fmt.Errorf("boom")
	cases := []struct {
		name string
		mode sched.Mode
		fail bool
	}{
		{"all success", sched.FailFast, false},
		{"fail fast", sched.FailFast, true},
		{"best effort", sched.BestEffort, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := buildGraph(t, 50, [][2]int{{0, 1}, {1, 2}})
			fns := okFns(g)
			if tc.fail {
				fns["t0000"] = func(context.Context) error { return errBoom }
			}
			base := runtime.NumGoroutine()
			if _, err := sched.New(8, tc.mode).Run(g, fns); err != nil {
				t.Fatal(err)
			}
			if !settled(base) {
				t.Fatalf("goroutines %d > baseline %d", runtime.NumGoroutine(), base)
			}
		})
	}
}

func TestBoundaryGraphs(t *testing.T) {
	cases := []struct {
		name  string
		n     int
		edges [][2]int
		want  map[fail.State]int
	}{
		{"empty", 0, nil, map[fail.State]int{}},
		{"single", 1, nil, map[fail.State]int{fail.Success: 1}},
		{"no deps", 20, nil, map[fail.State]int{fail.Success: 20}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := buildGraph(t, tc.n, tc.edges)
			rep, err := sched.New(4, sched.FailFast).Run(g, fnsOrEmpty(g))
			if err != nil {
				t.Fatal(err)
			}
			for st, want := range tc.want {
				if rep.Count(st) != want {
					t.Fatalf("count(%s) = %d; want %d", st, rep.Count(st), want)
				}
			}
		})
	}
}

func fnsOrEmpty(g *graph.Graph) map[string]exec.Func { return okFns(g) }
