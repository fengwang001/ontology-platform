// Command demo 演示可撤销批量重命名与冲突解析器的全部判定项。
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"

	"ontology/apply"
	"ontology/name"
	"ontology/plan"
	"ontology/undo"
)

var total, failed int

func check(label string, ok bool) {
	total++
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, label)
}

func main() {
	ns := name.New("", "a/b", `c\d`)
	check("names: empty+separator legal", name.Valid("") && ns.Has("") && ns.Has("a/b") && ns.Has(`c\d`))

	p1, err := plan.Build(name.New("a", "b"), []name.Rename{{Old: "a", New: "b"}, {Old: "b", New: "c"}})
	_, ok1 := runSteps([]string{"a", "b"}, p1.Steps)
	check("chain {a->b,b->c}: order b->c first, no overwrite", err == nil && ok1 &&
		slices.Equal(p1.Steps, []name.Rename{{Old: "b", New: "c"}, {Old: "a", New: "b"}}))

	p2, err := plan.Build(name.New("a", "b", "c"), []name.Rename{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "c", New: "a"}})
	ns2, ok2 := runSteps([]string{"a", "b", "c"}, p2.Steps)
	check("3-cycle: one temp, every target absent", err == nil && p2.Temps == 1 && ok2 &&
		ns2.Equal(name.New("a", "b", "c")))

	pre := []string{"a", "b"}
	for i := 0; i < 16; i++ {
		pre = append(pre, fmt.Sprintf(".tmp-rename-%d", i))
	}
	p3, err := plan.Build(name.New(pre...), []name.Rename{{Old: "a", New: "b"}, {Old: "b", New: "a"}})
	ns3, ok3 := runSteps(pre, p3.Steps)
	check("temp names preoccupied: still succeeds", err == nil && p3.Temps == 1 && ok3 &&
		ns3.Equal(name.New(pre...)))

	ok4 := true
	for _, c := range []struct {
		sent error
		ns   *name.Namespace
		reqs []name.Rename
	}{
		{plan.ErrTargetExists, name.New("a", "z"), []name.Rename{{Old: "a", New: "z"}}},
		{plan.ErrDuplicateTarget, name.New("a", "b"), []name.Rename{{Old: "a", New: "x"}, {Old: "b", New: "x"}}},
		{plan.ErrDuplicateSource, name.New("a"), []name.Rename{{Old: "a", New: "x"}, {Old: "a", New: "y"}}},
		{plan.ErrSourceMissing, name.New("a"), []name.Rename{{Old: "ghost", New: "x"}}},
	} {
		before := c.ns.Snapshot()
		if _, err := plan.Build(c.ns, c.reqs); !errors.Is(err, c.sent) || !slices.Equal(before, c.ns.Snapshot()) {
			ok4 = false
		}
	}
	check("four conflict kinds, ns untouched", ok4)

	base := []name.Rename{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "c", New: "a"}, {Old: "d", New: "e"}, {Old: "e", New: "f"}}
	ok8 := true
	var want []name.Rename
	for i := 0; i < 20; i++ {
		sh := slices.Clone(base)
		rand.Shuffle(len(sh), func(i, j int) { sh[i], sh[j] = sh[j], sh[i] })
		p, err := plan.Build(name.New("a", "b", "c", "d", "e"), sh)
		if err != nil {
			ok8 = false
			break
		}
		if want == nil {
			want = p.Steps
		} else if !slices.Equal(want, p.Steps) {
			ok8 = false
		}
	}
	check("deterministic steps over 20 shuffles", ok8)

	var names9 []string
	var reqs9 []name.Rename
	for i := 0; i < 10; i++ {
		x, y := fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i)
		names9 = append(names9, x, y)
		reqs9 = append(reqs9, name.Rename{Old: x, New: y}, name.Rename{Old: y, New: x})
	}
	p9, err := plan.Build(name.New(names9...), reqs9)
	_, ok9 := runSteps(names9, p9.Steps)
	check("temps == cycle count (10 cycles)", err == nil && p9.Temps == 10 && ok9)

	dir, _ := os.MkdirTemp("", "ontology-demo")
	defer os.RemoveAll(dir)

	ns5 := name.New("a", "b", "c", "d")
	reqs5 := []name.Rename{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "c", New: "d"}, {Old: "d", New: "e"}}
	before5 := ns5.Snapshot()
	if _, err := apply.Execute(ns5, reqs5, filepath.Join(dir, "fail.journal"), &apply.Options{FailAt: 2}); err == nil {
		check("fail at step k: full rollback", false)
	} else {
		check("fail at step k: full rollback", slices.Equal(before5, ns5.Snapshot()))
	}

	ns6 := name.New("a", "b", "c")
	reqs6 := []name.Rename{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "c", New: "a"}}
	before6 := ns6.Snapshot()
	j6 := filepath.Join(dir, "ok.journal")
	_, err6 := apply.Execute(ns6, reqs6, j6, nil)
	rep6, err6u := undo.Undo(ns6, j6)
	check("undo restores exact set", err6 == nil && err6u == nil && rep6.Undone == 4 &&
		slices.Equal(before6, ns6.Snapshot()))

	ns7 := name.New("a", "b", "c")
	j7 := filepath.Join(dir, "trunc.journal")
	_, err7 := apply.Execute(ns7, reqs6, j7, nil)
	data, _ := os.ReadFile(j7)
	seen := map[error]bool{}
	unclassified := 0
	for cut := 1; cut < len(data); cut++ {
		_, _, perr := undo.Parse(data[:cut])
		switch {
		case errors.Is(perr, undo.ErrHeaderIncomplete):
			seen[undo.ErrHeaderIncomplete] = true
		case errors.Is(perr, undo.ErrRecordIncomplete):
			seen[undo.ErrRecordIncomplete] = true
		case errors.Is(perr, undo.ErrCRCMismatch):
			seen[undo.ErrCRCMismatch] = true
		default:
			unclassified++
		}
	}
	check("truncation classes: header/record/crc", err7 == nil && unclassified == 0 &&
		seen[undo.ErrHeaderIncomplete] && seen[undo.ErrRecordIncomplete] && seen[undo.ErrCRCMismatch])

	fmt.Printf("TOTAL %d checks, %d failed\n", total, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// runSteps 逐步执行并断言每一步的目标名都不存在（无覆盖检查器）。
func runSteps(start []string, steps []name.Rename) (*name.Namespace, bool) {
	ns := name.New(start...)
	for _, s := range steps {
		if ns.Has(s.New) {
			return ns, false
		}
		if err := ns.Transact(func(tx *name.Tx) error { return tx.Rename(s.Old, s.New) }); err != nil {
			return ns, false
		}
	}
	return ns, true
}
