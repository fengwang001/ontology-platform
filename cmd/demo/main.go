// Command demo runs the OR-Set acceptance checks and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/orset"
)

var failed bool

func check(name string, err error) {
	if err != nil {
		failed = true
		fmt.Println(name + ": FAIL (" + err.Error() + ")")
		return
	}
	fmt.Println(name + ": OK")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// exec applies "A0 x" (add), "R1 x" (remove), "M0 1" (merge).
func exec(a *api.API, op string) error {
	r := int(op[1] - '0')
	if op[0] == 'M' {
		return a.Merge(r, int(op[3]-'0'))
	}
	if op[0] == 'A' {
		return a.Add(r, op[3:])
	}
	return a.Remove(r, op[3:])
}

func elements(a *api.API, r int) string {
	el, _ := a.Elements(r)
	return fmt.Sprint(el)
}

var steps = []struct{ op, wantA, wantB string }{
	{"A0 x", "map[x:[A1]]", "map[]"}, {"M1 0", "map[x:[A1]]", "map[x:[A1]]"},
	{"R1 x", "map[x:[A1]]", "map[]"}, {"A0 x", "map[x:[A1 A2]]", "map[]"},
	{"A0 y", "map[x:[A1 A2] y:[A3]]", "map[]"}, {"A1 y", "map[x:[A1 A2] y:[A3]]", "map[y:[B1]]"},
	{"R0 y", "map[x:[A1 A2]]", "map[y:[B1]]"}, {"M0 1", "map[x:[A2] y:[B1]]", "map[y:[B1]]"},
	{"M1 0", "map[x:[A2] y:[B1]]", "map[x:[A2] y:[B1]]"}, {"R1 x", "map[x:[A2] y:[B1]]", "map[y:[B1]]"},
	{"M0 1", "map[y:[B1]]", "map[y:[B1]]"},
}

func trace() error {
	a, _ := api.New(2, 100)
	for i, s := range steps {
		must(exec(a, s.op))
		if got := elements(a, 0) + "|" + elements(a, 1); got != s.wantA+"|"+s.wantB {
			return fmt.Errorf("step %d: %s want %s|%s", i+1, got, s.wantA, s.wantB)
		}
	}
	return nil
}

func variant() error { // (丙): step 4 moved before step 2
	a, _ := api.New(2, 100)
	for _, op := range []string{"A0 x", "A0 x", "M1 0", "R1 x", "A0 y", "A1 y", "R0 y", "M0 1"} {
		must(exec(a, op))
	}
	if got := elements(a, 0); got != "map[y:[B1]]" {
		return fmt.Errorf("step8 A=%s, want map[y:[B1]]", got)
	}
	must(exec(a, "M1 0"))
	before := elements(a, 1)
	err := exec(a, "R1 x")
	if !errors.Is(err, orset.ErrNotFound) || elements(a, 1) != before {
		return fmt.Errorf("step10: err=%v stateChanged=%v", err, elements(a, 1) != before)
	}
	return nil
}

func errorKinds() error {
	a, _ := api.New(2, 1)
	must(a.Add(0, "e"))
	must(a.Add(1, "f"))
	cases := []error{func() error { _, e := api.New(0, 1); return e }(),
		a.Add(9, "e"), a.Add(0, ""), a.Remove(0, "zz"), a.Merge(0, 1)}
	want := []error{orset.ErrParam, orset.ErrParam, orset.ErrEmpty, orset.ErrNotFound, orset.ErrCapacity}
	for i, err := range cases {
		if !errors.Is(err, want[i]) {
			return fmt.Errorf("case %d: %v, want %v", i, err, want[i])
		}
	}
	kinds := []error{orset.ErrParam, orset.ErrEmpty, orset.ErrNotFound, orset.ErrCapacity}
	for i, x := range kinds {
		for _, y := range kinds[i+1:] {
			if errors.Is(x, y) || errors.Is(y, x) {
				return errors.New("error kinds not distinct")
			}
		}
	}
	return nil
}

func concurrent() error {
	const n, ops = 4, 60
	a, _ := api.New(n, n*ops+1)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				e := fmt.Sprintf("g%d-%d", g, i)
				must(a.Add(g, e))
				if i%3 == 2 {
					must(a.Remove(g, e))
				}
				must(a.Merge((g+i)%n, (g+i+1)%n))
			}
		}(g)
	}
	wg.Wait()
	must(a.SyncAll())
	for r := 0; r < n; r++ {
		el, _ := a.Elements(r)
		if len(el) != n*(ops-ops/3) {
			return fmt.Errorf("replica %d: %d elements, want %d", r, len(el), n*(ops-ops/3))
		}
	}
	return nil
}

func main() {
	check(fmt.Sprintf("trace 11 steps (live tags @8 A=%s, @11 A=%s)", steps[7].wantA, steps[10].wantA), trace())
	check("variant step4-before-step2 (step8 A=map[y:[B1]], step10 ErrNotFound)", variant())
	a, _ := api.New(2, 100)
	for i, e := range a.SelfCheck() {
		check("self-check: "+[]string{"random vs naive", "merge laws", "add-wins", "no side effect"}[i], e)
	}
	check("error kinds distinct", errorKinds())
	fmt.Println("indexed lookup not O(m): pinned by TestIndexedLookup (counter unexported)")
	check("concurrent add/remove/merge", concurrent())
	if failed {
		os.Exit(1)
	}
}
