// Command demo exercises the streaming Top-K selector end to end and
// prints one OK/FAIL verdict per property, plus a final summary line.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"

	"ontology/topk"
)

var passed, total int

func check(name string, ok bool) {
	total++
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", verdict, name)
}

func ids(s *topk.Selector) []string {
	snap := s.Snapshot()
	out := make([]string, len(snap))
	for i, e := range snap {
		out[i] = e.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustNew(k int, dir topk.Direction) *topk.Selector {
	s, err := topk.New(k, dir)
	if err != nil {
		panic(err)
	}
	return s
}

func main() {
	desc := mustNew(4, topk.Desc)
	for _, id := range []string{"delta", "alpha", "charlie", "bravo"} {
		desc.Push(id, 7.0)
	}
	check("Desc ties break by ascending ID", equal(ids(desc), []string{"alpha", "bravo", "charlie", "delta"}))

	asc := mustNew(4, topk.Asc)
	for _, id := range []string{"delta", "alpha", "charlie", "bravo"} {
		asc.Push(id, 7.0)
	}
	check("Asc ties break by ascending ID", equal(ids(asc), []string{"alpha", "bravo", "charlie", "delta"}))

	cut := mustNew(2, topk.Desc)
	cut.Push("best", 10.0)
	cut.Push("zulu", 5.0)
	cut.Push("able", 5.0)
	check("tie across K boundary keeps smaller ID", equal(ids(cut), []string{"best", "able"}))

	base := []topk.Element{
		{ID: "e1", Score: 3.5}, {ID: "e2", Score: 3.5}, {ID: "e3", Score: -1},
		{ID: "e4", Score: 100}, {ID: "e5", Score: 0}, {ID: "e6", Score: 3.5},
		{ID: "e7", Score: -50}, {ID: "e8", Score: 42},
	}
	shuffled := mustNew(4, topk.Desc)
	perm := rand.New(rand.NewSource(7)).Perm(len(base))
	for _, i := range perm {
		shuffled.Push(base[i].ID, base[i].Score)
	}
	ordered := mustNew(4, topk.Desc)
	for _, e := range base {
		ordered.Push(e.ID, e.Score)
	}
	check("shuffled input yields identical snapshot", equal(ids(shuffled), ids(ordered)))

	nan := mustNew(3, topk.Desc)
	nan.Push("bad1", math.NaN())
	nan.Push("good", 1.0)
	nan.Push("bad2", math.NaN())
	check("NaN skipped and counted", nan.Skipped() == 2 && equal(ids(nan), []string{"good"}))

	zero := mustNew(2, topk.Desc)
	zero.Push("b", 0.0)
	zero.Push("a", math.Copysign(0, -1))
	check("+0.0 and -0.0 tie by ID", equal(ids(zero), []string{"a", "b"}))

	ow := mustNew(2, topk.Desc)
	ow.Push("a", 10.0)
	ow.Push("b", 9.0)
	ow.Push("c", 8.0)
	ow.Push("a", 1.0)
	ow.Push("d", 5.0)
	check("overwrite with worse score drops out", equal(ids(ow), []string{"b", "d"}))

	_, err := topk.New(0, topk.Desc)
	check("K<=0 returns decidable error", errors.Is(err, topk.ErrNonPositiveCapacity))

	long := mustNew(8, topk.Asc)
	within := true
	for i := 0; i < 100000; i++ {
		long.Push(fmt.Sprintf("id-%d", i), float64(i%997))
		if long.Size() > 8 {
			within = false
		}
	}
	check("held count never exceeds K on long stream", within && long.Size() == 8)

	fmt.Printf("TOTAL: %d/%d checks passed\n", passed, total)
}
