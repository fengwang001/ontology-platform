package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
)

type judge struct{ failed bool }

func (j *judge) line(ok bool, msg string) {
	tag := "OK  "
	if !ok {
		tag, j.failed = "FAIL", true
	}
	fmt.Println(tag, msg)
}

func main() {
	j := &judge{}
	t := api.New()

	// Section 3: the eight prescribed steps; run returns its own
	// display value ("OK" or a type), or the error's message on failure.
	step := func(n int, want string, run func() string) {
		got := run()
		j.line(got == want, fmt.Sprintf("%d %s => %s", n, t.RenderStack(), got))
	}
	okv := func(err error) string {
		if err != nil {
			return err.Error()
		}
		return "OK"
	}

	step(1, "OK", func() string { return okv(t.Declare("x", api.Int)) })
	step(2, "OK", func() string { t.Enter(); return "OK" })
	step(3, "OK", func() string { return okv(t.Declare("y", api.Bool)) })
	step(4, "OK", func() string { return okv(t.Declare("x", api.Bool)) })
	step(5, "bool", func() string { v, e := t.Lookup("x"); return result(v, e) })
	step(6, "OK", func() string { return okv(t.Exit()) })
	step(7, "int", func() string { v, e := t.Lookup("x"); return result(v, e) })
	step(8, api.ErrUndeclared.Error(), func() string { _, e := t.Lookup("y"); return okv(e) })

	// Three distinct sentinels; duplicate keeps the first binding;
	// rejected operations leave the stack untouched; SelfCheck covers
	// the invariants and the constant-probe guarantee at m=100..10000.
	distinct := api.ErrDuplicate != api.ErrUndeclared &&
		api.ErrUndeclared != api.ErrExitGlobal && api.ErrDuplicate != api.ErrExitGlobal
	snap := t.RenderStack()
	errDup := t.Declare("x", api.Bool)
	kept, _ := t.Lookup("x")
	_, errMiss := t.Lookup("zz")
	errGlobal := t.Exit()
	errsOK := errors.Is(errDup, api.ErrDuplicate) && errors.Is(errMiss, api.ErrUndeclared) &&
		errors.Is(errGlobal, api.ErrExitGlobal) && distinct && kept == api.Int &&
		t.RenderStack() == snap
	j.line(errsOK && t.SelfCheck() == nil, "3 distinct errors + no trace + selfcheck (incl. O(1) probes)")

	// Concurrent read-only lookups: every goroutine sees identical values.
	c := api.New()
	_ = c.Declare("w", api.Int)
	_ = c.Declare("x", api.Int)
	c.Enter()
	_ = c.Declare("x", api.Bool)
	const N = 64
	var wg sync.WaitGroup
	res := make([][2]api.T, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			res[g][0], _ = c.Lookup("x") // shadowed: bool
			res[g][1], _ = c.Lookup("w") // outer: int
		}(g)
	}
	wg.Wait()
	same := true
	for g := 1; g < N; g++ {
		if !reflect.DeepEqual(res[g], res[0]) {
			same = false
		}
	}
	j.line(same && res[0][0] == api.Bool && res[0][1] == api.Int, "concurrent lookups identical")

	if j.failed {
		os.Exit(1)
	}
}

func result(v api.T, err error) string {
	if err != nil {
		return err.Error()
	}
	return v.String()
}
