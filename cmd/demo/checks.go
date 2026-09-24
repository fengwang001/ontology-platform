package main

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
	"ontology/sched"
)

func checkConcurrencyCapAndDecisions() {
	g := graph.New()
	for i := 0; i < 500; i++ {
		g.AddNode(fmt.Sprintf("n%03d", i))
	}
	release := make(chan struct{})
	var mu sync.Mutex
	inFlight, maxSeen := 0, 0
	tasks := map[string]sched.TaskFunc{}
	for _, id := range g.Nodes() {
		tasks[id] = func(context.Context) error {
			mu.Lock()
			inFlight++
			if inFlight > maxSeen {
				maxSeen = inFlight
			}
			mu.Unlock()
			<-release
			mu.Lock()
			inFlight--
			mu.Unlock()
			return nil
		}
	}
	tr := fail.NewTracker(g)
	s := sched.New(g, tasks, &demoHooks{tr: tr}, 8)
	close(release)
	s.Run(context.Background())
	check("concurrency peak never exceeds limit 8", s.Peak() <= 8 && maxSeen <= 8)
	check(fmt.Sprintf("ready decisions %d <= 4*(V+E)=%d", s.Decisions(), 4*500),
		s.Decisions() <= 4*500)
}

func checkGoroutineBaseline() {
	base := stableGoroutines()
	for range 3 {
		_, _ = exec.New(demoABCDGraph(), demoABCDTasks(
			func(context.Context) error { return nil }),
			exec.BestEffort, 8).Run(context.Background())
	}
	check("goroutines return to baseline after all paths",
		stableGoroutines() <= base+1)
}

func stableGoroutines() int {
	prev, cur := 0, runtime.NumGoroutine()
	for range 5 {
		time.Sleep(15 * time.Millisecond)
		prev, cur = cur, runtime.NumGoroutine()
		if prev == cur {
			return cur
		}
	}
	return cur
}

func checkDeterministicReport() {
	edges := [][2]string{{"A", "B"}, {"B", "C"}, {"A", "D"}, {"D", "F"},
		{"B", "E"}, {"E", "G"}, {"C", "G"}}
	ids := []string{"A", "B", "C", "D", "E", "F", "G"}
	build := func(order [][2]string) *graph.Graph {
		g := graph.New()
		for _, n := range ids {
			g.AddNode(n)
		}
		for _, e := range order {
			_ = g.AddEdge(e[0], e[1])
		}
		return g
	}
	tasks := exec.TaskMap{"A": func(context.Context) error { return fmt.Errorf("a-fail") }}
	for _, n := range ids[1:] {
		tasks[n] = func(context.Context) error { return nil }
	}
	baseline, ok := "", true
	for iter := 0; iter < 20; iter++ {
		order := append([][2]string{}, edges...)
		if iter > 0 {
			r := rand.New(rand.NewSource(int64(iter)))
			r.Shuffle(len(order), func(i, j int) {
				order[i], order[j] = order[j], order[i]
			})
		}
		res, err := exec.New(build(order), tasks, exec.BestEffort, 8).
			Run(context.Background())
		if err != nil {
			ok = false
			break
		}
		rep := report.Render(res.States)
		if iter == 0 {
			baseline = rep
		} else if rep != baseline {
			ok = false
		}
	}
	check("report byte-identical over 20 shuffled edge orders", ok)
}
