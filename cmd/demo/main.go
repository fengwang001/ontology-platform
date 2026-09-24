package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/api"
	"ontology/rank"
)

// line prints one OK/FAIL result line, optionally with element triples.
func line(step string, ok bool, v *api.View, ids ...string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
	}
	s := ""
	for _, id := range ids {
		rn, rk, dr, _ := v.Get(id)
		s += fmt.Sprintf(" %s(%d,%d,%d)", id, rn, rk, dr)
	}
	fmt.Printf("%s %s%s\n", tag, step, s)
}

func main() {
	v := api.New()
	ok := func(e error) bool { return e == nil }
	line("1 Ins A100", ok(v.Insert("A", 100)), v, "A")
	line("2 Ins B90", ok(v.Insert("B", 90)), v, "A", "B")
	line("3 Ins C100: C tie RANK, B RANK/DENSE", ok(v.Insert("C", 100)), v, "A", "C", "B")
	line("4 Ins D80", ok(v.Insert("D", 80)), v, "A", "C", "B", "D")
	line("5 Ins E90", ok(v.Insert("E", 90)), v, "A", "C", "B", "E", "D")
	line("6 Del C: B and D triples", ok(v.Delete("C")), v, "A", "B", "E", "D")

	// Three distinct decidable errors; state survives the rejections and the
	// view keeps working afterwards.
	line("errors distinct + no trace", errorCheck(), v)
	line("new-top insert rewrite bounded in m", rank.ComplexityBoundHolds(), v)
	line("concurrent inserts: ROW_NUMBER bijection + selfcheck", concurrentCheck() && v.SelfCheck(), v)
}

func errorCheck() bool {
	v := api.New()
	v.Insert("A", 100)
	if !errors.Is(v.Insert("", 1), api.ErrEmptyID) ||
		!errors.Is(v.Insert("A", 1), api.ErrDuplicateID) ||
		!errors.Is(v.Delete("x"), api.ErrIDNotFound) {
		return false
	}
	rn, _, _, has := v.Get("A") // A untouched after the three rejections
	return has && rn == 1 && v.Insert("B", 90) == nil && v.Delete("A") == nil
}

func concurrentCheck() bool {
	const n = 200
	v := api.New()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; v.Insert(fmt.Sprintf("id%04d", i), int64(i)) }(i)
	}
	close(start)
	wg.Wait()
	seen := map[int]bool{}
	for i := 0; i < n; i++ {
		rn, _, _, present := v.Get(fmt.Sprintf("id%04d", i))
		if !present || rn < 1 || rn > n || seen[rn] {
			return false
		}
		seen[rn] = true
	}
	return len(seen) == n && v.SelfCheck()
}
