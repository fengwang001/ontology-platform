// Command demo 演示可撤销批量重命名器的各项判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"ontology/apply"
	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
)

var checks int
var failures int

func report(ok bool, format string, args ...any) {
	checks++
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, fmt.Sprintf(format, args...))
}

func main() {
	checkName()
	checkCycle()
	checkPlan()
	checkApply()
	fmt.Printf("TOTAL %d/%d\n", checks-failures, checks)
	if failures > 0 {
		os.Exit(1)
	}
}

func checkName() {
	ns := name.New("a", "b")
	ok := ns.Has("a") && !ns.Has("c") &&
		name.Valid("") && name.Valid("x/y") && name.Valid(`x\y`)
	ns.Lock()
	ok = ok && ns.Rename("a", "c") && !ns.Rename("b", "c")
	ns.Unlock()
	report(ok, "name: 集合操作与边界名合法性")
}

func checkCycle() {
	edges := map[string]string{"a": "b", "b": "c", "c": "a", "x": "y"}
	rings := cycle.Find(edges)
	ok := len(rings) == 1 && len(rings[0]) == 3 && rings[0][0] == "a"
	namer := cycle.NewTempNamer(func(s string) bool { return s == "~tmp-0" })
	tmp, err := namer.Next()
	ok = ok && err == nil && tmp == "~tmp-1"
	report(ok, "cycle: 环检测与临时名冲突重试")
}

// simulate 逐步执行计划并断言每一步的目标名都不存在（无覆盖）。
func simulate(ns map[string]bool, steps []plan.Step) bool {
	for _, s := range steps {
		if !ns[s.Old] || ns[s.New] {
			return false
		}
		delete(ns, s.Old)
		ns[s.New] = true
	}
	return true
}

func checkPlan() {
	p, err := plan.Build([]plan.Request{{Old: "a", New: "b"}, {Old: "b", New: "c"}},
		func(s string) bool { return s == "a" || s == "b" })
	ok := err == nil && len(p.Steps) == 2 &&
		p.Steps[0].Old == "b" && p.Steps[1].Old == "a" &&
		simulate(map[string]bool{"a": true, "b": true}, p.Steps)
	report(ok, "plan: {a→b,b→c} 先 b→c 且无覆盖")

	p, err = plan.Build([]plan.Request{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "c", New: "a"}},
		func(s string) bool { return s == "a" || s == "b" || s == "c" })
	ok = err == nil && p.Temps == 1 && len(p.Steps) == 4 &&
		simulate(map[string]bool{"a": true, "b": true, "c": true}, p.Steps)
	report(ok, "plan: 三元环借 1 个临时名且每步目标名不存在")
}

func checkApply() {
	reqs := []plan.Request{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "x", New: "y"}}
	for _, k := range []int{1, 2, 3} {
		ns := name.New("a", "b", "x")
		before := ns.Snapshot()
		ex := &apply.Executor{NS: ns, FailBefore: k}
		_, err := ex.Run(reqs)
		ok := errors.Is(err, apply.ErrInjected) && name.Equal(before, ns.Snapshot())
		report(ok, "apply: 第 %d 步注入失败后完整回滚", k)
	}

	ns := name.New("a", "b", "x")
	started := make(chan struct{})
	ex := &apply.Executor{NS: ns, OnStep: func(i int) {
		if i == 0 {
			close(started)
			time.Sleep(50 * time.Millisecond)
		}
	}}
	done := make(chan struct{})
	go func() { ex.Run(reqs); close(done) }()
	<-started
	acquired := make(chan struct{})
	go func() { ns.Lock(); ns.Unlock(); close(acquired) }()
	blocked := false
	select {
	case <-acquired:
	case <-time.After(10 * time.Millisecond):
		blocked = true
	}
	<-done
	<-acquired
	report(blocked, "apply: 批量执行期间并发修改被阻塞")
}
