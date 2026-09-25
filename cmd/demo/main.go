package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/elect"
	"ontology/ring"
)

var failed bool

func report(ok bool, what string) {
	if ok {
		fmt.Println("OK " + what)
	} else {
		fmt.Println("FAIL " + what)
		failed = true
	}
}

// checkRing 核验环闭合、三类可判定错误互不相同、被拒后状态不变。
func checkRing() {
	r := ring.New()
	for _, id := range []string{"3", "7", "2", "5"} {
		if r.Add(id) != nil {
			report(false, "ring build")
			return
		}
	}
	// 环闭合：从任意节点沿后继走，恰好访问每个节点一次后回到自身。
	closed := r.Size() == 4
	for _, start := range r.Order() {
		seen, cur := map[string]bool{}, start
		for i := 0; i < r.Size(); i++ {
			seen[cur] = true
			cur, _ = r.Successor(cur)
		}
		closed = closed && cur == start && len(seen) == 4
	}
	report(closed, "ring closed from every node")

	e1, e2, e3 := r.Add(""), r.Add("7"), r.Remove("9")
	distinct := errors.Is(e1, ring.ErrEmptyID) && errors.Is(e2, ring.ErrDupID) &&
		errors.Is(e3, ring.ErrNotFound) && e1 != e2 && e2 != e3 && e1 != e3
	report(distinct, "empty/dup/missing errors distinct")

	max, _ := r.Max()
	untouched := r.Size() == 4 && max == "7" && r.Add("9") == nil && r.Remove("9") == nil
	report(untouched, "rejected ops leave state unchanged")
}

// checkElect 核验第三节八行分步表、胜者 N2(ID 7)、总消息数 8、与朴素参照一致。
func checkElect() {
	r := ring.New()
	for _, id := range []string{"3", "7", "2", "5"} {
		_ = r.Add(id)
	}
	res, err := elect.Elect(r)
	if err != nil {
		report(false, "elect run")
		return
	}
	want := []elect.Event{
		{At: "7", Msg: "3", From: "3", Action: "discard"},
		{At: "2", Msg: "7", From: "7", Action: "forward", To: "5"},
		{At: "5", Msg: "2", From: "2", Action: "discard"},
		{At: "3", Msg: "5", From: "5", Action: "forward", To: "7"},
		{At: "5", Msg: "7", From: "2", Action: "forward", To: "3"},
		{At: "7", Msg: "5", From: "3", Action: "discard"},
		{At: "3", Msg: "7", From: "5", Action: "forward", To: "7"},
		{At: "7", Msg: "7", From: "3", Action: "declare"},
	}
	traceOK := len(res.Trace) == len(want)
	for i := range want {
		if traceOK && res.Trace[i] != want[i] {
			traceOK = false
		}
	}
	report(traceOK, "8-step trace recv/compare/dest")
	report(res.Winner == "7", "winner is N2 (id 7)")
	report(res.Messages == 8, "total messages == 8")
	max, _ := r.Max()
	report(res.Winner == max, "winner matches naive max")
}

// checkAPI 核验 SelfCheck、大 m 后继查询、并发选举一致。
func checkAPI() {
	report(api.SelfCheck() == nil, "api.SelfCheck four invariants")

	// 大 m 下后继查询：靠后继指针直接定位（计数器非导出，断言见 ring 白盒测试）。
	scalable := true
	for _, m := range []int{100, 1000, 10000} {
		r := ring.New()
		for i := 0; i < m; i++ {
			_ = r.Add(fmt.Sprintf("n%05d", i))
		}
		for i := 0; i < 100; i++ {
			if _, err := r.Successor("n00042"); err != nil {
				scalable = false
			}
		}
	}
	report(scalable, "successor query independent of m")

	// 并发：N 个 goroutine 对同一环 Elect，胜者必须相同；并发 AddNode 后胜者仍为最大。
	s := api.New()
	for _, id := range []string{"3", "7", "2", "5"} {
		_ = s.AddNode(id)
	}
	var wg sync.WaitGroup
	winners := make([]string, 32)
	for i := range winners {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			w, _, err := s.Elect()
			if err == nil {
				winners[k] = w
			}
		}(i)
	}
	wg.Wait()
	consistent := true
	for _, w := range winners {
		consistent = consistent && w == "7"
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			_ = s.AddNode(fmt.Sprintf("a%d", k))
		}(i)
	}
	wg.Wait()
	w, _, err := s.Elect()
	report(consistent && err == nil && w == "a7", "concurrent elect consistent")
}

func main() {
	checkRing()
	checkElect()
	checkAPI()
	if failed {
		os.Exit(1)
	}
}
