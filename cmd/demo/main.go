package main

import (
	"fmt"
	"time"

	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
)

func main() {
	total, failed := run()
	fmt.Printf("TOTAL %d/%d OK\n", total-failed, total)
	if failed != 0 {
		panic("demo checks failed")
	}
}

func run() (total, failed int) {
	check("demo skeleton boots", true, &total, &failed)
	sp := name.New("a")
	legalOK := name.Valid("") && name.Valid("x/y") && name.Valid("a\\b")
	sp.Lock()
	done := make(chan struct{})
	go func() { sp.Add("z"); close(done) }()
	select {
	case <-done:
		legalOK = false
	case <-time.After(10 * time.Millisecond):
	}
	sp.Unlock()
	<-done
	check("name: empty/slash valid; batch lock blocks concurrent edit",
		legalOK && sp.Contains("z"), &total, &failed)
	ok3 := false
	{
		exist := map[string]struct{}{"a": {}, "b": {}, "c": {}}
		reqs := []plan.Req{{"a", "b"}, {"b", "c"}, {"c", "a"}}
		p, err := plan.Build(exist, reqs)
		ok3 = err == nil && len(p.Cycles) == 1
		ex, err := cycle.Expand(p, exist, reqs)
		noOverwrite := err == nil && ex.TempCount() == 1
		sim := map[string]struct{}{"a": {}, "b": {}, "c": {}}
		for _, st := range cycle.LinearSteps(p, ex) {
			if _, taken := sim[st.New]; taken {
				noOverwrite = false
			}
			delete(sim, st.Old)
			sim[st.New] = struct{}{}
		}
		ok3 = ok3 && noOverwrite
	}
	check("cycle: 3-cycle one temp, target absent every step", ok3, &total, &failed)
	ok4 := false
	{
		exist := map[string]struct{}{"a": {}, "b": {}}
		for i := 0; i < 500; i++ {
			exist["tpm-"+itoaDemo(i)] = struct{}{}
		}
		reqs := []plan.Req{{"a", "b"}, {"b", "a"}}
		p, _ := plan.Build(exist, reqs)
		ex, err := cycle.Expand(p, exist, reqs)
		ok4 = err == nil && ex.Temps[0] == "tpm-500"
	}
	check("cycle: temp names pre-taken, retry still succeeds", ok4, &total, &failed)
	ok5 := false
	{
		exist := map[string]struct{}{}
		var reqs []plan.Req
		for i := 0; i < 10; i++ {
			x, y := fmt.Sprintf("x%02d", i), fmt.Sprintf("y%02d", i)
			exist[x], exist[y] = struct{}{}, struct{}{}
			reqs = append(reqs, plan.Req{x, y}, plan.Req{y, x})
		}
		p, _ := plan.Build(exist, reqs)
		ex, err := cycle.Expand(p, exist, reqs)
		ok5 = err == nil && ex.TempCount() == 10
	}
	check("cycle: 10 disjoint cycles use exactly 10 temps", ok5, &total, &failed)
	return total, failed
}

func itoaDemo(k int) string {
	if k == 0 {
		return "0"
	}
	buf := make([]byte, 0, 8)
	for k > 0 {
		buf = append([]byte{byte('0' + k%10)}, buf...)
		k /= 10
	}
	return string(buf)
}

func check(label string, ok bool, total, failed *int) {
	*total++
	if ok {
		fmt.Printf("OK   %s\n", label)
		return
	}
	*failed++
	fmt.Printf("FAIL %s\n", label)
}
