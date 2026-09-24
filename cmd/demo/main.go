// Command demo verifies the operator latency DAG end to end and prints OK/FAIL per check.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/dag"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", status, name)
}

type op struct {
	n string
	l int64
}

var fiveOps = []op{{"S", 0}, {"A", 30}, {"A2", 30}, {"B", 50}, {"T", 0}}
var fiveEdges = [][2]string{{"S", "A"}, {"S", "B"}, {"A", "A2"}, {"B", "T"}, {"A2", "T"}}

func mkAPI(ops []op, edges [][2]string) *api.Pipeline {
	p := api.New()
	for _, o := range ops {
		_ = p.Add(o.n, o.l)
	}
	for _, e := range edges {
		_ = p.Link(e[0], e[1])
	}
	return p
}
func mkDAG(ops []op, edges [][2]string) *dag.Graph {
	g := dag.New()
	for _, o := range ops {
		_ = g.Add(o.n, o.l)
	}
	for _, e := range edges {
		_ = g.Link(e[0], e[1])
	}
	return g
}

func brute(g *dag.Graph) int64 {
	source, sink, _ := g.Endpoints()
	best := int64(-1)
	var dfs func(n string, sum int64)
	dfs = func(n string, sum int64) {
		sum += g.Lat(n)
		if n == sink {
			best = max(best, sum)
			return
		}
		for _, m := range g.Next(n) {
			dfs(m, sum)
		}
	}
	dfs(source, 0)
	return best
}
func chainAPI(m int) *api.Pipeline {
	var ops []op
	var edges [][2]string
	for i := 0; i < m; i++ {
		ops = append(ops, op{fmt.Sprintf("n%d", i), 1})
		if i > 0 {
			edges = append(edges, [2]string{ops[i-1].n, ops[i].n})
		}
	}
	return mkAPI(ops, edges)
}
func main() {
	dist, _ := mkDAG(fiveOps, fiveEdges).Longest()
	want := map[string]int64{"S": 0, "A": 30, "B": 50, "A2": 60, "T": 60}
	ok := len(dist) == len(want)
	for n, d := range want {
		ok = ok && dist[n] == d
	}
	check("dist rows S=0 A=30 B=50 A2=60 T=60", ok)

	e2e, err1 := mkAPI(fiveOps, fiveEdges).EndToEnd()
	bn, blat, err2 := mkAPI(fiveOps, fiveEdges).Bottleneck()
	check("EndToEnd=60 Bottleneck=A(30)", err1 == nil && err2 == nil && e2e == 60 && bn == "A" && blat == 30)
	check("EndToEnd matches brute enumeration", e2e == brute(mkDAG(fiveOps, fiveEdges)))

	tie := mkAPI([]op{{"S", 5}, {"M1", 10}, {"M2", 10}, {"T", 5}},
		[][2]string{{"S", "M1"}, {"S", "M2"}, {"M1", "T"}, {"M2", "T"}})
	tn, tl, _ := tie.Bottleneck()
	check("bottleneck tie breaks to lexicographic M1(10)", tn == "M1" && tl == 10)

	p := mkAPI(fiveOps, fiveEdges)
	before, _ := p.EndToEnd()
	rej := []error{p.Add("A", 1), p.Add("X", -1), p.Link("A", "ghost"), p.Link("T", "S"), p.Link("A", "A")}
	ok = errors.Is(rej[0], dag.ErrDuplicate) && errors.Is(rej[1], dag.ErrNegative) &&
		errors.Is(rej[2], dag.ErrUnknown) && errors.Is(rej[3], dag.ErrCycle) && errors.Is(rej[4], dag.ErrCycle)
	kinds := []error{dag.ErrDuplicate, dag.ErrNegative, dag.ErrUnknown, dag.ErrCycle}
	for i := range kinds {
		for j := range kinds {
			ok = ok && (i == j || !errors.Is(kinds[i], kinds[j]))
		}
	}
	check("four distinct sentinel errors", ok)
	after, _ := p.EndToEnd()
	check("rejected ops leave state unchanged", before == after && after == 60)

	ok = true
	for _, m := range []int{100, 1000, 10000} { // single topo pass, not O(m^2)
		v, err := chainAPI(m).EndToEnd()
		ok = ok && err == nil && v == int64(m)
	}
	check("chain m=100..10000 EndToEnd=m (linear relaxations)", ok)

	shared := mkAPI(fiveOps, fiveEdges) // concurrent read-only queries must agree
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan bool, 64*50)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for k := 0; k < 50; k++ {
				v, _ := shared.EndToEnd()
				n, l, _ := shared.Bottleneck()
				results <- v == 60 && n == "A" && l == 30 && shared.SelfCheck() == nil
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	ok = true
	for r := range results {
		ok = ok && r
	}
	check("64 goroutines concurrent reads identical", ok)

	if failed {
		os.Exit(1)
	}
}
