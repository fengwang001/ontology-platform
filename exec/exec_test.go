package exec_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"
	"time"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
)

func ok() exec.Fn { return func(context.Context) error { return nil } }

func slow(d time.Duration) exec.Fn {
	return func(ctx context.Context) error {
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func slowWriteLate(d time.Duration, written *bool) exec.Fn {
	return func(ctx context.Context) error {
		<-time.After(d) // 故意忽略 ctx：取消后仍继续并尝试写回
		*written = true
		return nil
	}
}

func failf(msg string) exec.Fn { return func(context.Context) error { return errors.New(msg) } }

func panicFn(v any) exec.Fn {
	return func(context.Context) error { panic(v) }
}

func index(results []exec.TaskResult, id string) int {
	for i := range results {
		if results[i].ID == id {
			return i
		}
	}
	return -1
}

// TestFourStates 表驱动：两模式下 A→B→C（A 失败）+ 并行慢任务 S 的状态分布。
func TestFourStates(t *testing.T) {
	build := func() (*graph.Graph, map[string]exec.Fn) {
		g := graph.New()
		for _, id := range []string{"A", "B", "C", "S"} {
			g.AddTask(id)
		}
		_ = g.AddEdge("A", "B")
		_ = g.AddEdge("B", "C")
		fns := map[string]exec.Fn{
			"A": failf("boom"),
			"B": ok(),
			"C": ok(),
			"S": slow(200 * time.Millisecond),
		}
		return g, fns
	}
	cases := []struct {
		name     string
		mode     fail.Mode
		wantS    fail.Status
		sStarted bool
		cRoot    string
		bRoot    string
	}{
		{"FailFast", fail.FailFast, fail.Canceled, true, "A", "A"},
		{"BestEffort", fail.BestEffort, fail.Success, true, "A", "A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, fns := build()
			res, err := exec.New(g, fns, exec.WithMode(tc.mode), exec.WithConcurrency(4)).
				Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]fail.Status{
				"A": fail.Failed, "B": fail.Skipped, "C": fail.Skipped, "S": tc.wantS,
			}
			for _, r := range res {
				if r.Status != want[r.ID] {
					t.Errorf("%s: %s=%s want %s", tc.name, r.ID, r.Status, want[r.ID])
				}
			}
			c := res[index(res, "C")]
			b := res[index(res, "B")]
			s := res[index(res, "S")]
			if !errors.Is(c.Err, fail.RootCause(tc.cRoot)) ||
				errors.Is(c.Err, fail.RootCause("B")) && tc.cRoot != "B" {
				t.Fatalf("C 跳过原因未指向 A: %v", c.Err)
			}
			if !errors.Is(b.Err, fail.RootCause(tc.bRoot)) {
				t.Fatalf("B 跳过原因未指向 A: %v", b.Err)
			}
			if s.Started != tc.sStarted || s.Status == fail.Skipped && tc.wantS == fail.Canceled {
				t.Fatalf("慢任务区分错误: started=%v status=%s", s.Started, s.Status)
			}
		})
	}
}

// TestFaults 表驱动：环、panic、多失败、取消后写回。
func TestFaults(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"cycle", func(t *testing.T) {
			g := graph.New()
			g.AddTask("a")
			g.AddTask("b")
			_ = g.AddEdge("a", "b")
			_ = g.AddEdge("b", "a")
			_, err := exec.New(g, map[string]exec.Fn{"a": ok(), "b": ok()}).Run(context.Background())
			if !errors.Is(err, fail.ErrCycle) {
				t.Fatalf("want ErrCycle, got %v", err)
			}
		}},
		{"panic", func(t *testing.T) {
			g := graph.New()
			g.AddTask("p")
			res, err := exec.New(g, map[string]exec.Fn{"p": panicFn("kaboom")}).Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			r := res[0]
			if r.Status != fail.Failed || !errors.Is(r.Err, fail.ErrPanic) {
				t.Fatalf("panic 未捕获为失败: status=%s err=%v", r.Status, r.Err)
			}
		}},
		{"multi-fail", func(t *testing.T) {
			g := graph.New()
			for _, id := range []string{"F1", "F2", "D1", "D2"} {
				g.AddTask(id)
			}
			_ = g.AddEdge("F1", "D1")
			_ = g.AddEdge("F2", "D2")
			fns := map[string]exec.Fn{
				"F1": failf("e1"), "F2": failf("e2"), "D1": ok(), "D2": ok(),
			}
			res, _ := exec.New(g, fns, exec.WithMode(fail.BestEffort)).Run(context.Background())
			failed := map[string]bool{}
			for _, r := range res {
				if r.Status == fail.Failed {
					failed[r.ID] = true
				}
			}
			if !failed["F1"] || !failed["F2"] {
				t.Fatalf("未记录全部失败: %v", failed)
			}
			if !errors.Is(res[index(res, "D1")].Err, fail.RootCause("F1")) ||
				!errors.Is(res[index(res, "D2")].Err, fail.RootCause("F2")) {
				t.Fatal("传播原因未指向各自最早失败")
			}
		}},
		{"cancel-late-write", func(t *testing.T) {
			written := false
			aStarted := make(chan struct{})
			sCanFinish := make(chan struct{})
			g := graph.New()
			for _, id := range []string{"A", "S"} {
				g.AddTask(id)
			}
			fns := map[string]exec.Fn{
				"A": func(context.Context) error {
					close(aStarted)
					<-sCanFinish // 等 S 确实在跑后再失败
					return failf("fast")(nil)
				},
				"S": func(ctx context.Context) error {
					<-aStarted
					close(sCanFinish)
					return slowWriteLate(150*time.Millisecond, &written)(ctx)
				},
			}
			res, _ := exec.New(g, fns, exec.WithConcurrency(2)).Run(context.Background())
			r := res[index(res, "S")]
			if r.Status != fail.Canceled {
				t.Fatalf("S=%s want CANCELED", r.Status)
			}
			time.Sleep(200 * time.Millisecond)
			if !written {
				t.Fatal("慢任务应当在取消后仍继续并写回")
			}
			// 慢任务最终返回了成功，但终态必须仍是 CANCELED 而非 SUCCESS。
			r2 := res[index(res, "S")]
			if r2.Status != fail.Canceled {
				t.Fatalf("迟到成功写回未被丢弃，状态被改为 %s", r2.Status)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t) })
	}
}

