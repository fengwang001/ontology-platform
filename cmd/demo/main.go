package main

import "errors"
import "fmt"
import "math/rand/v2"
import "slices"
import "sync"
import "ontology/api"
import "ontology/ev"
import "ontology/fold"

func ok(cond bool, label string) {
	if cond {
		fmt.Println("OK " + label)
	} else {
		fmt.Println("FAIL " + label)
	}
}
func evi(k string, v int64) ev.Event { return ev.Event{Key: k, Val: v, Op: ev.Insert} }
func evr(k string, v int64) ev.Event { return ev.Event{Key: k, Val: v, Op: ev.Retract} }

func mstr(a *api.API, p []ev.Event) string {
	st, err := a.Replay(api.State{}, p)
	if err != nil {
		return "ERR"
	}
	var o []int64
	for v, n := range st["g"] {
		o = append(o, slices.Repeat([]int64{v}, n)...)
	}
	slices.Sort(o)
	return fmt.Sprint(o)
}

func main() {
	a := api.New(0)
	six := []ev.Event{evi("g", 7), evi("g", 7), evr("g", 7), evr("g", 7), evi("g", 7), evi("g", 3)}
	steps := ""
	for i := range six {
		steps += mstr(a, six[:i+1]) + " "
	}
	ok(steps == "[7] [7 7] [7] [] [7] [3 7] ", "six-step multisets: "+steps)
	c1, err := a.Compact(six)
	end1, _ := a.Replay(api.State{}, six)
	end2, _ := a.Replay(api.State{}, c1)
	c2, _ := a.Compact(c1)
	ok(err == nil && fmt.Sprint(end1) == fmt.Sprint(end2) && len(c1) <= len(six) &&
		slices.Equal(c1, c2), "terminal equal, no growth, idempotent")
	ri := []ev.Event{evr("g", 7), evi("g", 7)}
	cri, _ := a.Compact(ri)
	_, eRI := a.Replay(api.State{}, ri)
	ok(slices.Equal(cri, ri) && errors.Is(eRI, api.ErrIllegalRetract),
		"R->I kept: [R,I] illegal on I={}, [] would be legal")
	rng := rand.New(rand.NewPCG(3, 4))
	match := true
	for range 50 {
		s := make([]ev.Event, 60)
		for i := range s {
			s[i] = ev.Event{Key: fmt.Sprintf("g%d", rng.IntN(4)),
				Val: int64(rng.IntN(3)), Op: ev.Op(1 + rng.IntN(2))}
		}
		init := api.State{"g0": {0: 100, 1: 100, 2: 100}, "g1": {0: 100, 1: 100, 2: 100}, "g2": {0: 100, 1: 100, 2: 100}, "g3": {0: 100, 1: 100, 2: 100}}
		c, _ := a.Compact(s)
		t1, e1 := a.Replay(init, s)
		t2, e2 := a.Replay(init, c)
		match = match && (e1 == nil) == (e2 == nil) && (e1 != nil || fmt.Sprint(t1) == fmt.Sprint(t2))
	}
	ok(match, "random streams replay equal per group")
	small := api.New(1)
	_, eLen := small.Replay(api.State{}, []ev.Event{evi("g", 1), evi("g", 2)})
	_, eBad := a.Replay(api.State{}, []ev.Event{{Key: "g", Op: ev.Op(7)}})
	_, eRet := a.Replay(api.State{}, []ev.Event{evr("g", 1)})
	ok(errors.Is(eLen, api.ErrStreamTooLong) && errors.Is(eBad, ev.ErrInvalidEvent) &&
		errors.Is(eRet, api.ErrIllegalRetract) && !errors.Is(eLen, ev.ErrInvalidEvent),
		"three distinct sentinel errors")
	init := api.State{"g": {1: 1}}
	snap := fmt.Sprint(init)
	_, f1 := a.Replay(init, []ev.Event{evr("g", 9)})
	_, f2 := a.Compact([]ev.Event{{Key: "", Op: ev.Insert}})
	after, _ := a.Compact(six)
	ok(f1 != nil && f2 != nil && fmt.Sprint(init) == snap && slices.Equal(after, c1),
		"rejected calls leave no trace, compacter reusable")
	ok(fold.New().LinearBound(10000, 1), "comparisons <= 1*n at n=10000")
	const N = 16
	res := make([][]ev.Event, N)
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func() { defer wg.Done(); res[i], _ = a.Compact(six) }()
	}
	wg.Wait()
	same := true
	for i := 1; i < N; i++ {
		same = same && slices.Equal(res[0], res[i])
	}
	ok(same, "16 goroutines compact to identical streams")
	ok(a.SelfCheck() == nil, "SelfCheck passes")
}
