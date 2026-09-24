// Command demo exercises the nested-savepoint KV store and prints OK/FAIL
// verdicts (at most 10 lines). Exit code is 0 only if every verdict passes.
package main

import (
	"errors"
	"fmt"

	"ontology/api"
)

type act struct {
	kind byte // S set, P savepoint, R rollback, X release
	key  string
	arg  int // Set value, or R/X target savepoint id
}

var abc = []string{"a", "b", "c"}

func snap(k *api.KV) string {
	out := ""
	for _, key := range abc {
		if v, ok := k.Get(key); ok {
			out += fmt.Sprintf("%s=%d ", key, v)
		}
	}
	if out == "" {
		return "{}"
	}
	return "{" + out[:len(out)-1] + "}"
}

func main() {
	ok := true
	check := func(name string, pass bool) {
		if pass {
			fmt.Println("OK   " + name)
		} else {
			ok = false
			fmt.Println("FAIL " + name)
		}
	}

	// Twelve-step section-three sequence through the public api facade.
	k, _ := api.New(100)
	seq := []act{
		{'S', "a", 1}, {'P', "", 0}, {'S', "a", 2}, {'P', "", 0},
		{'S', "b", 5}, {'X', "", 1}, {'S', "c", 8}, {'R', "", 0},
		{'S', "b", 3}, {'P', "", 0}, {'S', "c", 6}, {'R', "", 1},
	}
	got := make([]string, 12)
	var e6, e8, e12 error
	for i, a := range seq {
		switch a.kind {
		case 'S':
			_ = k.Set(a.key, a.arg)
		case 'P':
			k.Savepoint()
		case 'R':
			if i == 7 {
				e8 = k.RollbackTo(a.arg)
			} else {
				e12 = k.RollbackTo(a.arg)
			}
		case 'X':
			e6 = k.Release(a.arg)
		}
		got[i] = snap(k)
	}
	fmt.Println("12-step stores:", fmt.Sprint(got))
	want := []string{"{a=1}", "{a=1}", "{a=2}", "{a=2}", "{a=2 b=5}",
		"{a=2 b=5}", "{a=2 b=5 c=8}", "{a=1}", "{a=1 b=3}",
		"{a=1 b=3}", "{a=1 b=3 c=6}", "{a=1 b=3 c=6}"}
	twelve := true
	for i := range want {
		twelve = twelve && got[i] == want[i]
	}
	check("steps6/8/12+partial rollback: release keeps data, rb0->{a=1}, rb1 ErrNotFound, {a=1 b=3 c=6}",
		twelve && e6 == nil && e8 == nil && errors.Is(e12, api.ErrSavepointNotFound))

	// Invariant checks reuse the package's self-check building blocks.
	check("naive replay equivalence (random sequences)", api.CheckNaive() == nil)
	check("release changes no key/value and kills deeper ids", api.ReleaseScenario() == nil)
	check("four distinct errors; rejected ops leave no trace and stay usable", api.CheckErrors() == nil)
	locateOK := true
	for _, m := range []int{100, 1000, 10000} {
		if api.LocateScenario(m) != nil {
			locateOK = false
		}
	}
	check("locate is O(1) at m = 100, 1000, 10000", locateOK)
	check("concurrent Get: goroutines agree keywise", api.ConcurrentScenario(16) == nil)
	d, _ := api.New(100)
	check("SelfCheck passes", d.SelfCheck() == nil)

	if !ok {
		panic("demo checks failed")
	}
}
