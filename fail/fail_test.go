package fail_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/sched"
)

var errA = errors.New("a broke")

// chainPlusSlow builds A->B->C plus an independent slow task S.
func chainPlusSlow(t *testing.T, slow exec.Func) (*graph.Graph, map[string]exec.Func) {
	t.Helper()
	g := graph.New()
	for _, id := range []string{"a", "b", "c", "s"} {
		g.AddTask(id)
	}
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}} {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan struct{})
	fns := map[string]exec.Func{
		"a": func(context.Context) error { <-started; return errA },
		"b": func(context.Context) error { return nil },
		"c": func(context.Context) error { return nil },
		"s": func(ctx context.Context) error { close(started); return slow(ctx) },
	}
	return g, fns
}

func skipRoot(t *testing.T, err error) string {
	t.Helper()
	var se *fail.SkipError
	if !errors.Is(err, fail.ErrSkipped) || !errors.As(err, &se) {
		t.Fatalf("reason %v is not a SkipError", err)
	}
	return se.Root
}

func TestFourStatesFailFast(t *testing.T) {
	g, fns := chainPlusSlow(t, func(ctx context.Context) error {
		<-ctx.Done()
		return nil // late write-back: must be discarded
	})
	rep, err := sched.New(4, sched.FailFast).Run(g, fns)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id    string
		state fail.State
		root  string // "" => check original/cancel error instead
	}{
		{"a", fail.Failed, ""},
		{"b", fail.Skipped, "a"},
		{"c", fail.Skipped, "a"},
		{"s", fail.Canceled, ""},
	}
	for _, tc := range cases {
		e, ok := rep.Entry(tc.id)
		if !ok || e.State != tc.state {
			t.Fatalf("%s: state = %v; want %v", tc.id, e.State, tc.state)
		}
		if tc.root != "" && skipRoot(t, e.Err) != tc.root {
			t.Fatalf("%s: skip root = %s; want %s", tc.id, skipRoot(t, e.Err), tc.root)
		}
	}
	if e, _ := rep.Entry("a"); !errors.Is(e.Err, errA) {
		t.Fatalf("a: reason %v; want original errA", e.Err)
	}
	if e, _ := rep.Entry("s"); !errors.Is(e.Err, fail.ErrCanceled) {
		t.Fatalf("s: reason %v; want ErrCanceled (not skipped)", e.Err)
	}
}

func TestBestEffortDistribution(t *testing.T) {
	g, fns := chainPlusSlow(t, func(context.Context) error {
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	rep, err := sched.New(4, sched.BestEffort).Run(g, fns)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Count(fail.Success) != 1 || rep.Count(fail.Failed) != 1 ||
		rep.Count(fail.Skipped) != 2 || rep.Count(fail.Canceled) != 0 {
		t.Fatalf("distribution = %d/%d/%d/%d; want 1/1/2/0",
			rep.Count(fail.Success), rep.Count(fail.Failed),
			rep.Count(fail.Skipped), rep.Count(fail.Canceled))
	}
	if e, _ := rep.Entry("s"); e.State != fail.Success {
		t.Fatalf("s: state = %v; want success in best-effort", e.State)
	}
	if e, _ := rep.Entry("c"); skipRoot(t, e.Err) != "a" {
		t.Fatalf("c: skip root = %v; want a", e.Err)
	}
}

func TestMultiFailure(t *testing.T) {
	g := graph.New()
	for _, id := range []string{"a1", "b1", "a2", "b2"} {
		g.AddTask(id)
	}
	for _, e := range [][2]string{{"a1", "b1"}, {"a2", "b2"}} {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatal(err)
		}
	}
	err1, err2 := errors.New("first"), errors.New("second")
	fns := map[string]exec.Func{
		"a1": func(context.Context) error { return err1 },
		"a2": func(context.Context) error { return err2 },
		"b1": func(context.Context) error { return nil },
		"b2": func(context.Context) error { return nil },
	}
	rep, err := sched.New(4, sched.BestEffort).Run(g, fns)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Count(fail.Failed) != 2 {
		t.Fatalf("failed count = %d; want both failures recorded", rep.Count(fail.Failed))
	}
	for id, want := range map[string]error{"a1": err1, "a2": err2} {
		if e, _ := rep.Entry(id); !errors.Is(e.Err, want) {
			t.Fatalf("%s: reason %v; want %v", id, e.Err, want)
		}
	}
	for id, root := range map[string]string{"b1": "a1", "b2": "a2"} {
		if e, _ := rep.Entry(id); skipRoot(t, e.Err) != root {
			t.Fatalf("%s: skip root = %v; want %s", id, e.Err, root)
		}
	}
}

func TestPanicAndLateWrite(t *testing.T) {
	cases := []struct {
		name string
		mode sched.Mode
		id   string
		want fail.State
		is   error
	}{
		{"panic captured", sched.BestEffort, "p", fail.Failed, exec.ErrPanic},
		{"late write dropped", sched.FailFast, "s", fail.Canceled, fail.ErrCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := graph.New()
			g.AddTask(tc.id)
			g.AddTask("x")
			fns := map[string]exec.Func{"x": func(context.Context) error { return nil }}
			if tc.id == "p" {
				fns["p"] = func(context.Context) error { panic("kaboom") }
			} else {
				started := make(chan struct{})
				fns["s"] = func(context.Context) error {
					close(started)
					time.Sleep(30 * time.Millisecond) // ignores cancel, writes back late
					return nil
				}
				fns["x"] = func(context.Context) error { <-started; return errA }
			}
			rep, err := sched.New(4, tc.mode).Run(g, fns)
			if err != nil {
				t.Fatal(err)
			}
			e, _ := rep.Entry(tc.id)
			if e.State != tc.want || !errors.Is(e.Err, tc.is) {
				t.Fatalf("%s: (%v, %v); want (%v, %v)", tc.id, e.State, e.Err, tc.want, tc.is)
			}
		})
	}
}
