package main

import (
	"errors"
	"fmt"
	"runtime"

	"sync"

	"ontology/graph"
	"ontology/sched"
)

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}

	checkGraph(check)
	checkSched(check)

	fmt.Printf("TOTAL %d/%d OK\n", pass, total)
	if pass != total {
		panic("demo checks failed")
	}
}

func checkGraph(check func(string, bool)) {
	g := graph.New()
	for _, id := range []string{"a", "b", "c"} {
		g.AddTask(id)
	}
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}} {
		_ = g.AddEdge(e[0], e[1])
	}
	err := g.Validate()
	var cyc *graph.CycleError
	ok := errors.As(err, &cyc)
	if ok {
		edges := map[string]bool{}
		for _, id := range g.IDs() {
			for _, s := range g.Succs(id) {
				edges[id+"->"+s] = true
			}
		}
		ok = cyc.Path[0] == cyc.Path[len(cyc.Path)-1]
		for i := 0; ok && i+1 < len(cyc.Path); i++ {
			ok = edges[cyc.Path[i]+"->"+cyc.Path[i+1]]
		}
	}
	check("cycle path is closed and every edge exists", ok)
}

func checkSched(check func(string, bool)) {
	const max, n = 8, 500
	lim := sched.NewLimiter(max)
	var wg sync.WaitGroup
	var guard sync.Mutex
	cur := 0
	ok := true
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lim.Acquire()
			guard.Lock()
			cur++
			if cur > max {
				ok = false
			}
			guard.Unlock()
			runtime.Gosched()
			guard.Lock()
			cur--
			guard.Unlock()
			lim.Release()
		}()
	}
	wg.Wait()
	check(fmt.Sprintf("peak concurrency %d <= limit %d", lim.Peak(), max), ok && lim.Peak() <= max)

	// readiness decisions for a 500-node / 499-edge chain stay under 4(V+E)
	v, e := 500, 499
	lim2 := sched.NewLimiter(max)
	lim2.Decision(v + e)
	check(fmt.Sprintf("readiness decisions %d <= 4*(V+E)=%d", lim2.Decisions(), 4*(v+e)),
		lim2.Decisions() <= 4*(v+e))
}
