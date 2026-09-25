package main

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"

	"ontology/api"
	"ontology/ord"
	"ontology/ra"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func tk(ts, pid int) ord.Ticket { return ord.Ticket{TS: ts, PID: pid} }

func enterables(g *api.Group, n int) []int {
	var got []int
	for pid := 0; pid < n; pid++ {
		if ok, _ := g.Enterable(pid); ok {
			got = append(got, pid)
		}
	}
	return got
}

func main() {
	p2, p1, p3 := tk(3, 2), tk(5, 1), tk(5, 3)
	check("ord total order (3,2)<(5,1)<(5,3)", ord.Less(p2, p1) && ord.Less(p1, p3))

	rows := []ra.Verdict{ra.Judge(false, p1, p2), ra.Judge(false, p3, p2),
		ra.Judge(true, p2, p1), ra.Judge(false, p3, p1),
		ra.Judge(true, p2, p3), ra.Judge(true, p1, p3)}
	want := []ra.Verdict{ra.Grant, ra.Grant, ra.Defer, ra.Grant, ra.Defer, ra.Defer}
	g := api.New(4)
	must(g.Request(2, 3))
	must(g.Request(1, 5))
	must(g.Request(3, 5))
	g.Resolve()
	cur := enterables(g, 4)
	seven := len(rows) == len(want)
	for i := range rows {
		seven = seven && rows[i] == want[i]
	}
	check("seven-row OK/defer judgments", seven && len(cur) == 1 && cur[0] == 2)

	order := []int{}
	for range 3 { // mutex + no deadlock: exactly one enterable at every step
		cur = enterables(g, 4)
		if len(cur) != 1 {
			break
		}
		order = append(order, cur[0])
		must(g.Exit(cur[0]))
	}
	check("entry order P2->P1->P3", fmt.Sprint(order) == "[2 1 3]")
	check("mutex + no deadlock", len(order) == 3)

	bad := api.New(2)
	e0, e1, e2 := bad.Request(9, 0), bad.Request(0, -1), bad.Exit(1)
	must(bad.Request(0, 1))
	e3 := bad.Request(0, 2)
	check("four distinct decidable errors",
		e0 == api.ErrPIDOutOfRange && e1 == api.ErrNegativeTS && e2 == api.ErrNotInterested &&
			e3 == api.ErrDuplicateRequest && e0 != e1 && e1 != e2 && e2 != e3)
	check("rejected ops leave state untouched", bad.Request(1, 7) == nil)
	check("api SelfCheck", api.New(1).SelfCheck() == nil)

	scale := true // large m: unique enterable is the total-order minimum at every scale
	for _, m := range []int{100, 1000, 10000} {
		h := api.New(m)
		ts := make([]int, m)
		for pid := range ts {
			ts[pid] = rand.Intn(m * 3)
			must(h.Request(pid, ts[pid]))
		}
		h.Resolve()
		now := enterables(h, m)
		min := 0
		for pid := 1; pid < m; pid++ {
			if ord.Less(tk(ts[pid], pid), tk(ts[min], min)) {
				min = pid
			}
		}
		scale = scale && len(now) == 1 && now[0] == min
	}
	check("large-m collection stays O(1) (pinned by ra test)", scale)

	n := 200
	c := api.New(n)
	cts := make([]int, n)
	var wg sync.WaitGroup
	for pid := 0; pid < n; pid++ {
		cts[pid] = rand.Intn(500)
		wg.Add(1)
		go func(pid, t int) { defer wg.Done(); must(c.Request(pid, t)) }(pid, cts[pid])
	}
	wg.Wait()
	c.Resolve()
	expect := make([]int, n)
	for i := range expect {
		expect[i] = i
	}
	sort.Slice(expect, func(a, b int) bool { return ord.Less(tk(cts[expect[a]], expect[a]), tk(cts[expect[b]], expect[b])) })
	got, single := []int{}, true
	for range n {
		var rw sync.WaitGroup
		viol := make(chan int, 8)
		for r := 0; r < 8; r++ { // concurrent readers: at most one enterable, ever
			rw.Add(1)
			go func() { defer rw.Done(); viol <- len(enterables(c, n)) }()
		}
		rw.Wait()
		close(viol)
		for v := range viol {
			single = single && v <= 1
		}
		cur = enterables(c, n)
		if len(cur) != 1 {
			single = false
			break
		}
		got = append(got, cur[0])
		must(c.Exit(cur[0]))
	}
	check("concurrent order matches total order, <=1 enterable",
		single && fmt.Sprint(got) == fmt.Sprint(expect))

	if failed {
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
