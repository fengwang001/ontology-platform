package main

import (
	"errors"
	"fmt"

	"ontology/plan"
	"ontology/name"
)

type check struct {
	label string
	ok    bool
}

func report(cs []check) {
	pass := 0
	for _, c := range cs {
		if c.ok {
			pass++
			fmt.Println("OK  " + c.label)
		} else {
			fmt.Println("FAIL " + c.label)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(cs))
	if pass != len(cs) {
		panic("demo checks failed")
	}
}

func main() {
	var cs []check

	// name 包：空串/分隔符合法，自环无操作，目标已存在被拒。
	ns := name.New("a", "a/b", "")
	ok := name.Valid("") && name.Valid("x/y")
	ok = ok && ns.RenameLocked("a", "a") == nil && name.EqualSet(ns.Snapshot(), []string{"", "a", "a/b"})
	ok = ok && ns.RenameLocked("a", "a/b") != nil
	cs = append(cs, check{"name: 空串与分隔符合法/自环无操作/覆盖被拒", ok})

	// plan 包：链的拓扑序、四类冲突且检测前集合不变。
	ns2 := name.New("a", "b")
	before := ns2.Snapshot()
	p, err := plan.Build(ns2, []plan.Req{{Old: "a", New: "b"}, {Old: "b", New: "c"}})
	chainOK := err == nil && len(p.Order) == 2 &&
		p.Order[0].Old == "b" && p.Order[1].Old == "a"
	confCases := []struct {
		init []string
		reqs []plan.Req
		kind error
	}{
		{[]string{"a", "x"}, []plan.Req{{Old: "a", New: "x"}}, plan.ErrTargetExists},
		{[]string{"a", "b"}, []plan.Req{{Old: "a", New: "x"}, {Old: "b", New: "x"}}, plan.ErrDuplicateTarget},
		{[]string{"a"}, []plan.Req{{Old: "a", New: "x"}, {Old: "a", New: "y"}}, plan.ErrDuplicateSource},
		{[]string{"a"}, []plan.Req{{Old: "z", New: "x"}}, plan.ErrMissingSource},
	}
	for _, cc := range confCases {
		n := name.New(cc.init...)
		b := n.Snapshot()
		_, e := plan.Build(n, cc.reqs)
		chainOK = chainOK && errorsIs(e, cc.kind) && name.EqualSet(b, n.Snapshot())
	}
	chainOK = chainOK && name.EqualSet(before, ns2.Snapshot())
	cs = append(cs, check{"plan: {a->b,b->c} 先 b->c 且四类冲突检测前未改集合", chainOK})

	report(cs)
}

func errorsIs(err, target error) bool {
	if target == nil {
		return err == nil
	}
	return err != nil && errors.Is(err, target)
}
