package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
	"ontology/sched"
)

var pass, total int

func check(name string, ok bool) {
	total++
	if ok {
		pass++
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
}

func okFn(context.Context) error { return nil }

func find(res []exec.TaskResult, id string) exec.TaskResult {
	for _, r := range res {
		if r.ID == id {
			return r
		}
	}
	return exec.TaskResult{}
}

// mkScene：A→B→C，A 失败；S 为并行慢任务（尊重 ctx）。
func mkScene() (*graph.Graph, map[string]exec.Fn) {
	g := graph.New()
	for _, id := range []string{"A", "B", "C", "S"} {
		g.AddTask(id)
	}
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("B", "C")
	fns := map[string]exec.Fn{
		"A": func(context.Context) error { return errors.New("A-boom") },
		"B": okFn, "C": okFn,
		"S": func(ctx context.Context) error {
			select {
			case <-time.After(300 * time.Millisecond):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	return g, fns
}

func main() {
	g1, f1 := mkScene()
	ff, _ := exec.New(g1, f1, exec.WithMode(fail.FailFast), exec.WithConcurrency(4)).
		Run(context.Background())
	g2, f2 := mkScene()
	be, _ := exec.New(g2, f2, exec.WithMode(fail.BestEffort), exec.WithConcurrency(4)).
		Run(context.Background())
	c := find(ff, "C")
	check("C 的跳过原因指向 A 而非 B",
		errors.Is(c.Err, fail.RootCause("A")) && !errors.Is(c.Err, fail.RootCause("B")))
	check("慢任务为被取消而非被跳过",
		find(ff, "S").Status == fail.Canceled && find(ff, "S").Started)
	check("两模式状态分布差异（S: 取消 vs 成功）",
		find(ff, "S").Status == fail.Canceled && find(be, "S").Status == fail.Success)

	const nTask, maxRun = 500, 8
	s := sched.New(maxRun, nTask)
	var cur, peak, done int32
	go func() {
		for id := range s.Launch() {
			go func(id string) {
				p := atomic.AddInt32(&cur, 1)
				for {
					old := atomic.LoadInt32(&peak)
					if p <= old || atomic.CompareAndSwapInt32(&peak, old, p) {
						break
					}
				}
				atomic.AddInt32(&cur, -1)
				s.Finish(id)
				atomic.AddInt32(&done, 1)
			}(id)
		}
	}()
	for i := 0; i < nTask; i++ {
		s.Enqueue(fmt.Sprintf("t%03d", i))
	}
	for atomic.LoadInt32(&done) < nTask {
		<-s.Done()
		s.Release()
	}
	s.CloseLaunch()
	check(fmt.Sprintf("并发峰值不超上限（观测=%d,计数=%d≤%d）", peak, s.Peak(), maxRun),
		peak <= maxRun && s.Peak() <= maxRun)
	check(fmt.Sprintf("就绪判定 %d ≤ 4(V+E)=%d", s.Decisions(), 4*nTask),
		s.Decisions() <= 4*nTask)

	var ref string
	detOK := true
	rnd := rand.New(rand.NewSource(7))
	var rmu sync.Mutex
	for it := 0; it < 20; it++ {
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
			detOK = false
			break
		}
		txt := report.Render(res)
		if it == 0 {
			ref = txt
		} else if txt != ref {
			detOK = false
		}
	}
	check("打乱边顺序 20 次报告逐字节相同", detOK)

	gc := graph.New()
	for _, id := range []string{"a", "b", "c"} {
		gc.AddTask(id)
	}
	for _, e2 := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}} {
		_ = gc.AddEdge(e2[0], e2[1])
	}
	cyc := gc.Cycle()
	edgeOK := len(cyc) >= 2 && cyc[0] == cyc[len(cyc)-1]
	for i := 0; edgeOK && i+1 < len(cyc); i++ {
		edgeOK = gc.HasEdge(cyc[i], cyc[i+1])
	}
	check("环路径逐边真实且首尾闭合", edgeOK)

	gp := graph.New()
	gp.AddTask("p")
	pr, _ := exec.New(gp, map[string]exec.Fn{"p": func(context.Context) error { panic("kaboom") }}).
		Run(context.Background())
	pr0 := find(pr, "p")
	check("panic 被捕获为 FAILED 且带 ErrPanic",
		pr0.Status == fail.Failed && errors.Is(pr0.Err, fail.ErrPanic))

	gm := graph.New()
	for _, id := range []string{"F1", "F2", "D1", "D2"} {
		gm.AddTask(id)
	}
	_ = gm.AddEdge("F1", "D1")
	_ = gm.AddEdge("F2", "D2")
	mr, _ := exec.New(gm, map[string]exec.Fn{
		"F1": func(context.Context) error { return errors.New("e1") },
		"F2": func(context.Context) error { return errors.New("e2") }, "D1": okFn, "D2": okFn,
	}, exec.WithMode(fail.BestEffort), exec.WithConcurrency(4)).Run(context.Background())
	check("多任务同时失败全部记录且原因各自指源",
		find(mr, "F1").Status == fail.Failed && find(mr, "F2").Status == fail.Failed &&
			errors.Is(find(mr, "D1").Err, fail.RootCause("F1")) &&
			errors.Is(find(mr, "D2").Err, fail.RootCause("F2")))

	var written bool
	aStarted := make(chan struct{})
	sCanFinish := make(chan struct{})
	gl := graph.New()
	gl.AddTask("A")
	gl.AddTask("S")
	lr, _ := exec.New(gl, map[string]exec.Fn{
		"A": func(context.Context) error {
			close(aStarted)
			<-sCanFinish
			return errors.New("fast")
		},
		"S": func(ctx context.Context) error {
			<-aStarted
			close(sCanFinish)
			time.Sleep(150 * time.Millisecond) // 忽略 ctx：取消后仍写回
			written = true
			return nil
		},
	}, exec.WithConcurrency(2)).Run(context.Background())
	lateOK := find(lr, "S").Status == fail.Canceled
	time.Sleep(200 * time.Millisecond)
	check("取消后写回被丢弃且状态保持 CANCELED", lateOK && written &&
		find(lr, "S").Status == fail.Canceled)

	runtime.GC()
	base := runtime.NumGoroutine()
	for k := 0; k < 3; k++ {
		g, f := mkScene()
		_, _ = exec.New(g, f, exec.WithConcurrency(4)).Run(context.Background())
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	check(fmt.Sprintf("goroutine 回基线（base=%d now=%d）", base, runtime.NumGoroutine()),
		runtime.NumGoroutine() <= base)

	mkDiamond := func() (*graph.Graph, map[string]exec.Fn) {
		g := graph.New()
		for _, id := range []string{"a", "b", "c"} {
			g.AddTask(id)
		}
		_ = g.AddEdge("a", "c")
		_ = g.AddEdge("b", "c")
		return g, map[string]exec.Fn{"a": okFn, "b": okFn, "c": okFn}
	}
	gs1, f3 := mkDiamond()
	gs2, f4 := mkDiamond()
	r1, _ := exec.New(gs1, f3, exec.WithConcurrency(1)).Run(context.Background())
	r2, _ := exec.New(gs2, f4, exec.WithConcurrency(8)).Run(context.Background())
	check("并发度 1 串行报告与并行一致", report.Render(r1) == report.Render(r2))

	fmt.Printf("TOTAL: %d/%d\n", pass, total)
	if pass != total {
		fmt.Println("FAIL 有判定未通过")
	}
}
