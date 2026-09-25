// Command demo prints OK/FAIL lines for each required check.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/merge"
	"ontology/msrc"
)

var failed bool

func check(ok bool, name string) {
	if ok {
		fmt.Println("OK " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func scenario() map[string][]api.Event {
	return map[string][]api.Event{
		"A": {{Seq: 0, TS: 5, Key: "k1", Val: "a1"}, {Seq: 1, TS: 7, Key: "k2", Val: "a2"}, {Seq: 2, TS: 9, Key: "k1", Val: "a3"}},
		"B": {{Seq: 0, TS: 5, Key: "k1", Val: "b1"}, {Seq: 1, TS: 8, Key: "k3", Val: "b2"}, {Seq: 2, TS: 9, Key: "k1", Val: "b3"}},
		"C": {{Seq: 0, TS: 6, Key: "k4", Val: "c1"}, {Seq: 1, TS: 7, Key: "k2", Val: "c2"}, {Seq: 2, TS: 10, Key: "k5", Val: "c3"}},
	}
}

func main() {
	srcs := scenario()
	sys := api.New()
	for _, n := range []string{"A", "B", "C"} {
		if err := sys.AddSource(n, srcs[n]); err != nil {
			check(false, "注册源")
		}
	}
	log := sys.Drain()
	wantView := map[string]string{"k1": "a3", "k2": "a2", "k3": "b2", "k4": "c1", "k5": "c3"}
	vals := make([]string, 0, len(log))
	for _, e := range log {
		vals = append(vals, e.Val)
	}
	check(fmt.Sprint(vals) == fmt.Sprint([]string{"a1", "c1", "a2", "b2", "a3", "c3"}) &&
		fmt.Sprint(sys.View()) == fmt.Sprint(wantView), "九事件归并顺序与最终 view 符合推导")
	check(sys.Dups() == 3, "去重数等于 3")
	re := map[string]string{}
	for _, e := range log { // 批量重算：只对被保留事件 LWW
		re[e.Key] = e.Val
	}
	check(fmt.Sprint(re) == fmt.Sprint(sys.View()), "View 与批量重算一致")
	seen := map[[2]interface{}]bool{}
	selfOK := len(log) == 6
	for _, e := range log {
		kt := [2]interface{}{e.Key, e.TS}
		selfOK = selfOK && !seen[kt]
		seen[kt] = true
	}
	check(selfOK, "变更日志 (Key,TS) 唯一、前缀自洽")
	errs := []error{
		sys.AddSource("A", srcs["A"]),
		sys.AddSource("D", []api.Event{{Seq: 1, TS: 1, Key: "x"}, {Seq: 1, TS: 2, Key: "y"}}),
		sys.AddSource("E", []api.Event{{Seq: 0, TS: 2, Key: "x"}, {Seq: 1, TS: 1, Key: "y"}}),
		sys.AddSource("F", []api.Event{{Seq: 0, TS: 1, Key: ""}}),
	}
	distinct := errors.Is(errs[0], merge.ErrDuplicateName) && errors.Is(errs[1], msrc.ErrSeqNotStrictlyIncreasing) &&
		errors.Is(errs[2], msrc.ErrTSDecreasing) && errors.Is(errs[3], msrc.ErrEmptyKey)
	check(distinct && errs[0] != errs[1] && errs[1] != errs[2] && errs[2] != errs[3], "四类错误可判定且互不相同")
	check(len(sys.Drain()) == 6 && sys.Dups() == 3 && fmt.Sprint(sys.View()) == fmt.Sprint(wantView), "被拒后状态不变")
	big := api.New()
	for i := 0; i < 10000; i++ {
		if big.AddSource(fmt.Sprint("s", i), []api.Event{{Seq: 0, TS: int64(i), Key: fmt.Sprint("k", i), Val: "v"}}) != nil {
			check(false, "大 m 注册")
		}
	}
	check(len(big.Drain()) == 10000, "大 m 归并正确（比较次数亚线性由 merge 内部测试钉住）")
	start := make(chan struct{})
	var wg sync.WaitGroup
	views := make([]map[string]string, 32)
	for i := range views {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; views[i] = sys.View() }(i)
	}
	close(start)
	wg.Wait()
	same := true
	for _, v := range views {
		same = same && fmt.Sprint(v) == fmt.Sprint(wantView)
	}
	check(same, "32 goroutine 并发只读视图一致")
	check(sys.SelfCheck() == nil, "SelfCheck 通过")
	if failed {
		os.Exit(1)
	}
}
