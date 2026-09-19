package main

import (
	"fmt"
	"math"

	"ontology"
)

var fails int

func verdict(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fails++
	fmt.Printf("FAIL %s\n", name)
}

func pushAll(s *ontology.Selector, order []string, scores map[string]float64) {
	for _, id := range order {
		s.Push(id, scores[id])
	}
}

func snapshotIDs(s *ontology.Selector) []string {
	snap := s.Snapshot()
	ids := make([]string, len(snap))
	for i, e := range snap {
		ids[i] = e.ID
	}
	return ids
}

func eq(a, b []string) bool {
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
	// 1. Desc ties broken by ID ascending.
	d, _ := ontology.New(3, ontology.Desc)
	pushAll(d, []string{"z", "a", "m", "b", "y"}, map[string]float64{
		"z": 9, "a": 9, "m": 9, "b": 1, "y": 5,
	})
	verdict("Desc tie -> ID ascending [a m z]",
		eq(snapshotIDs(d), []string{"a", "m", "z"}))

	// 2. Asc ties broken by ID ascending (direction does not flip the rule).
	a, _ := ontology.New(3, ontology.Asc)
	pushAll(a, []string{"z", "a", "m", "b", "y"}, map[string]float64{
		"z": 1, "a": 1, "m": 1, "b": 9, "y": 5,
	})
	verdict("Asc tie -> ID ascending [a m z]",
		eq(snapshotIDs(a), []string{"a", "m", "z"}))

	// 3. Tie straddling the K/K+1 boundary keeps smaller IDs.
	cut, _ := ontology.New(2, ontology.Desc)
	pushAll(cut, []string{"ccc", "aaa", "bbb", "low"}, map[string]float64{
		"ccc": 7, "aaa": 7, "bbb": 7, "low": 0,
	})
	verdict("tie across K boundary keeps [aaa bbb]",
		eq(snapshotIDs(cut), []string{"aaa", "bbb"}))

	// 4. Shuffled arrival orders give identical snapshots.
	scores := map[string]float64{"q": 5, "a": 1, "z": 5, "b": 4, "m": 5, "x": 0}
	run := func(order []string) []string {
		s, _ := ontology.New(3, ontology.Desc)
		pushAll(s, order, scores)
		return snapshotIDs(s)
	}
	first := run([]string{"q", "a", "z", "b", "m", "x"})
	second := run([]string{"x", "m", "b", "z", "a", "q"})
	verdict(fmt.Sprintf("shuffled pushes identical %v", first), eq(first, second))

	// 5. NaN rejected and counted.
	n, _ := ontology.New(3, ontology.Desc)
	n.Push("ok1", 2)
	n.Push("nan1", math.NaN())
	n.Push("ok2", 5)
	n.Push("nan2", math.NaN())
	verdict(fmt.Sprintf("NaN skipped=%d, held=%d", n.Skipped(), n.Len()),
		n.Skipped() == 2 && n.Len() == 2)

	// 6. +0.0 and -0.0 tie, ID order decides.
	z, _ := ontology.New(3, ontology.Desc)
	z.Push("neg", math.Float64frombits(0x8000000000000000))
	z.Push("pos", 0.0)
	z.Push("mid", 0.0)
	verdict("+0/-0 equal -> [mid neg pos]",
		eq(snapshotIDs(z), []string{"mid", "neg", "pos"}))

	// 7. Same ID overwritten, then drops out of top K.
	o, _ := ontology.New(2, ontology.Desc)
	o.Push("a", 10)
	o.Push("b", 8)
	o.Push("c", 6)
	o.Push("a", 1)
	verdict(fmt.Sprintf("overwrite drops a out, held=%v", snapshotIDs(o)),
		eq(snapshotIDs(o), []string{"b"}) && o.Len() == 1)

	// 8. K <= 0 returns a decidable error.
	_, err := ontology.New(0, ontology.Desc)
	verdict("K<=0 returns ErrInvalidCapacity", ontology.IsInvalidCapacity(err))

	// 9. Long stream: held count never exceeds K.
	big, _ := ontology.New(10, ontology.Asc)
	maxHeld := 0
	for i := 0; i < 10000; i++ {
		big.Push(fmt.Sprintf("id-%05d", i), math.Sin(float64(i)))
		if h := big.Len(); h > maxHeld {
			maxHeld = h
		}
	}
	verdict(fmt.Sprintf("long stream held count peaks at %d (<=10)", maxHeld),
		maxHeld == 10)

	if fails == 0 {
		fmt.Println("TOTAL: 9/9 checks OK")
	} else {
		fmt.Printf("TOTAL: %d check(s) FAILed\n", fails)
	}
}
