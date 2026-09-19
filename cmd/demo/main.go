// Command demo exercises the topk selector's ordering, tie-breaking,
// truncation, NaN handling, overwrite, and capacity guarantees.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"

	"ontology/topk"
)

var failures int

func check(name string, ok bool, detail any) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %v\n", status, name, detail)
}

func ids(items []topk.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func main() {
	// 1-2. Ties break by ascending ID in both directions.
	d, _ := topk.New(3, topk.Desc)
	for _, id := range []string{"c", "a", "b"} {
		d.Push(id, 1.0)
	}
	d.Push("z", 0.5)
	check("Desc ties ID-asc", reflect.DeepEqual(ids(d.Snapshot()), []string{"a", "b", "c"}), ids(d.Snapshot()))

	a, _ := topk.New(3, topk.Asc)
	for _, id := range []string{"c", "a", "b"} {
		a.Push(id, 1.0)
	}
	a.Push("z", 2.0)
	check("Asc ties ID-asc", reflect.DeepEqual(ids(a.Snapshot()), []string{"a", "b", "c"}), ids(a.Snapshot()))

	// 3. Tie straddling the K boundary keeps the lexicographically smaller ID.
	b, _ := topk.New(2, topk.Desc)
	b.Push("hi", 10.0)
	b.Push("omega", 5.0)
	b.Push("alpha", 5.0)
	check("tie at K boundary", reflect.DeepEqual(ids(b.Snapshot()), []string{"hi", "alpha"}), ids(b.Snapshot()))

	// 4. Shuffled pushes yield identical snapshots.
	base := []topk.Item{
		{ID: "e", Score: 3}, {ID: "b", Score: 1}, {ID: "a", Score: 1},
		{ID: "d", Score: 2}, {ID: "c", Score: 1}, {ID: "f", Score: 3},
	}
	var first []topk.Item
	same := true
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 2; trial++ {
		s, _ := topk.New(4, topk.Desc)
		for _, i := range rng.Perm(len(base)) {
			s.Push(base[i].ID, base[i].Score)
		}
		if trial == 0 {
			first = s.Snapshot()
		} else if !reflect.DeepEqual(s.Snapshot(), first) {
			same = false
		}
	}
	check("shuffled input same snapshot", same, ids(first))

	// 5. NaN scores are skipped and counted.
	n, _ := topk.New(3, topk.Desc)
	n.Push("a", 1.0)
	n.Push("bad1", math.NaN())
	n.Push("bad2", math.NaN())
	check("NaN skipped count", n.Skipped() == 2 && n.Len() == 1, fmt.Sprintf("skipped=%d", n.Skipped()))

	// 6. +0.0 and -0.0 are equal; ID decides.
	z, _ := topk.New(2, topk.Desc)
	z.Push("b", math.Copysign(0, -1))
	z.Push("a", 0.0)
	check("signed zero tie", reflect.DeepEqual(ids(z.Snapshot()), []string{"a", "b"}), ids(z.Snapshot()))

	// 7. Overwriting to a worse score drops the ID out of Top-K.
	o, _ := topk.New(2, topk.Desc)
	o.Push("a", 10.0)
	o.Push("b", 9.0)
	o.Push("a", 1.0)
	o.Push("d", 5.0)
	check("overwrite drops out", reflect.DeepEqual(ids(o.Snapshot()), []string{"b", "d"}), ids(o.Snapshot()))

	// 8. K <= 0 is a checkable error, not a panic.
	_, err := topk.New(0, topk.Desc)
	check("K<=0 error", err != nil, err)

	// 9. A long stream never holds more than K items.
	const k = 10
	s, _ := topk.New(k, topk.Asc)
	maxHeld := 0
	for i := 0; i < 100000; i++ {
		s.Push(fmt.Sprintf("id-%d", i), float64((i*7919)%500))
		if s.Len() > maxHeld {
			maxHeld = s.Len()
		}
	}
	check("held never exceeds K", maxHeld <= k, fmt.Sprintf("maxHeld=%d K=%d", maxHeld, k))

	fmt.Printf("TOTAL: %d checks, %d failed\n", 9, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
