package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/foj"
	"ontology/rel"
)

var failed bool

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func fmtChanges(cs []rel.Change) string {
	var b strings.Builder
	for _, c := range cs {
		sign := "-"
		if c.Add {
			sign = "+"
		}
		fmt.Fprintf(&b, "%s[%s,%s,%s];", sign, c.Key, c.Lid, c.Rid)
	}
	return strings.TrimSuffix(b.String(), ";")
}

func fmtRows(rs []rel.Row) string {
	var b strings.Builder
	for _, r := range rs {
		fmt.Fprintf(&b, "[%s,%s,%s]", r.Key, r.Lid, r.Rid)
	}
	return b.String()
}
func checkEightSteps() bool {
	op := func(k foj.Kind, id string) api.Op { return api.Op{Kind: k, Key: "K", ID: id} }
	ops := []api.Op{
		op(api.PutLeft, "a"), op(api.PutRight, "x"), op(api.PutRight, "y"), op(api.PutLeft, "b"),
		op(api.DelLeft, "a"), op(api.DelLeft, "b"), op(api.DelRight, "x"), op(api.DelRight, "y"),
	}
	steps := []struct{ log, view string }{
		{"+[K,a,-]", "[K,a,-]"},
		{"-[K,a,-];+[K,a,x]", "[K,a,x]"},
		{"+[K,a,y]", "[K,a,x][K,a,y]"},
		{"+[K,b,x];+[K,b,y]", "[K,a,x][K,a,y][K,b,x][K,b,y]"},
		{"-[K,a,x];-[K,a,y]", "[K,b,x][K,b,y]"},
		{"-[K,b,x];-[K,b,y];+[K,-,x];+[K,-,y]", "[K,-,x][K,-,y]"},
		{"-[K,-,x]", "[K,-,y]"},
		{"-[K,-,y]", ""},
	}
	a := api.New()
	for i, s := range steps {
		cs, err := a.Apply(ops[i])
		if err != nil || fmtChanges(cs) != s.log || fmtRows(a.View()) != s.view {
			fmt.Printf("step %d: got %s view %s\n", i+1, fmtChanges(cs), fmtRows(a.View()))
			return false
		}
	}
	return true
}
func checkCrossing() bool {
	r := rel.New("K")
	if c, _ := r.PutLeft("l"); fmtChanges(c) != "+[K,l,-]" {
		return false
	}
	if c, _ := r.PutRight("r"); fmtChanges(c) != "-[K,l,-];+[K,l,r]" { // right 0->1
		return false
	}
	if c, _ := r.DelRight("r"); fmtChanges(c) != "-[K,l,r];+[K,l,-]" { // right 1->0
		return false
	}
	c, _ := r.DelLeft("l")
	return fmtChanges(c) == "-[K,l,-]" && r.Empty()
}
func checkErrors() bool {
	a := api.New()
	_, _ = a.Apply(api.Op{Kind: api.PutLeft, Key: "K", ID: "a"})
	_, e1 := a.Apply(api.Op{Kind: api.PutLeft, Key: "K", ID: "a"})
	_, e2 := a.Apply(api.Op{Kind: api.DelLeft, Key: "K", ID: "zz"})
	_, e3 := a.Apply(api.Op{Kind: api.PutLeft, Key: "", ID: "b"})
	if !errors.Is(e1, rel.ErrDuplicate) || !errors.Is(e2, rel.ErrNotFound) || !errors.Is(e3, foj.ErrEmptyKey) {
		return false
	}
	return !errors.Is(e1, e2) && !errors.Is(e2, e3) && !errors.Is(e1, e3)
}
func checkAtomic() bool {
	a := api.New()
	_, _ = a.Apply(api.Op{Kind: api.PutLeft, Key: "K", ID: "a"})
	snap := a.View()
	bad := []api.Op{{Kind: api.PutRight, Key: "K", ID: "x"}, {Kind: api.DelLeft, Key: "K", ID: "nope"}}
	if cs, err := a.Apply(bad...); err == nil || cs != nil {
		return false
	}
	if !slices.Equal(a.View(), snap) {
		return false
	}
	_, err := a.Apply(api.Op{Kind: api.PutRight, Key: "K", ID: "x"}) // still usable
	return err == nil
}
func checkConcurrent() bool {
	a := api.New()
	for i := 0; i < 20; i++ {
		_, _ = a.Apply(api.Op{Kind: api.PutLeft, Key: string(rune('A' + i%5)), ID: string(rune('a' + i))})
	}
	want := a.View()
	start := make(chan struct{})
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 50; i++ {
				if !slices.Equal(a.View(), want) {
					bad.Store(true)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	return !bad.Load()
}
func main() {
	report("eight-step changelog+view", checkEightSteps())
	report("zero-crossing padding/pair switch", checkCrossing())
	report("three distinct sentinel errors", checkErrors())
	report("rejected batch leaves state unchanged", checkAtomic())
	report("selfcheck invariants 1-4", api.New().SelfCheck() == nil)
	report("big-m checked-keys bounded", foj.ScalingHolds(100, 1000, 10000))
	report("concurrent read-only views identical", checkConcurrent())
	if failed {
		os.Exit(1)
	}
}
