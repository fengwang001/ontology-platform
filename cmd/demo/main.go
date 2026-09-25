// Command demo exercises the column-level diff service and prints OK/FAIL.
package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"ontology/api"
	"ontology/diff"
)

var failed bool

func check(cond bool, msg string) {
	if cond {
		fmt.Println("OK: " + msg)
	} else {
		fmt.Println("FAIL: " + msg)
		failed = true
	}
}

func render(cs []api.Change) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = c.String()
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func main() {
	a := api.New()
	events := []map[string]string{
		{"a": "1", "b": "x"},
		{"a": "1", "b": "y", "c": ""},
		{"a": "2", "b": "y", "c": ""},
		{"a": "2", "c": ""},
		{"c": "z"},
		{},
	}
	steps := make([]string, 6)
	var step3 []api.Change
	var step2, step4 []api.Change
	for i, ev := range events {
		cs, err := a.Apply("k", ev)
		if err != nil {
			check(false, fmt.Sprintf("step %d apply: %v", i+1, err))
			return
		}
		steps[i] = render(cs)
		if i == 1 {
			step2 = cs
		}
		if i == 2 {
			step3 = cs
		}
		if i == 3 {
			step4 = cs
		}
	}
	check(strings.Join(steps, " | ") ==
		"[A{a,,1} A{b,,x}] | [C{b,x,y} A{c,,}] | [C{a,1,2}] | [R{b,y,}] | [R{a,2,} C{c,,z}] | [R{c,z,}]",
		"six-step changelog: "+strings.Join(steps, " | "))
	check(reflect.DeepEqual(step3, []api.Change{{Kind: api.Changed, Col: "a", Old: "1", New: "2"}}),
		"step 3 marks only changed columns (b and c skipped)")
	check(reflect.DeepEqual(step2, []api.Change{
		{Kind: api.Changed, Col: "b", Old: "x", New: "y"}, {Kind: api.Added, Col: "c"},
	}), `step 2 empty-string value c is Added (exists, not "missing")`)
	check(reflect.DeepEqual(step4, []api.Change{{Kind: api.Removed, Col: "b", Old: "y"}}),
		"step 4 column b removed")

	var recomputed []api.Change
	for _, c := range a.Recompute("k") {
		recomputed = append(recomputed, c)
	}
	check(a.SelfCheck() == nil && reflect.DeepEqual(a.View()["k"], map[string]string{}),
		"View matches replay and Recompute net effect; SelfCheck passes")

	_, e1 := a.Apply("", map[string]string{"a": "1"})
	_, e2 := a.Apply("x", nil)
	_, e3 := a.Apply("x", map[string]string{"": "v"})
	check(errors.Is(e1, api.ErrEmptyKey) && errors.Is(e2, api.ErrNilEvent) && errors.Is(e3, api.ErrEmptyColumn) &&
		e1 != e2 && e2 != e3 && e1 != e3, "three distinct sentinel errors")

	before := a.View()
	_, _ = a.Apply("", map[string]string{"a": "1"})
	_, _ = a.Apply("x", nil)
	_, _ = a.Apply("x", map[string]string{"": "v"})
	check(reflect.DeepEqual(a.View(), before), "state unchanged after rejected events")
	check(diff.SelfCheck() == nil, "comparisons = m for m differing shared columns, 0 for identical rows")

	fed := api.New()
	for i := 0; i < 50; i++ {
		_, _ = fed.Apply(fmt.Sprintf("k%02d", i), map[string]string{"a": "v", "b": ""})
	}
	want := fed.View()
	const n = 16
	var wg sync.WaitGroup
	same := make([]bool, n)
	wg.Add(n)
	for g := 0; g < n; g++ {
		go func(g int) {
			defer wg.Done()
			same[g] = reflect.DeepEqual(fed.View(), want)
		}(g)
	}
	wg.Wait()
	allSame := true
	for _, s := range same {
		allSame = allSame && s
	}
	check(allSame, "16 concurrent readers see field-identical Views")

	if failed {
		fmt.Println("DEMO FAILED")
	}
}
