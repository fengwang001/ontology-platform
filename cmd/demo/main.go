// Command demo exercises the SEMI JOIN incremental view for ontology-416.
// It reads no arguments and performs no network access; exit code 0 means
// every verdict below printed OK. Output is at most ten lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"ontology/api"
)

func sp(v string) *string { return &v }

var failed bool

func verdict(name string, good bool) {
	if !good {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[good], name)
}

func main() {
	// Prescribed eight-step sequence (NOTES.md), via the public api facade.
	j, err := api.New(8)
	if err != nil {
		fmt.Println("FAIL new:", err)
		os.Exit(1)
	}
	ops := []func() error{
		func() error { return j.AddLeft(1, sp("a")) },
		func() error { return j.AddLeft(2, sp("a")) },
		func() error { return j.AddRight(sp("a")) },
		func() error { return j.AddLeft(3, sp("b")) },
		func() error { return j.AddRight(sp("b")) },
		func() error { return j.AddLeft(4, nil) },
		func() error { return j.DelRight(sp("a")) },
		func() error { return j.AddRight(nil) },
	}
	want8 := [][]int64{{}, {}, {1, 2}, {1, 2}, {1, 2, 3}, {1, 2, 3}, {3}, {3}}
	views := make([][]int64, 0, 8)
	for _, f := range ops {
		if err := f(); err != nil {
			fmt.Println("FAIL eight-step op:", err)
			os.Exit(1)
		}
		views = append(views, j.View())
	}
	verdict("eight-step views "+fmt.Sprint(views), reflect.DeepEqual(views, want8))

	// (甲) ref a: 1->2 must not multiply rows; (step-7) rows extinguish;
	// (乙/step-8) NULL right never lights the NULL left row L4.
	d, _ := api.New(8)
	_ = d.AddLeft(1, sp("a"))
	_ = d.AddLeft(2, sp("a"))
	_ = d.AddRight(sp("a"))
	once := d.View()
	_ = d.AddRight(sp("a"))
	verdict("semi ref a=2 keeps [1 2] (no inner-join multiply)",
		reflect.DeepEqual(once, []int64{1, 2}) && reflect.DeepEqual(d.View(), []int64{1, 2}))
	verdict("step7 extinguishes a-rows -> [3]", reflect.DeepEqual(views[6], []int64{3}))
	verdict("step8 NULL matches nothing; L4 absent -> [3]",
		reflect.DeepEqual(views[7], []int64{3}) && !contains(views[7], 4))

	// Four distinct, decidable sentinel errors (plus the dup-id kind).
	errs := []error{
		api.ErrInvalidMaxLeft, api.ErrLeftIDExists, api.ErrLeftTableFull,
		api.ErrLeftNotFound, api.ErrRightUnderflow,
	}
	distinct := true
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				distinct = false
			}
		}
	}
	_, e0 := api.New(0)
	e1 := d2dup()
	e2 := d2full()
	e3 := d2missing()
	e4 := d2underflow()
	verdict("four(+dup) distinct sentinel errors",
		distinct && errors.Is(e0, api.ErrInvalidMaxLeft) && errors.Is(e1, api.ErrLeftIDExists) &&
			errors.Is(e2, api.ErrLeftTableFull) && errors.Is(e3, api.ErrLeftNotFound) &&
			errors.Is(e4, api.ErrRightUnderflow))

	// A rejected op leaves no trace and the instance stays usable.
	u, _ := api.New(1)
	_ = u.AddLeft(1, sp("a"))
	before := u.View()
	rejected := u.AddLeft(2, sp("a")) != nil && u.DelLeft(9) != nil && u.DelRight(sp("z")) != nil
	unchanged := reflect.DeepEqual(u.View(), before)
	_ = u.AddRight(sp("a"))
	verdict("rejected ops leave no trace; still usable -> [1]",
		rejected && unchanged && reflect.DeepEqual(u.View(), []int64{1}))

	// Indexed scan tiers (m=100..10000) and concurrent reader agreement are
	// checked inside SelfCheck; lastScan is unexported and never read here.
	fresh, _ := api.New(8)
	verdict("SelfCheck: batch equality, O(1) key-indexed scan, concurrent readers",
		fresh.SelfCheck() == nil && j.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

func contains(xs []int64, v int64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func d2dup() error {
	j, _ := api.New(4)
	_ = j.AddLeft(1, sp("a"))
	return j.AddLeft(1, sp("b"))
}
func d2full() error {
	j, _ := api.New(1)
	_ = j.AddLeft(1, sp("a"))
	return j.AddLeft(2, sp("a"))
}
func d2missing() error {
	j, _ := api.New(4)
	return j.DelLeft(9)
}
func d2underflow() error {
	j, _ := api.New(4)
	return j.DelRight(sp("z"))
}