func TestBoundariesAndDeterminism(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		res, err := exec.New(graph.New(), map[string]exec.Fn{}).Run(context.Background())
		if err != nil || len(res) != 0 {
			t.Fatalf("empty: %v %v", res, err)
		}
	})
	t.Run("chain1000", func(t *testing.T) {
		g := graph.New()
		fns := map[string]exec.Fn{}
		for i := 0; i < 1000; i++ {
			id := fmt.Sprintf("n%04d", i)
			g.AddTask(id)
			fns[id] = ok()
			if i > 0 {
				_ = g.AddEdge(fmt.Sprintf("n%04d", i-1), id)
			}
		}
		res, err := exec.New(g, fns).Run(context.Background())
		if err != nil || len(res) != 1000 {
			t.Fatalf("chain1000: err=%v n=%d", err, len(res))
		}
		for _, r := range res {
			if r.Status != fail.Success {
				t.Fatalf("chain task %s = %s", r.ID, r.Status)
			}
		}
	})
	t.Run("deterministic", func(t *testing.T) {
		var ref string
		rnd := rand.New(rand.NewSource(1))
		var rmu sync.Mutex
		for iter := 0; iter < 20; iter++ {
			g := graph.New()
			ids := []string{"a", "b", "c", "d", "e"}
			for _, id := range ids {
				g.AddTask(id)
			}
			edges := [][2]string{{"a", "c"}, {"b", "c"}, {"c", "d"}, {"c", "e"}}
			rnd.Shuffle(len(edges), func(i, j int) { edges[i], edges[j] = edges[j], edges[i] })
			for _, e2 := range edges {
				_ = g.AddEdge(e2[0], e2[1])
			}
			fns := map[string]exec.Fn{}
			for _, id := range ids {
				id := id
				fns[id] = func(context.Context) error {
					rmu.Lock()
					d := rnd.Intn(5)
					rmu.Unlock()
					time.Sleep(time.Duration(d) * time.Millisecond)
					return nil
				}
			}
			res, err := exec.New(g, fns, exec.WithConcurrency(3)).Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := render(res)
			if iter == 0 {
				ref = got
			} else if got != ref {
				t.Fatalf("第 %d 次报告不一致:\n%s\nvs\n%s", iter, got, ref)
			}
		}
	})
	t.Run("serial-parallel-same", func(t *testing.T) {
		mk := func() (*graph.Graph, map[string]exec.Fn) {
			g := graph.New()
			for _, id := range []string{"a", "b", "c"} {
				g.AddTask(id)
			}
			_ = g.AddEdge("a", "c")
			_ = g.AddEdge("b", "c")
			return g, map[string]exec.Fn{"a": ok(), "b": ok(), "c": ok()}
		}
		g1, f1 := mk()
		g2, f2 := mk()
		r1, _ := exec.New(g1, f1, exec.WithConcurrency(1)).Run(context.Background())
		r2, _ := exec.New(g2, f2, exec.WithConcurrency(8)).Run(context.Background())
		if render(r1) != render(r2) {
			t.Fatal("串行与并行报告不一致")
		}
	})
}

// TestNoLeak 三种路径后 goroutine 回到基线。
func TestNoLeak(t *testing.T) {
	cases := []struct {
		name string
		mode fail.Mode
	}{
		{"all-success", fail.BestEffort},
		{"failfast", fail.FailFast},
		{"besteffort", fail.BestEffort},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime.GC()
			base := runtime.NumGoroutine()
			for round := 0; round < 5; round++ {
				g := graph.New()
				for _, id := range []string{"A", "B", "C", "S", "S2"} {
					g.AddTask(id)
				}
				_ = g.AddEdge("A", "B")
				_ = g.AddEdge("B", "C")
				fns := map[string]exec.Fn{
					"A": ok(), "B": ok(), "C": ok(),
					"S":  slow(30 * time.Millisecond),
					"S2": slow(30 * time.Millisecond),
				}
				if tc.name != "all-success" {
					fns["A"] = failf("x")
				}
				_, _ = exec.New(g, fns, exec.WithMode(tc.mode), exec.WithConcurrency(4)).
					Run(context.Background())
			}
			deadline := time.Now().Add(2 * time.Second)
			for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
				runtime.GC()
				time.Sleep(10 * time.Millisecond)
			}
			if got := runtime.NumGoroutine(); got > base {
				t.Fatalf("goroutine 泄漏: base=%d now=%d", base, got)
			}
		})
	}
}

func render(res []exec.TaskResult) string {
	out := ""
	for _, r := range res {
		err := ""
		if r.Err != nil {
			err = r.Err.Error()
		}
		out += fmt.Sprintf("%s=%s:%s|", r.ID, r.Status, err)
	}
	return out
}
