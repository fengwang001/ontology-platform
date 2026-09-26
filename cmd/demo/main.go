// Command demo exercises the scoped symbol table and prints OK/FAIL lines.
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/api"
	"ontology/sym"
)

func main() {
	failures := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK " + name)
		} else {
			fmt.Println("FAIL " + name)
			failures++
		}
	}

	// Section 3: the eight prescribed steps, recording stack + result each.
	t := api.New()
	var trace []string
	add := func(op, res string) { trace = append(trace, fmt.Sprintf("%s => %s | %s", op, res, t.String())) }
	step := func(op string, e error) {
		r := "ok"
		if e != nil {
			r = e.Error()
		}
		add(op, r)
	}

	step("Declare x:int", t.Declare("x", sym.Int))
	t.Enter()
	step("Enter", nil)
	step("Declare y:bool", t.Declare("y", sym.Bool))
	step("Declare x:bool", t.Declare("x", sym.Bool))
	v, e := t.Lookup("x")
	add("Lookup x", string(v))
	xInner := e == nil && v == sym.Bool
	step("Exit", t.Exit())
	v, e = t.Lookup("x")
	add("Lookup x", string(v))
	xRestored := e == nil && v == sym.Int
	_, yerr := t.Lookup("y")
	yUndeclared := errors.Is(yerr, api.ErrUndeclared)
	add("Lookup y", errName(yerr))
	if err := t.Declare("z", sym.Int); err != nil {
		add("Declare z:int", err.Error())
	}
	dupErr := t.Declare("z", sym.Bool)
	add("Declare z:bool", errName(dupErr))
	dupRejected := errors.Is(dupErr, api.ErrDuplicate)
	fmt.Println("trace: " + strings.Join(trace, "  ;  "))
	check("eight-step trace", xInner && xRestored && yUndeclared)
	check("shadow=bool, restored=int, y undeclared, dup rejected", xInner && xRestored && yUndeclared && dupRejected)

	// Three pairwise distinct, decidable failure modes.
	distinct := api.ErrDuplicate != api.ErrUndeclared &&
		api.ErrDuplicate != api.ErrExitGlobal && api.ErrUndeclared != api.ErrExitGlobal
	check("three distinct sentinel errors", distinct && errors.Is(dupErr, api.ErrDuplicate) && errors.Is(yerr, api.ErrUndeclared))

	// Rejected operations leave the stack and bindings untouched.
	before := t.String()
	_ = t.Declare("z", sym.Bool) // duplicate, rejected
	_, _ = t.Lookup("ghost")     // undeclared, rejected
	exitErr := t.Exit()          // global only, rejected
	check("rejected ops leave no trace", errors.Is(exitErr, api.ErrExitGlobal) && t.String() == before)

	// O(1) probe budget at m = 100/1000/10000 plus the four invariants.
	check("self-check: 4 invariants + O(1) lookup budget", api.New().SelfCheck() == nil)

	// Concurrent read-only lookups agree value-for-value (barrier, no sleep).
	c := api.New()
	_ = c.Declare("a", sym.Int)
	_ = c.Declare("g", sym.Int)
	c.Enter()
	_ = c.Declare("a", sym.Bool)
	want := map[string]sym.T{"a": sym.Bool, "g": sym.Int}
	agree := true
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for n, w := range want {
				got, err := c.Lookup(n)
				if err != nil || got != w {
					mu.Lock()
					agree = false
					mu.Unlock()
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent lookups agree", agree)

	if failures == 0 {
		fmt.Println("OK demo")
	} else {
		fmt.Println("FAIL demo")
	}
}

func errName(e error) string {
	if e == nil {
		return "ok"
	}
	return e.Error()
}
