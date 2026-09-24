// Command demo exercises the ontology rename planner end to end.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"

	"ontology/apply"
	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
	"ontology/undo"
)

var passed, total int

func report(ok bool, msg string) {
	total++
	if ok {
		passed++
		fmt.Printf("OK   %s\n", msg)
	} else {
		fmt.Printf("FAIL %s\n", msg)
	}
}

func conflictKeepsNS(ns *name.Set, reqs []plan.Request, sentinel error) bool {
	before := ns.Snapshot()
	err := plan.Check(ns, reqs)
	return errors.Is(err, sentinel) && reflect.DeepEqual(ns.Snapshot(), before)
}

// runChecked executes steps one by one, asserting the target name is
// absent before every single step (no overwrite, ever).
func runChecked(ns *name.Set, steps []plan.Step) bool {
	for _, s := range steps {
		if ns.Contains(s.To) {
			return false
		}
		if err := ns.Rename(s.From, s.To); err != nil {
			return false
		}
	}
	return true
}

// pipeline runs Check + Order + Break and returns the full step list.
func pipeline(ns *name.Set, reqs []plan.Request) ([]plan.Step, error) {
	if err := plan.Check(ns, reqs); err != nil {
		return nil, err
	}
	ord, cyc := plan.Order(reqs)
	cs, _, err := cycle.Break(ns, reqs, cyc)
	return append(ord, cs...), err
}

func main() {
	report(name.Valid("") && name.Valid("a/b"), "name: empty and separator names legal")

	nsC := name.New("a", "b")
	steps, _ := plan.Order([]plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}})
	want := []plan.Step{{From: "b", To: "c"}, {From: "a", To: "b"}}
	report(reflect.DeepEqual(steps, want) && runChecked(nsC, steps) && nsC.Equal(name.New("b", "c")),
		"chain {a->b,b->c} order is b->c then a->b, no overwrite")

	report(conflictKeepsNS(name.New("x", "y"),
		[]plan.Request{{From: "x", To: "a"}, {From: "y", To: "a"}},
		plan.ErrDuplicateTarget), "conflict: two requests share new name")
	report(conflictKeepsNS(name.New("x"),
		[]plan.Request{{From: "x", To: "a"}, {From: "x", To: "b"}},
		plan.ErrDuplicateSource), "conflict: same old name twice")
	report(conflictKeepsNS(name.New("a"),
		[]plan.Request{{From: "ghost", To: "b"}},
		plan.ErrMissingSource), "conflict: old name missing")
	report(conflictKeepsNS(name.New("x", "a"),
		[]plan.Request{{From: "x", To: "a"}},
		plan.ErrTargetExists), "conflict: target exists and stays")

	base := []plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "d", To: "e"},
		{From: "p", To: "q"}, {From: "q", To: "p"}}
	wantSteps, wantCycles := plan.Order(base)
	det := true
	for i := 0; i < 20; i++ {
		sh := append([]plan.Request(nil), base...)
		rand.New(rand.NewSource(int64(i))).Shuffle(len(sh), func(x, y int) { sh[x], sh[y] = sh[y], sh[x] })
		s2, c2 := plan.Order(sh)
		det = det && reflect.DeepEqual(s2, wantSteps) && reflect.DeepEqual(c2, wantCycles)
	}
	report(det, "deterministic steps over 20 shuffles")

	ns3 := name.New("a", "b", "c")
	reqs3 := []plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "a"}}
	ord3, cyc3 := plan.Order(reqs3)
	cs3, tm3, err3 := cycle.Break(ns3, reqs3, cyc3)
	report(err3 == nil && len(ord3) == 0 && len(tm3) == 1 &&
		runChecked(ns3, cs3) && ns3.Equal(name.New("a", "b", "c")),
		"3-cycle: 1 temp, no overwrite, final set intact")

	pre := []string{"a", "b"}
	for i := 0; i < 50; i++ {
		pre = append(pre, fmt.Sprintf("tmp~%d", i))
	}
	nsT := name.New(pre...)
	reqsT := []plan.Request{{From: "a", To: "b"}, {From: "b", To: "a"}}
	_, cycT := plan.Order(reqsT)
	csT, tmT, errT := cycle.Break(nsT, reqsT, cycT)
	report(errT == nil && len(tmT) == 1 && tmT[0] == "tmp~50" && runChecked(nsT, csT),
		"temp names preoccupied: retries to tmp~50 and succeeds")

	var namesC []string
	var reqsC []plan.Request
	for i := 0; i < 10; i++ {
		x, y := fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i)
		namesC = append(namesC, x, y)
		reqsC = append(reqsC, plan.Request{From: x, To: y}, plan.Request{From: y, To: x})
	}
	_, cycC := plan.Order(reqsC)
	_, tmC, errC := cycle.Break(name.New(namesC...), reqsC, cycC)
	report(errC == nil && len(cycC) == 10 && len(tmC) == 10, "temp count equals cycle count (10)")

	dir, derr := os.MkdirTemp("", "ontology-demo")
	report(derr == nil, "temp dir for logs")
	if derr != nil {
		fmt.Printf("TOTAL %d/%d OK\n", passed, total)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	rbOK := true
	for _, k := range []int{1, 3, 5} {
		ns := name.New("a", "b", "c", "d", "e")
		reqs := []plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "c", To: "d"},
			{From: "d", To: "e"}, {From: "e", To: "f"}}
		st, _ := pipeline(ns, reqs)
		ex := &apply.Executor{NS: ns, Hook: func(i int, _ plan.Step) error {
			if i == k-1 {
				return errors.New("injected")
			}
			return nil
		}}
		err := ex.Run(st, filepath.Join(dir, "rb.log"))
		rbOK = rbOK && errors.Is(err, apply.ErrStepFailed) && ns.Equal(name.New("a", "b", "c", "d", "e"))
	}
	report(rbOK, "failure at step k=1,mid,last rolls back fully")

	nsU := name.New("a", "b", "c", "p", "q")
	reqsU := []plan.Request{{From: "a", To: "b"}, {From: "b", To: "c"}, {From: "p", To: "q"}, {From: "q", To: "p"}}
	stU, _ := pipeline(nsU, reqsU)
	logU := filepath.Join(dir, "u.log")
	okU := (&apply.Executor{NS: nsU}).Run(stU, logU) == nil
	unrec, errU := undo.Undo(nsU, logU)
	okU = okU && errU == nil && len(unrec) == 0 && nsU.Equal(name.New("a", "b", "c", "p", "q"))
	unrec2, errU2 := undo.Undo(nsU, logU)
	okU = okU && errU2 == nil && len(unrec2) == 0 && nsU.Equal(name.New("a", "b", "c", "p", "q"))
	report(okU, "undo restores namespace; second undo is a no-op")

	data := apply.EncodeLog([]plan.Step{{From: "a", To: "x"}, {From: "b", To: "y"}, {From: "c", To: "z"}})
	hl := bytes.IndexByte(data, '\n') + 1
	_, _, e1 := apply.ParseLog(data[:5])
	_, _, e2 := apply.ParseLog(data[:hl+3])
	_, _, e3 := apply.ParseLog(data[:hl+6])
	report(errors.Is(e1, apply.ErrHeaderIncomplete) && errors.Is(e2, apply.ErrRecordIncomplete) &&
		errors.Is(e3, apply.ErrCRCMismatch), "truncation classes: header/record/crc")

	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
