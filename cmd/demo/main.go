package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/api"
	"ontology/sw"
	"ontology/wg"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

// runSW 是演示用的简版 Stoer-Wagner；pickMin、edgeCut 为故障注入开关。
func runSW(w [4][4]int64, pickMin, edgeCut bool) (phases []int64, min, last int64) {
	alive := []int{0, 1, 2, 3}
	for len(alive) > 1 {
		inA := map[int]bool{alive[0]: true}
		cur := map[int]int64{}
		for _, v := range alive[1:] {
			cur[v] = w[alive[0]][v]
		}
		s, t := alive[0], alive[0]
		var cut int64
		for len(inA) < len(alive) {
			best := -1
			for _, v := range alive {
				if !inA[v] && (best == -1 || better(cur[v], cur[best], v, best, pickMin)) {
					best = v
				}
			}
			s, t = t, best
			cut = cur[best]
			inA[best] = true
			for _, v := range alive {
				if !inA[v] {
					cur[v] += w[best][v]
				}
			}
		}
		if edgeCut {
			cut = w[s][t]
		}
		phases = append(phases, cut)
		for _, v := range alive {
			if v != s && v != t {
				w[s][v] += w[t][v]
				w[v][s] = w[s][v]
			}
		}
		w[s][t], w[t][s] = 0, 0
		next := alive[:0]
		for _, v := range alive {
			if v != t {
				next = append(next, v)
			}
		}
		alive = next
	}
	min, last = phases[0], phases[len(phases)-1]
	for _, c := range phases {
		if c < min {
			min = c
		}
	}
	return
}

func better(cw, bw int64, v, best int, pickMin bool) bool {
	if cw != bw {
		if pickMin {
			return cw < bw
		}
		return cw > bw
	}
	return v < best
}

func fourEdges() [4][4]int64 {
	var w [4][4]int64
	for _, e := range [][3]int64{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}} {
		w[e[0]][e[1]], w[e[1]][e[0]] = e[2], e[2]
	}
	return w
}

func main() {
	ok := true
	g4, _ := wg.New(4)
	for _, e := range [][3]int64{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}} {
		_ = g4.AddEdge(int(e[0]), int(e[1]), e[2])
	}
	check("sw: four-edge graph mincut == 1", sw.MinCut(g4) == 1)

	phases, min, last := runSW(fourEdges(), false, false)
	check("sw: phase cuts == [1 6 8]", fmt.Sprint(phases) == "[1 6 8]" && min == 1)
	check("trap A (last phase only) == 8", last == 8)
	_, minB, _ := runSW(fourEdges(), true, false)
	check("trap B (min-MAS) == 8", minB == 8)
	_, minC, _ := runSW(fourEdges(), false, true)
	check("trap C (s-t edge as cut) == 0", minC == 0)

	ag, _ := api.New(4)
	for _, e := range [][3]int64{{0, 1, 3}, {1, 2, 2}, {0, 2, 5}, {2, 3, 1}} {
		_ = ag.AddEdge(int(e[0]), int(e[1]), e[2])
	}
	errs := []error{ag.AddEdge(0, 4, 1), ag.AddEdge(2, 2, 1), ag.AddEdge(1, 0, 5)}
	_, errNew := api.New(1)
	ok = errors.Is(errNew, api.ErrTooFewNodes) &&
		errors.Is(errs[0], api.ErrNodeOutOfRange) &&
		errors.Is(errs[1], api.ErrSelfLoop) &&
		errors.Is(errs[2], api.ErrDuplicateEdge)
	ok = ok && errs[0] != errs[1] && errs[1] != errs[2] && errs[0] != errs[2]
	check("api: 4 distinct decidable errors", ok)
	cut, err := ag.MinCut()
	check("api: state intact after rejects", err == nil && ag.EdgeCount() == 4 && cut == 1)

	chain, _ := api.New(10000)
	for i := 0; i+1 < 10000; i++ {
		_ = chain.AddEdge(i, i+1, 1)
	}
	ccut, _ := chain.MinCut()
	check("api: chain-10000 mincut == 1 (heap, no full scan)", ccut == 1)

	const P = 8
	res := make(chan int64, P)
	for i := 0; i < P; i++ {
		go func() { c, _ := ag.MinCut(); res <- c }()
	}
	ok = true
	for i := 0; i < P; i++ {
		ok = ok && <-res == 1
	}
	check("api: concurrent MinCut consistent", ok)

	check("api: SelfCheck", ag.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
