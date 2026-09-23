package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/capture"
	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
	"ontology/table"
)

var fails int

func check(label string, ok bool) {
	if ok {
		fmt.Println("OK   " + label)
		return
	}
	fmt.Println("FAIL " + label)
	fails++
}

func main() {
	check("name: equality and kinds", name.Equal("x", "x") && !name.Equal("x", "y") &&
		name.Plain != name.Forward && name.Decl{Name: "a", Kind: name.Forward, Pos: 3}.Pos == 3)
	sc := scope.New(nil, 0)
	declOK := sc.Declare(name.Decl{Name: "x", Kind: name.Plain, Pos: 1}) == nil
	_, found := sc.Lookup("x")
	check("scope: declare/lookup/dup/parent", declOK && found && sc.Len() == 1 &&
		sc.Declare(name.Decl{Name: "x"}) == scope.ErrDuplicate && sc.Parent() == nil && sc.Depth() == 0)
	res := &resolve.Resolver{}
	root := scope.New(nil, 0)
	_ = root.Declare(name.Decl{Name: "x", Kind: name.Plain, Pos: 0})
	inner := scope.New(root, 1)
	_ = inner.Declare(name.Decl{Name: "x", Kind: name.Forward, Pos: 10})
	_ = inner.Declare(name.Decl{Name: "y", Kind: name.Plain, Pos: 2})
	hit, err := res.Resolve(inner, "y", 5)
	check(fmt.Sprintf("resolve: basic hit depth=%d", hit.Depth), err == nil && hit.Depth == 1 && hit.Decl.Name == "y")
	hit, err = res.Resolve(inner, "x", 5)
	check("resolve: inner fwd decl shadows outer (DESIGN sec.2)", err == nil && hit.Depth == 1 && hit.Decl.Pos == 10)
	_, errFwd := res.Resolve(inner, "x", 5)
	_, errUse := res.Resolve(inner, "y", 1)
	_, errUnd := res.Resolve(inner, "nope", 0)
	check("resolve: fwd ok / use-before-decl / undefined are distinct",
		errFwd == nil && errors.Is(errUse, resolve.ErrUseBeforeDecl) &&
			errors.Is(errUnd, resolve.ErrUndefined) && !errors.Is(errUnd, resolve.ErrUseBeforeDecl) &&
			!errors.Is(errUse, resolve.ErrUndefined))
	hOuter, _ := res.Resolve(inner, "x", 5)
	hOuter2, _ := res.Resolve(root, "x", 0)
	caps := capture.Outer([]resolve.Hit{hOuter2, hOuter2, hOuter}, 1)
	check("capture: exact set, deduped", len(caps) == 1 && caps[0] == hOuter2)

	tb := table.New(1000, 60)
	_ = tb.Declare("g", name.Plain, 0)
	dg, depg, _ := tb.Ref("g", 1)
	check(fmt.Sprintf("table: declare+ref depth=%d", depg), depg == 0 && dg.Name == "g")
	before, bDep, bErr := tb.Ref("g", 1)
	_ = tb.Enter()
	_ = tb.Declare("g", name.Forward, 9)
	_ = tb.Leave()
	after, aDep, aErr := tb.Ref("g", 1)
	check("table: leave restores outer decl field-by-field", before == after && bDep == aDep && bErr == aErr)
	errDup := tb.Declare("g", name.Plain, 5)
	dg2, _, _ := tb.Ref("g", 1)
	check("table: dup rejected, first decl intact", errors.Is(errDup, scope.ErrDuplicate) && dg2 == dg)
	lim := table.New(1, 1)
	_ = lim.Enter()
	errDepth := lim.Enter()
	_ = lim.Declare("a", name.Plain, 0)
	errDecls := lim.Declare("b", name.Plain, 1)
	errEmpty := lim.Declare("", name.Plain, 0)
	errRoot := table.New(1, 1).Leave()
	check("table: limits/empty/root-leave are distinct errors",
		errors.Is(errDepth, table.ErrMaxDepth) && errors.Is(errDecls, table.ErrMaxDecls) &&
			errors.Is(errEmpty, table.ErrEmptyName) && errors.Is(errRoot, table.ErrLeaveRoot) &&
			!errors.Is(errRoot, table.ErrEmptyName))
	s1, sd1, _ := lim.Ref("a", 0)
	errDepth2 := lim.Enter()
	s2, sd2, _ := lim.Ref("a", 0)
	check("table: rejected op leaves state identical, still usable",
		errors.Is(errDepth2, table.ErrMaxDepth) && s1 == s2 && sd1 == sd2 && lim.SelfCheck() == nil)
	ct := table.New(10, 10)
	_ = ct.Declare("a", name.Plain, 0)
	_ = ct.Declare("b", name.Plain, 0)
	_ = ct.Enter()
	_, _, _ = ct.Ref("a", 1)
	_, _, _ = ct.Ref("a", 2)
	_ = ct.Declare("c", name.Forward, 9)
	_, _, _ = ct.Ref("c", 1)
	capsT := ct.Captures()
	check("table: captures exact (a only, once)",
		len(capsT) == 1 && capsT[0].Decl.Name == "a" && capsT[0].Depth == 0)
	check("table: selfcheck passes", tb.SelfCheck() == nil && ct.SelfCheck() == nil)
	deep := table.New(1000, 60)
	_ = deep.Declare("target", name.Plain, 0)
	for i := 0; i < 999; i++ {
		_ = deep.Enter()
		for j := 0; j < 50; j++ {
			_ = deep.Declare(fmt.Sprintf("f%d-%d", i, j), name.Plain, 0)
		}
	}
	_, depOut, errOut := deep.Ref("target", 0)
	deep2 := table.New(1000, 60)
	for i := 0; i < 999; i++ {
		_ = deep2.Enter()
	}
	_ = deep2.Declare("target", name.Plain, 0)
	_, depIn, errIn := deep2.Ref("target", 0)
	entriesOut, entriesIn := 999-depOut+1, 999-depIn+1
	check(fmt.Sprintf("resolve: deep chain entries outer=%d inner=%d", entriesOut, entriesIn),
		errOut == nil && errIn == nil && depOut == 0 && depIn == 999 && entriesOut <= 1000 && entriesIn <= 1)
	conc := table.New(10, 10)
	_ = conc.Declare("x", name.Plain, 0)
	_ = conc.Enter()
	_ = conc.Declare("y", name.Forward, 5)
	wantD, wantDep, _ := conc.Ref("x", 3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	same := true
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				d, dep, err := conc.Ref("x", 3)
				_, _, err2 := conc.Ref("y", 1)
				if err != nil || err2 != nil || d != wantD || dep != wantDep ||
					conc.SelfCheck() != nil || len(conc.Captures()) != 1 {
					mu.Lock()
					same = false
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	check("table: concurrent refs bit-identical", same)
	if fails > 0 {
		os.Exit(1)
	}
}
