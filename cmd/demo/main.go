package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
	"ontology/table"
)

var failed bool

func check(ok bool, label string) {
	if !ok {
		failed = true
	}
	status := "OK  "
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, label)
}

func main() {
	t1 := table.New(8, 8)
	_ = t1.Declare("a", name.KindStrict, 1)
	r1, _ := t1.Ref("a", 2)
	check(r1.Depth == 0 && r1.Decl.Name == "a", fmt.Sprintf("01 basic resolve, hit depth=%d", r1.Depth))
	_ = t1.Enter()
	_ = t1.Declare("a", name.KindStrict, 3)
	r2, _ := t1.Ref("a", 4)
	check(r2.Depth == 1 && r2.Decl.Pos == 3, "02 inner shadows outer")
	_ = t1.Leave()
	r3, _ := t1.Ref("a", 2)
	check(r3 == r1, "03 leave restores outer field-by-field")

	t2 := table.New(8, 8)
	_ = t2.Enter()
	_ = t2.Declare("f", name.KindForward, 5)
	rf, ef := t2.Ref("f", 2)
	check(ef == nil && rf.Depth == 1 && rf.Decl.Pos == 5, "04 forward ref hits later decl")
	_ = t2.Declare("g", name.KindStrict, 5)
	_, eg := t2.Ref("g", 2)
	check(eg == resolve.ErrUseBeforeDeclare, "05 strict: use-before-declare")
	_, en := t2.Ref("nope", 2)
	check(en == resolve.ErrUndefined && en != eg, "06 undefined: third distinct error")

	t3 := table.New(8, 8)
	_ = t3.Declare("x", name.KindStrict, 1)
	_ = t3.Enter()
	_ = t3.Declare("x", name.KindForward, 5)
	rx, _ := t3.Ref("x", 2)
	check(rx.Depth == 1, "07 shadow+forward: inner decl wins (DESIGN)")
	check(t3.Declare("x", name.KindStrict, 6) == scope.ErrDuplicateDeclare, "08 dup declare rejected")

	t4 := table.New(8, 8)
	_ = t4.Declare("a", name.KindStrict, 1)
	_ = t4.Declare("b", name.KindStrict, 2)
	_ = t4.Enter()
	_ = t4.Declare("c", name.KindStrict, 3)
	_, _ = t4.Ref("a", 4)
	_, _ = t4.Ref("c", 5)
	_, _ = t4.Ref("a", 6)
	c4, _ := t4.Captures()
	check(len(c4) == 1 && c4[0].Decl.Name == "a" && c4[0].Depth == 0, "09 captures exact: {a@0}")

	t5 := table.New(1, 1)
	_ = t5.Declare("d", name.KindStrict, 1)
	p1, _ := t5.Ref("d", 2)
	errs := []error{t5.Declare("e", name.KindStrict, 3), t5.Declare("", name.KindStrict, 3)}
	_ = t5.Enter()
	errs = append(errs, t5.Enter())
	_ = t5.Leave()
	errs = append(errs, t5.Leave())
	p2, _ := t5.Ref("d", 2)
	seen, distinct := map[error]bool{}, true
	for _, e := range errs {
		if e == nil || seen[e] {
			distinct = false
		}
		seen[e] = true
	}
	check(distinct && p1 == p2 && t5.Leave() == table.ErrLeaveRoot, "10 rejected ops: distinct, no trace")

	ta, tb := build(1000, false), build(1000, true)
	_, _ = ta.Ref("target", 1)
	_, _ = tb.Ref("target", 1)
	check(ta.Looked() <= 1001 && tb.Looked() <= 4,
		fmt.Sprintf("11 looked: outer=%d(<=1001) inner=%d(<=4)", ta.Looked(), tb.Looked()))
	check(concurrent(), "12 concurrent refs identical + selfcheck")
	if failed {
		os.Exit(1)
	}
}

func build(depth int, inner bool) *table.Table {
	tb := table.New(depth, 64)
	if !inner {
		_ = tb.Declare("target", name.KindStrict, 0)
	}
	for d := 1; d <= depth; d++ {
		_ = tb.Enter()
		for i := 0; i < 50; i++ {
			_ = tb.Declare(name.Name(fmt.Sprintf("j%d_%d", d, i)), name.KindStrict, 0)
		}
	}
	if inner {
		_ = tb.Declare("target", name.KindStrict, 0)
	}
	return tb
}

func concurrent() bool {
	tc := table.New(4, 8)
	_ = tc.Declare("a", name.KindStrict, 1)
	_ = tc.Enter()
	_ = tc.Declare("c", name.KindForward, 2)
	wantA, _ := tc.Ref("a", 5)
	wantC, _ := tc.Ref("c", 5)
	const g = 16
	got := make([][2]resolve.Result, g)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < g; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				ra, _ := tc.Ref("a", 5)
				rc, _ := tc.Ref("c", 5)
				got[i] = [2]resolve.Result{ra, rc}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for _, r := range got {
		if r[0] != wantA || r[1] != wantC {
			return false
		}
	}
	return tc.SelfCheck() == nil
}
