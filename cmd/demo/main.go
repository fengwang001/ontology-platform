// 并行任务图调度与失败传播演示：逐条判定打印 OK/FAIL，全部通过时退出码 0。
package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"time"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
	"ontology/sched"
)

var errA = errors.New("a failed")

func ok(context.Context) error { return nil }
func want(cond bool, format string, args ...any) error {
	if !cond {
		return fmt.Errorf(format, args...)
	}
	return nil
}
func nodes(ids ...graph.ID) (*graph.Graph, map[graph.ID]exec.Func) {
	g := graph.New()
	fns := map[graph.ID]exec.Func{}
	for _, id := range ids {
		g.AddNode(id)
		fns[id] = ok
	}
	return g, fns
}
func run(g *graph.Graph, fns map[graph.ID]exec.Func, mode fail.Mode, limit int) *report.Report {
	r, err := sched.New(limit).Run(context.Background(), g, fns, mode)
	if err != nil {
		panic(err)
	}
	return r
}
func slow(ctx context.Context) error {
	select {
	case <-time.After(300 * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// abc：A→B→C 且 A 失败，S 为并行慢分支。
func abc() (*graph.Graph, map[graph.ID]exec.Func) {
	g, fns := nodes("a", "b", "c", "s")
	_ = g.AddEdge("a", "b")
	_ = g.AddEdge("b", "c")
	fns["a"] = func(context.Context) error { return errA }
	fns["s"] = slow
	return g, fns
}
func ent(r *report.Report, id graph.ID) report.Entry {
	e, _ := r.Get(id)
	return *e
}
func main() {
	ga, fa := abc()
	fast := run(ga, fa, fail.FastFail, 4)
	gb, fb := abc()
	best := run(gb, fb, fail.BestEffort, 4)
	g5, f5 := nodes()
	for i := 0; i < 500; i++ {
		id := graph.ID(fmt.Sprintf("t%03d", i))
		g5.AddNode(id)
		f5[id] = ok
	}
	big := sched.New(8)
	if _, err := big.Run(context.Background(), g5, f5, fail.FastFail); err != nil {
		panic(err)
	}
	checks := []struct {
		n string
		f func() error
	}{
		{"cycle-path-verified", checkCycle},
		{"panic-caught", checkPanic},
		{"skip-cause-points-to-A", func() error {
			e := ent(fast, "c")
			return want(e.Status == fail.Skipped && e.Origin == "a", "c=%v origin=%q", e.Status, e.Origin)
		}},
		{"slow-canceled-not-skipped", func() error {
			e := ent(fast, "s")
			return want(e.Status == fail.Canceled && e.Started, "s=%v started=%t", e.Status, e.Started)
		}},
		{"mode-distribution-differs", func() error {
			c := want(fast.Count(fail.Canceled) > 0 && best.Count(fail.Canceled) == 0, "canceled fast=%d best=%d", fast.Count(fail.Canceled), best.Count(fail.Canceled))
			s := want(best.Count(fail.Success) > fast.Count(fail.Success), "success fast=%d best=%d", fast.Count(fail.Success), best.Count(fail.Success))
			return want(c == nil && s == nil, "%v %v", c, s)
		}},
		{"peak-within-limit", func() error { return want(big.Peak() == 8, "peak=%d, want 8", big.Peak()) }},
		{"decisions-within-bound", func() error {
			return want(big.Decisions() <= 4*500, "decisions=%d > %d", big.Decisions(), 4*500)
		}},
		{"shuffle-20x-identical", checkShuffle},
		{"multi-failure-all-recorded", checkMultiFail},
		{"late-write-discarded", checkLateWrite},
		{"goroutine-back-to-baseline", checkGoroutines},
	}
	bad := 0
	for _, c := range checks {
		if err := c.f(); err != nil {
			bad++
			fmt.Printf("FAIL %s: %v\n", c.n, err)
		} else {
			fmt.Printf("OK %s\n", c.n)
		}
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-bad, len(checks))
	if bad > 0 {
		os.Exit(1)
	}
}
func checkCycle() error {
	g, _ := nodes("a", "b", "c")
	edges := map[[2]graph.ID]bool{{"a", "b"}: true, {"b", "c"}: true, {"c", "a"}: true}
	for e := range edges {
		_ = g.AddEdge(e[0], e[1])
	}
	var ce *graph.CycleError
	if err := g.Check(); !errors.As(err, &ce) || !errors.Is(err, fail.ErrCycle) {
		return fmt.Errorf("want CycleError, got %v", err)
	}
	p := ce.Path
	if len(p) < 2 || p[0] != p[len(p)-1] {
		return fmt.Errorf("path not closed: %v", p)
	}
	for i := 0; i+1 < len(p); i++ {
		if !edges[[2]graph.ID{p[i], p[i+1]}] {
			return fmt.Errorf("edge %s->%s not in input", p[i], p[i+1])
		}
	}
	return nil
}
func checkPanic() error {
	res := exec.Run(context.Background(), func(context.Context) error { panic("boom-42") })
	var pe *fail.PanicError
	converted := errors.Is(res.Err, fail.ErrPanic) && errors.As(res.Err, &pe) && pe.Value == "boom-42"
	return want(converted, "panic not converted with payload: %v", res.Err)
}
func checkShuffle() error {
	edges := [][2]graph.ID{{"a", "c"}, {"b", "c"}, {"c", "d"}, {"a", "e"}, {"d", "f"}, {"e", "f"}}
	base := ""
	for i := 0; i < 20; i++ {
		g, fns := nodes("a", "b", "c", "d", "e", "f")
		for _, j := range rand.New(rand.NewSource(int64(i))).Perm(len(edges)) {
			_ = g.AddEdge(edges[j][0], edges[j][1])
		}
		if s := run(g, fns, fail.FastFail, 3).String(); i == 0 {
			base = s
		} else if s != base {
			return fmt.Errorf("run %d differs", i)
		}
	}
	return nil
}
func checkMultiFail() error {
	errF1, errF2 := errors.New("e1"), errors.New("e2")
	g, fns := nodes("f1", "f2", "d1", "d2")
	_ = g.AddEdge("f1", "d1")
	_ = g.AddEdge("f2", "d2")
	fns["f1"] = func(context.Context) error { return errF1 }
	fns["f2"] = func(context.Context) error { return errF2 }
	rep := run(g, fns, fail.FastFail, 2)
	for id, w := range map[graph.ID]error{"f1": errF1, "f2": errF2} {
		if e := ent(rep, id); e.Status != fail.Failed || !errors.Is(e.Err, w) {
			return fmt.Errorf("%s=%v, want failed %v", id, e.Status, w)
		}
	}
	return nil
}
func checkLateWrite() error {
	g, fns := nodes("slow", "bad")
	fns["slow"] = func(context.Context) error { time.Sleep(150 * time.Millisecond); return nil }
	fns["bad"] = func(context.Context) error { return errA }
	e := ent(run(g, fns, fail.FastFail, 2), "slow")
	return want(e.Status == fail.Canceled, "slow=%v, want canceled (late write discarded)", e.Status)
}
func checkGoroutines() error {
	base := runtime.NumGoroutine()
	g, fns := abc()
	run(g, fns, fail.BestEffort, 4)
	for i := 0; i < 100; i++ {
		if runtime.NumGoroutine() <= base {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("goroutines %d > baseline %d", runtime.NumGoroutine(), base)
}
