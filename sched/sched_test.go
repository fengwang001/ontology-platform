package sched

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"testing"
	"time"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
)

var errBoom = errors.New("boom")

func okF(context.Context) error  { return nil }
func failFn(err error) exec.Func { return func(context.Context) error { return err } }
func mkGraph(ids ...string) (*graph.Graph, map[graph.ID]exec.Func) {
	g, fns := graph.New(), map[graph.ID]exec.Func{}
	for _, id := range ids {
		g.AddNode(graph.ID(id))
		fns[graph.ID(id)] = okF
	}
	return g, fns
}
func genGraph(n int, chain bool) (*graph.Graph, map[graph.ID]exec.Func) {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("t%04d", i)
	}
	g, fns := mkGraph(ids...)
	for i := 1; i < n && chain; i++ {
		_ = g.AddEdge(graph.ID(ids[i-1]), graph.ID(ids[i]))
	}
	return g, fns
}
func mustRun(t *testing.T, g *graph.Graph, fns map[graph.ID]exec.Func, m fail.Mode, l int) *report.Report {
	r, err := New(l).Run(context.Background(), g, fns, m)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func ent(t *testing.T, rep *report.Report, id graph.ID, s fail.Status, o graph.ID, started bool, err error) {
	if e, _ := rep.Get(id); e.Status != s || e.Origin != o || e.Started != started || (err != nil && !errors.Is(e.Err, err)) {
		t.Errorf("%s: got {%v %q started=%t %v}", id, e.Status, e.Origin, e.Started, e.Err)
	}
}
func abc() (*graph.Graph, map[graph.ID]exec.Func) { // A→B→C 且 A 失败，S 为并行慢分支
	g, fns := mkGraph("a", "b", "c", "s")
	_ = g.AddEdge("a", "b")
	_ = g.AddEdge("b", "c")
	fns["a"] = failFn(errBoom)
	fns["s"] = func(ctx context.Context) error {
		select {
		case <-time.After(300 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return g, fns
}
func buildDet(perm []int) (*graph.Graph, map[graph.ID]exec.Func) {
	edges := [][2]string{{"a", "c"}, {"b", "c"}, {"c", "d"}, {"a", "e"}, {"d", "f"}, {"e", "f"}}
	g, fns := mkGraph("a", "b", "c", "d", "e", "f")
	for _, j := range perm {
		_ = g.AddEdge(graph.ID(edges[j][0]), graph.ID(edges[j][1]))
	}
	return g, fns
}

func stable(t *testing.T, name string, gen func(int) (*graph.Graph, map[graph.ID]exec.Func), limit int) {
	base := ""
	for i := 0; i < 20; i++ {
		g, fns := gen(i)
		if s := mustRun(t, g, fns, fail.FastFail, limit).String(); i == 0 {
			base = s
		} else if s != base {
			t.Fatalf("%s run %d differs", name, i)
		}
	}
}

func TestStates(t *testing.T) {
	cases := map[fail.Mode][4]fail.Status{
		fail.FastFail:   {fail.Failed, fail.Skipped, fail.Skipped, fail.Canceled},
		fail.BestEffort: {fail.Failed, fail.Skipped, fail.Skipped, fail.Success},
	}
	ids := []graph.ID{"a", "b", "c", "s"}
	origins := []graph.ID{"a", "a", "a", ""} // C 指向最初失败的 A 而非直接上游 B
	for mode, st := range cases {
		g, fns := abc()
		rep := mustRun(t, g, fns, mode, 4)
		for i, id := range ids {
			ent(t, rep, id, st[i], origins[i], st[i] != fail.Skipped, nil)
		}
	}
}

func emptyG() (*graph.Graph, map[graph.ID]exec.Func) { return graph.New(), map[graph.ID]exec.Func{} }
func oneG() (*graph.Graph, map[graph.ID]exec.Func)   { return mkGraph("only") }
func threeG() (*graph.Graph, map[graph.ID]exec.Func) { return mkGraph("x", "y", "z") }
func chainG() (*graph.Graph, map[graph.ID]exec.Func) { return genGraph(1000, true) }

func TestLimits(t *testing.T) {
	g500, f500 := genGraph(500, false)
	s := New(8)
	if _, err := s.Run(context.Background(), g500, f500, fail.FastFail); err != nil {
		t.Fatal(err)
	}
	if s.Peak() != 8 || s.Decisions() > 4*500 {
		t.Errorf("peak=%d decisions=%d, want peak=8 decisions<=%d", s.Peak(), s.Decisions(), 4*500)
	}
	builds := []func() (*graph.Graph, map[graph.ID]exec.Func){emptyG, oneG, threeG, chainG}
	names := []string{"empty", "single", "independent", "chain-1000"}
	total := []int{0, 1, 3, 1000}
	for i, b := range builds {
		g, fns := b()
		if rep := mustRun(t, g, fns, fail.FastFail, 4); len(rep.Entries) != total[i] || rep.Count(fail.Success) != total[i] {
			t.Errorf("%s: total=%d success=%d, want %d", names[i], len(rep.Entries), rep.Count(fail.Success), total[i])
		}
	}
}

func TestDeterminism(t *testing.T) {
	stable(t, "shuffle-edges-20x", func(i int) (*graph.Graph, map[graph.ID]exec.Func) {
		return buildDet(rand.New(rand.NewSource(int64(i))).Perm(6))
	}, 3)
	stable(t, "random-delay-20x", func(i int) (*graph.Graph, map[graph.ID]exec.Func) {
		g, fns := buildDet([]int{0, 1, 2, 3, 4, 5})
		rng := rand.New(rand.NewSource(int64(i)))
		for id := range fns {
			d := time.Duration(rng.Intn(3)) * time.Millisecond
			fns[id] = func(context.Context) error { time.Sleep(d); return nil }
		}
		return g, fns
	}, 4)
	g1, f1 := buildDet([]int{0, 1, 2, 3, 4, 5})
	g2, f2 := buildDet([]int{0, 1, 2, 3, 4, 5})
	if mustRun(t, g1, f1, fail.FastFail, 1).String() != mustRun(t, g2, f2, fail.FastFail, 8).String() {
		t.Fatal("limit=1 report differs from limit=8")
	}
}
func TestInjection(t *testing.T) {
	errF1, errF2 := errors.New("e1"), errors.New("e2")
	cyc, _ := mkGraph("x")
	_ = cyc.AddEdge("x", "x")
	pan, pfns := mkGraph("p")
	pfns["p"] = func(context.Context) error { panic("kaboom") }
	multi, mfns := mkGraph("f1", "f2", "d1", "d2")
	_ = multi.AddEdge("f1", "d1")
	_ = multi.AddEdge("f2", "d2")
	mfns["f1"], mfns["f2"] = failFn(errF1), failFn(errF2)
	late, lfns := mkGraph("slow", "bad")
	lfns["slow"] = func(context.Context) error { time.Sleep(150 * time.Millisecond); return nil }
	lfns["bad"] = failFn(errBoom)
	if _, err := New(2).Run(context.Background(), cyc, nil, fail.FastFail); !errors.Is(err, fail.ErrCycle) {
		t.Errorf("cycle: want ErrCycle, got %v", err)
	}
	reps := map[graph.ID]*report.Report{
		"panic": mustRun(t, pan, pfns, fail.FastFail, 2),
		"multi": mustRun(t, multi, mfns, fail.FastFail, 2),
		"late":  mustRun(t, late, lfns, fail.FastFail, 2),
	}
	cases := map[[2]graph.ID]report.Entry{
		{"panic", "p"}:   {Status: fail.Failed, Started: true, Origin: "p", Err: fail.ErrPanic},
		{"multi", "f1"}:  {Status: fail.Failed, Started: true, Origin: "f1", Err: errF1},
		{"multi", "f2"}:  {Status: fail.Failed, Started: true, Origin: "f2", Err: errF2},
		{"multi", "d1"}:  {Status: fail.Skipped, Started: false, Origin: "f1"},
		{"multi", "d2"}:  {Status: fail.Skipped, Started: false, Origin: "f2"},
		{"late", "slow"}: {Status: fail.Canceled, Started: true}, // 取消后写回被丢弃
	}
	for k, w := range cases {
		ent(t, reps[k[0]], k[1], w.Status, w.Origin, w.Started, w.Err)
	}
}
func TestNoLeak(t *testing.T) {
	names := []string{"all-success", "fast-fail", "best-effort"}
	modes := []fail.Mode{fail.FastFail, fail.FastFail, fail.BestEffort}
	failing := []bool{false, true, true}
	for i := range names {
		base := runtime.NumGoroutine()
		g, fns := abc()
		if !failing[i] {
			fns["a"] = okF
		}
		mustRun(t, g, fns, modes[i], 4)
		for j := 0; j < 100 && runtime.NumGoroutine() > base; j++ {
			time.Sleep(10 * time.Millisecond)
		}
		if runtime.NumGoroutine() > base {
			t.Errorf("%s: goroutines %d > baseline %d", names[i], runtime.NumGoroutine(), base)
		}
	}
}
