package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/closure"
	"ontology/graph"
)

var fails int

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK " + name)
		return
	}
	fails++
	fmt.Println("FAIL " + name)
}

func match(eng *api.Engine, gm *graph.Graph) bool { // R 与 gm 的朴素 BFS 参照逐对相同
	want, got := closure.Naive(gm), eng.Pairs()
	bad := len(got) != len(want)
	for _, p := range got {
		bad = bad || !want[p]
	}
	return !bad
}

func main() {
	eng, _ := api.New(10)
	adds := []bool{true, true, true, true, true, false, false, true, false, false}
	edges := [][2]string{{"A", "B"}, {"B", "C"}, {"A", "C"}, {"C", "A"}, {"B", "C"}, {"B", "C"}, {"A", "B"}, {"C", "D"}, {"C", "A"}, {"B", "C"}}
	wantSize := []int{1, 3, 3, 9, 9, 9, 6, 9, 5, 3}
	wantODRE := [][2]int{{-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {-1, -1}, {0, 0}, {9, 6}, {-1, -1}, {9, 5}, {2, 0}}
	g1 := true
	for i, e := range edges {
		od, re := -1, -1
		var err error
		if adds[i] {
			err = eng.AddEdge(e[0], e[1])
		} else {
			od, re, err = eng.RemoveEdge(e[0], e[1])
		}
		g1 = g1 && err == nil && len(eng.Pairs()) == wantSize[i] && od == wantODRE[i][0] && re == wantODRE[i][1]
	}
	ok("十步表|R|与第6/7/9/10步过删再推", g1)
	e2, _ := api.New(10)
	e2.AddEdge("p", "q")
	e2.AddEdge("p", "q")
	od, re, _ := e2.RemoveEdge("p", "q")
	ok("重复加边后删一次可达性保留", od == 0 && re == 0 && e2.Reachable("p", "q"))
	rng := rand.New(rand.NewSource(275))
	e3, _ := api.New(1000)
	g3m := graph.New()
	nodes := []string{"a", "b", "c", "d", "e", "f"}
	g3 := true
	for i := 0; i < 400 && g3; i++ {
		u, v := nodes[rng.Intn(6)], nodes[rng.Intn(6)]
		if rng.Intn(2) == 0 {
			e3.AddEdge(u, v)
			g3m.Add(u, v)
		} else if g3m.Has(u, v) {
			e3.RemoveEdge(u, v)
			g3m.Remove(u, v)
		}
		g3 = match(e3, g3m)
	}
	ok("随机增删与朴素BFS一致", g3)
	_, err0 := api.New(0)
	e4, _ := api.New(1)
	e4.AddEdge("a", "b")
	before := e4.Pairs()
	_, _, err3 := e4.RemoveEdge("a", "c")
	errs := []error{err0, e4.AddEdge("", "x"), err3, e4.AddEdge("c", "d")}
	want := []error{api.ErrBadMaxEdges, api.ErrEmptyNode, api.ErrEdgeNotFound, api.ErrTooManyEdges}
	g4 := true
	for i := range errs {
		for j := range want {
			g4 = g4 && errors.Is(errs[i], want[j]) == (i == j)
		}
	}
	ok("四类可判定错误互不相同", g4)
	g5 := len(e4.Pairs()) == len(before) && e4.Reachable("a", "b")
	_, _, err5 := e4.RemoveEdge("a", "b")
	ok("被拒后状态不变且可继续用", g5 && err5 == nil && e4.AddEdge("c", "d") == nil)
	e6, _ := api.New(20000)
	for i := 0; i < 10000; i++ {
		e6.AddEdge(fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i))
	}
	e6.AddEdge("p", "q")
	e6.AddEdge("q", "s")
	e6.RemoveEdge("q", "s")
	ok("大m下检查数不随m增长(界由TestCheckedBounded钉住)", e6.Reachable("p", "q") && !e6.Reachable("p", "s") && len(e6.Pairs()) == 10001)
	e7, _ := api.New(100)
	for _, ed := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"c", "d"}} {
		e7.AddEdge(ed[0], ed[1])
	}
	base := e7.Pairs()
	e8, _ := api.New(100)
	g8m := graph.New()
	ops8 := [][2]string{{"m", "n"}, {"n", "o"}, {"o", "p"}, {"m", "o"}, {"p", "m"}}
	var bad atomic.Bool
	var wg sync.WaitGroup
	start := make(chan struct{})
	worker := func(write bool) {
		defer wg.Done()
		<-start
		if write {
			for i := 0; i < 120; i++ {
				ed := ops8[i%len(ops8)]
				if i%2 == 0 {
					e8.AddEdge(ed[0], ed[1])
					g8m.Add(ed[0], ed[1])
				} else if g8m.Has(ed[0], ed[1]) {
					e8.RemoveEdge(ed[0], ed[1])
					g8m.Remove(ed[0], ed[1])
				}
			}
			return
		}
		for j := 0; j < 50; j++ {
			e8.Pairs()
			if !reflect.DeepEqual(e7.Pairs(), base) {
				bad.Store(true)
				return
			}
		}
	}
	for i := 0; i < 9; i++ {
		wg.Add(1)
		go worker(i == 8)
	}
	close(start)
	wg.Wait()
	ok("并发读结果一致且写后R=朴素BFS", !bad.Load() && match(e8, g8m))
	ok("SelfCheck", eng.SelfCheck() == nil)
	if fails > 0 {
		os.Exit(1)
	}
}
