// Command demo 演示可撤销批量重命名与冲突解析器的全部判定。
package main

import (
	"errors"
	"fmt"
	"math/rand"

	"ontology/name"
	"ontology/plan"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

// simulate 逐步执行并报告是否发生覆盖或源缺失。
func simulate(initial []string, steps []plan.Step) bool {
	at := map[string]bool{}
	for _, n := range initial {
		at[n] = true
	}
	for _, s := range steps {
		if at[s.New] || !at[s.Old] {
			return false
		}
		delete(at, s.Old)
		at[s.New] = true
	}
	return true
}

func rq(old, new string) plan.Request { return plan.Request{Old: old, New: new} }

func main() {
	total := 0
	run := func(name string, ok bool) { total++; check(name, ok) }

	// name 包：空串与分隔符名字合法，集合操作正确。
	ns := name.New("a", "a/b", "")
	ns.Add(`a\b`)
	run("namespace basics", name.Valid("") && ns.Has("") && ns.Has("a/b") &&
		name.Equal(ns.Snapshot(), []string{"", "a", "a/b", `a\b`}))

	// plan：链 {a→b,b→c} 的执行顺序与无覆盖。
	p, err := plan.Build(name.New("a", "b"), []plan.Request{rq("a", "b"), rq("b", "c")})
	run("chain order b->c then a->b, no overwrite", err == nil && len(p.Steps) == 2 &&
		p.Steps[0] == plan.Step{Old: "b", New: "c"} && p.Steps[1] == plan.Step{Old: "a", New: "b"} &&
		simulate([]string{"a", "b"}, p.Steps))

	// plan：三元环借一个临时名，每步目标名都不存在。
	p, err = plan.Build(name.New("a", "b", "c"), []plan.Request{rq("a", "b"), rq("b", "c"), rq("c", "a")})
	run("3-cycle one temp, targets absent", err == nil && len(p.Temps) == 1 &&
		len(p.Steps) == 4 && simulate([]string{"a", "b", "c"}, p.Steps))

	// plan：临时名被预先占用仍能成功。
	pre := []string{"a", "b"}
	for i := range 10 {
		pre = append(pre, fmt.Sprintf("\x00renametmp%d", i))
	}
	p, err = plan.Build(name.New(pre...), []plan.Request{rq("a", "b"), rq("b", "a")})
	run("temp names preoccupied still succeeds", err == nil && len(p.Temps) == 1 &&
		p.Temps[0] == "\x00renametmp10" && simulate(pre, p.Steps))

	// plan：四类冲突各一例，检测前命名空间逐元素未变。
	conflicts := []struct {
		names []string
		reqs  []plan.Request
		kind  error
	}{
		{[]string{"a", "b"}, []plan.Request{rq("a", "b")}, plan.ErrTargetExists},
		{[]string{"a", "b"}, []plan.Request{rq("a", "x"), rq("b", "x")}, plan.ErrDuplicateNew},
		{[]string{"a"}, []plan.Request{rq("a", "x"), rq("a", "y")}, plan.ErrDuplicateOld},
		{[]string{"a"}, []plan.Request{rq("z", "x")}, plan.ErrOldMissing},
	}
	ok := true
	for _, c := range conflicts {
		cns := name.New(c.names...)
		before := cns.Snapshot()
		_, err := plan.Build(cns, c.reqs)
		ok = ok && errors.Is(err, c.kind) && name.Equal(cns.Snapshot(), before)
	}
	run("four conflict kinds, namespace untouched", ok)

	// plan：打乱构造顺序 20 次，步骤序列一致。
	base := []plan.Request{rq("a", "b"), rq("b", "c"), rq("x", "y"), rq("y", "x"), rq("p", "q")}
	want := ""
	ok = true
	for seed := range 20 {
		reqs := append([]plan.Request{}, base...)
		rand.New(rand.NewSource(int64(seed))).Shuffle(len(reqs), func(i, j int) { reqs[i], reqs[j] = reqs[j], reqs[i] })
		dp, err := plan.Build(name.New("a", "b", "x", "y", "p"), reqs)
		got := fmt.Sprint(dp.Steps, dp.Temps)
		if seed == 0 {
			want = got
		}
		ok = ok && err == nil && got == want
	}
	run("deterministic over 20 shuffles", ok)

	// plan：临时名数等于环数（10 个互不相交的环）。
	var cycNames []string
	var cycReqs []plan.Request
	for i := range 10 {
		a, b, c := fmt.Sprintf("c%da", i), fmt.Sprintf("c%db", i), fmt.Sprintf("c%dc", i)
		cycNames = append(cycNames, a, b, c)
		cycReqs = append(cycReqs, rq(a, b), rq(b, c), rq(c, a))
	}
	p, err = plan.Build(name.New(cycNames...), cycReqs)
	run("temp count equals cycle count", err == nil && len(p.Temps) == 10 && simulate(cycNames, p.Steps))

	fmt.Printf("TOTAL %d checks, %d failures\n", total, failures)
}
