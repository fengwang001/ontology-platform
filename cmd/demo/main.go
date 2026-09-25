package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/mrg"
	"ontology/run"
)

var failures int

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s: %s\n", name, status)
}

func runShape(runs []run.Run) [][]int {
	out := make([][]int, len(runs))
	for i, r := range runs {
		out[i] = r.Keys
	}
	return out
}

// buggyJoin simulates broken joins: mode 1 = pair once per equal key
// and advance both; mode 2 = advance R on <= (never pairs).
func buggyJoin(r, s []int, mode int) int {
	n, i, j := 0, 0, 0
	for i < len(r) && j < len(s) {
		switch {
		case r[i] < s[j] || (mode == 2 && r[i] == s[j]):
			i++
		case r[i] > s[j]:
			j++
		default:
			n++
			i++
			j++
		}
	}
	return n
}

// sift mirrors mrg's heap sift-down and returns comparisons made.
func sift(h []int, i int) int {
	n := 0
	for {
		s := i
		if l := 2*i + 1; l < len(h) {
			n++
			if h[l] < h[s] {
				s = l
			}
		}
		if r := 2*i + 2; r < len(h) {
			n++
			if h[r] < h[s] {
				s = r
			}
		}
		if s == i {
			return n
		}
		h[i], h[s] = h[s], h[i]
		i = s
	}
}

// worstProbes pops an m-heap dry, returning the max compares per pop.
func worstProbes(m int) int {
	h := make([]int, m)
	for i := range h {
		h[i] = i
	}
	for i := m/2 - 1; i >= 0; i-- {
		sift(h, i)
	}
	worst := 0
	for len(h) > 0 {
		h[0] = h[len(h)-1]
		h = h[:len(h)-1]
		if n := sift(h, 0); n > worst {
			worst = n
		}
	}
	return worst
}

func ceilLog2(m int) int {
	n := 0
	for (1 << n) < m {
		n++
	}
	return n
}

func main() {
	R := []int{5, 1, 3, 7, 3, 2}
	S := []int{3, 6, 3, 2}

	runsR, errR := run.Build(3, R)
	runsS, errS := run.Build(3, S)
	report("run-structure R/S", errR == nil && errS == nil &&
		reflect.DeepEqual(runShape(runsR), [][]int{{1, 3, 5}, {2, 3, 7}}) &&
		reflect.DeepEqual(runShape(runsS), [][]int{{3, 3, 6}, {2}}))

	mergedR, _ := mrg.Sorted(runsR, 2)
	mergedS, _ := mrg.Sorted(runsS, 2)
	report("merged-seq R/S", reflect.DeepEqual(mergedR, []int{1, 2, 3, 3, 5, 7}) &&
		reflect.DeepEqual(mergedS, []int{2, 3, 3, 6}))

	j, _ := mrg.New(runsR, runsS, 2)
	pairs := j.Join()
	step2, step3 := 0, 0
	for _, p := range pairs {
		if p.R == 2 {
			step2++
		}
		if p.R == 3 {
			step3++
		}
	}
	report("join steps: step2=1 step3=4 total=5", step2 == 1 && step3 == 4 && len(pairs) == 5)

	// 三问错值: 甲=3 乙=0 丙=2
	runsR0, _ := run.Build(3, []int{5, 1, 3})
	jc, _ := mrg.New(runsR0, runsS, 2)
	report("wrong-values a=3 b=0 c=2", buggyJoin(mergedR, mergedS, 1) == 3 &&
		buggyJoin(mergedR, mergedS, 2) == 0 && len(jc.Join()) == 2)

	eng, err := api.New(3, 2)
	ok := err == nil && eng.BuildR(R) == nil && eng.BuildS(S) == nil
	got, errJ := eng.Join()
	report("join==naive & SelfCheck", ok && errJ == nil && len(got) == 5 && eng.SelfCheck() == nil)

	_, e1 := api.New(0, 2)
	_, e2 := api.New(3, 1)
	e3 := eng.BuildR([]int{1, -2})
	report("3 distinct sentinel errors", errors.Is(e1, api.ErrBadThreshold) &&
		errors.Is(e2, api.ErrBadFanIn) && errors.Is(e3, api.ErrNegativeKey) &&
		e1 != e2 && e2 != e3 && e1 != e3)

	got2, _ := eng.Join() // 被拒的 BuildR 不得留痕: 仍是 5 条
	report("rejection leaves no trace", len(got2) == 5)

	bound := true
	for _, m := range []int{100, 1000, 10000} {
		if worstProbes(m) > 2*ceilLog2(m)+2 {
			bound = false
		}
	}
	report("heap probes <= 2*ceil(log2 m)+2", bound)

	conc := true
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := eng.Join()
			if err != nil || len(p) != 5 {
				conc = false
			}
		}()
	}
	wg.Wait()
	report("concurrent read-only Join identical", conc)

	if failures > 0 {
		fmt.Println("FAILURES:", failures)
		os.Exit(1)
	}
}
