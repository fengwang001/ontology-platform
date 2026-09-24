package main

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
)

var pass, fail int

func check(label string, ok bool) {
	if ok {
		pass++
		fmt.Println("OK  " + label)
		return
	}
	fail++
	fmt.Println("FAIL " + label)
}

func main() {
	// name 包判定：合法性、自环无操作、移动语义。
	check("name: 空串与分隔符合法", name.Valid("") && name.Valid("a/b"))
	sp := name.New("a")
	check("name: 自环 a→a 为无操作", sp.Move("a", "a") == nil && sp.Has("a"))
	check("name: a→b 成功且目标存在不覆盖",
		sp.Move("a", "b") == nil && sp.Has("b") && sp.Move("b", "b") == nil)

	// plan 包判定：链顺序、四类冲突、5 万请求复杂度。
	pl, err := plan.Compile(
		[]plan.Req{{"a", "b"}, {"b", "c"}}, []string{"a", "b"})
	orderOK := err == nil && len(pl.Chains) == 2 &&
		pl.Chains[0] == (plan.Step{"b", "c"}) && pl.Chains[1] == (plan.Step{"a", "b"})
	check("plan: {a→b,b→c} 顺序为 b→c 再 a→b", orderOK)
	confCases := []struct {
		init []string
		reqs []plan.Req
		kind error
	}{
		{[]string{"a", "b"}, []plan.Req{{"a", "b"}}, plan.ErrDestExists},
		{[]string{"a", "b"}, []plan.Req{{"a", "x"}, {"b", "x"}}, plan.ErrDupDest},
		{[]string{"a"}, []plan.Req{{"a", "x"}, {"a", "y"}}, plan.ErrDupSrc},
		{[]string{"a"}, []plan.Req{{"z", "x"}}, plan.ErrSrcMissing},
	}
	confOK := true
	for _, c := range confCases {
		before := name.New(c.init...).Snapshot()
		ns := name.New(c.init...)
		_, e := plan.Compile(c.reqs, ns.Snapshot())
		if !errors.Is(e, c.kind) || !reflect.DeepEqual(ns.Snapshot(), before) {
			confOK = false
		}
	}
	check("plan: 四类冲突各一例且检测前命名空间未变", confOK)
	var big []plan.Req
	var init []string
	for i := 0; i < 50000; i++ {
		a, b := fmt.Sprintf("n%06d", i), fmt.Sprintf("m%06d", i)
		big, init = append(big, plan.Req{a, b}), append(init, a)
	}
	bp, _ := plan.Compile(big, init)
	check("plan: 5 万请求 lookups ≤ 4*(R+名字数)",
		bp.Lookups() <= 4*(len(big)+len(init)))

	// cycle 包判定：三元环借一个临时名且每步目标不存在；
	// 临时名被占用仍成功；10 环 => 10 个临时名。
	cyc3, _ := plan.Compile(
		[]plan.Req{{"a", "b"}, {"b", "c"}, {"c", "a"}},
		[]string{"a", "b", "c"})
	st3, tp3, e3 := cycle.Break(cyc3, []string{"a", "b", "c"})
	s3 := name.New("a", "b", "c")
	noClob := e3 == nil && len(tp3) == 1
	if noClob {
		for _, step := range st3 {
			if s3.Has(step.To) {
				noClob = false
			}
			_ = s3.Move(step.From, step.To)
		}
	}
	check("cycle: 三元环借一个临时名且每步目标名都不存在",
		noClob && reflect.DeepEqual(s3.Snapshot(), []string{"a", "b", "c"}))

	occ := []string{"a", "b"}
	for i := 0; i < 1000; i++ {
		occ = append(occ, fmt.Sprintf(".rename-tmp-%d", i))
	}
	cyc2, _ := plan.Compile(
		[]plan.Req{{"a", "b"}, {"b", "a"}}, []string{"a", "b"})
	_, tp2, e2 := cycle.Break(cyc2, occ)
	check("cycle: 临时名 tmp-0..999 被占用仍能成功",
		e2 == nil && len(tp2) == 1 && tp2[0] == ".rename-tmp-1000")

	var cInit []string
	var cReqs []plan.Req
	for i := 0; i < 10; i++ {
		a, b, c := fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i), fmt.Sprintf("c%d", i)
		cInit, cReqs = append(cInit, a, b, c),
			append(cReqs, plan.Req{a, b}, plan.Req{b, c}, plan.Req{c, a})
	}
	cp10, _ := plan.Compile(cReqs, cInit)
	_, tp10, e10 := cycle.Break(cp10, cInit)
	check("cycle: 10 个互不相交环 => 临时名恰好 10 个",
		e10 == nil && len(tp10) == 10)
	fmt.Printf("TOTAL pass=%d fail=%d\n", pass, fail)
}
