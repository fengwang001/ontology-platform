// Command demo exercises the topk selector end to end and prints one
// OK/FAIL verdict line per check plus a final summary.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"

	"ontology/topk"
)

var failures int

func check(name string, ok bool, detail string) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %-28s %s\n", verdict, name, detail)
}

func ids(items []topk.Element) []string {
	out := make([]string, len(items))
	for i, e := range items {
		out[i] = e.ID
	}
	return out
}

func eqStr(a, b []string) bool {
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

func eqElem(a, b []topk.Element) bool {
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

func main() {
	// 1. Ties break by ascending ID under both directions.
	for _, dir := range []topk.Direction{topk.Desc, topk.Asc} {
		s, _ := topk.New(3, dir)
		s.Push("c", 7)
		s.Push("a", 7)
		s.Push("b", 7)
		name := "Desc tie -> ID asc"
		if dir == topk.Asc {
			name = "Asc tie -> ID asc"
		}
		check(name, eqStr(ids(s.Snapshot()), []string{"a", "b", "c"}),
			fmt.Sprint(ids(s.Snapshot())))
	}

	// 2. Tie straddling the K boundary keeps the smaller ID.
	s, _ := topk.New(2, topk.Desc)
	s.Push("zeta", 10)
	s.Push("alpha", 10)
	s.Push("mid", 10)
	check("tie at K boundary", eqStr(ids(s.Snapshot()), []string{"alpha", "mid"}),
		fmt.Sprint(ids(s.Snapshot())))

	// 3. Shuffled arrival order yields identical snapshots.
	base := make([]topk.Element, 40)
	for i := range base {
		base[i] = topk.Element{ID: fmt.Sprintf("id-%02d", i), Score: float64(i % 6)}
	}
	var ref []topk.Element
	same := true
	for seed := int64(0); seed < 5; seed++ {
		sh := append([]topk.Element(nil), base...)
		rand.New(rand.NewSource(seed)).Shuffle(len(sh), func(i, j int) {
			sh[i], sh[j] = sh[j], sh[i]
		})
		sel, _ := topk.New(8, topk.Desc)
		for _, e := range sh {
			sel.Push(e.ID, e.Score)
		}
		if seed == 0 {
			ref = sel.Snapshot()
		} else if !eqElem(ref, sel.Snapshot()) {
			same = false
		}
	}
	check("shuffle-invariant snapshot", same, fmt.Sprint(ids(ref)))

	// 4. NaN scores are skipped and counted.
	s, _ = topk.New(3, topk.Desc)
	s.Push("ok", 1)
	s.Push("bad", math.NaN())
	s.Push("bad2", math.NaN())
	check("NaN skipped", s.Skipped() == 2 && s.Len() == 1,
		fmt.Sprintf("skipped=%d len=%d", s.Skipped(), s.Len()))

	// 5. +0.0 and -0.0 tie and fall back to ID order.
	s, _ = topk.New(2, topk.Desc)
	s.Push("b", 0.0)
	s.Push("a", math.Copysign(0, -1))
	check("+0.0 == -0.0 tie", eqStr(ids(s.Snapshot()), []string{"a", "b"}),
		fmt.Sprint(ids(s.Snapshot())))

	// 6. Overwrite re-ranks immediately and can evict the ID.
	s, _ = topk.New(2, topk.Desc)
	s.Push("a", 5)
	s.Push("b", 4)
	s.Push("c", 3)
	s.Push("a", 2) // a falls behind b
	s.Push("d", 3) // d evicts the weakened a
	check("overwrite drops out", eqStr(ids(s.Snapshot()), []string{"b", "d"}),
		fmt.Sprint(ids(s.Snapshot())))

	// 7. K <= 0 is a detectable error.
	_, err := topk.New(0, topk.Desc)
	check("K<=0 error", errors.Is(err, topk.ErrInvalidCapacity), fmt.Sprint(err))

	// 8. A long stream never holds more than K elements.
	const k = 5
	s, _ = topk.New(k, topk.Asc)
	capped := true
	for i := 0; i < 50000; i++ {
		s.Push(fmt.Sprintf("n%d", i), float64((i*7919)%997))
		if s.Len() > k {
			capped = false
			break
		}
	}
	check("len capped at K", capped && s.Len() == k, fmt.Sprintf("len=%d K=%d", s.Len(), k))

	fmt.Printf("TOTAL %d checks, %d failed\n", 9, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
