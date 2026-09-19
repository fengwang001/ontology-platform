package main

import (
	"fmt"
	"math"
	"reflect"

	"ontology"
)

func main() {
	passed := 0
	check := func(name string, ok bool) {
		if ok {
			passed++
			fmt.Printf("OK   %s\n", name)
			return
		}
		fmt.Printf("FAIL %s\n", name)
	}

	desc, _ := ontology.NewSelector(3, ontology.Desc)
	asc, _ := ontology.NewSelector(3, ontology.Asc)
	for _, id := range []string{"c", "a", "b"} {
		desc.Push(id, 1)
		asc.Push(id, 1)
	}
	wantTies := []ontology.Element{{ID: "a", Score: 1}, {ID: "b", Score: 1}, {ID: "c", Score: 1}}
	check("Desc/Asc equal scores both rank IDs ascending",
		reflect.DeepEqual(desc.Snapshot(), wantTies) && reflect.DeepEqual(asc.Snapshot(), wantTies))

	boundary, _ := ontology.NewSelector(2, ontology.Desc)
	boundary.Push("alpha", 5)
	boundary.Push("charlie", 5)
	boundary.Push("bravo", 5)
	check("Tie across K boundary keeps smaller IDs",
		reflect.DeepEqual(boundary.Snapshot(), []ontology.Element{{ID: "alpha", Score: 5}, {ID: "bravo", Score: 5}}))

	first := pushBatch([][2]interface{}{{"d", 4.0}, {"a", 1.0}, {"c", 2.0}, {"b", 3.0}})
	second := pushBatch([][2]interface{}{{"b", 3.0}, {"d", 4.0}, {"a", 1.0}, {"c", 2.0}})
	check("Shuffled arrival order gives identical snapshots", reflect.DeepEqual(first, second))

	nanSelector, _ := ontology.NewSelector(2, ontology.Desc)
	nanSelector.Push("bad-1", math.NaN())
	nanSelector.Push("good", 7)
	nanSelector.Push("bad-2", math.NaN())
	check("NaN elements are skipped and counted", nanSelector.Skipped() == 2 &&
		reflect.DeepEqual(nanSelector.Snapshot(), []ontology.Element{{ID: "good", Score: 7}}))

	zeroSelector, _ := ontology.NewSelector(2, ontology.Desc)
	zeroSelector.Push("b", math.Copysign(0, -1))
	zeroSelector.Push("a", 0)
	zeroSnapshot := zeroSelector.Snapshot()
	check("+0 and -0 are equal and tie by ID", len(zeroSnapshot) == 2 &&
		zeroSnapshot[0].ID == "a" && zeroSnapshot[1].ID == "b" && zeroSnapshot[1].Score == 0)

	overwrite, _ := ontology.NewSelector(2, ontology.Desc)
	overwrite.Push("a", 10)
	overwrite.Push("b", 8)
	overwrite.Push("a", 1)
	check("Overwritten ID can immediately fall out of Top-K",
		reflect.DeepEqual(overwrite.Snapshot(), []ontology.Element{{ID: "b", Score: 8}}))

	_, err := ontology.NewSelector(0, ontology.Desc)
	check("K <= 0 returns a detectable error", err == ontology.ErrInvalidCapacity)

	long, _ := ontology.NewSelector(7, ontology.Desc)
	maxLen := 0
	for i := 0; i < 500; i++ {
		long.Push(fmt.Sprintf("id-%03d", i), float64(i))
		if long.Len() > maxLen {
			maxLen = long.Len()
		}
	}
	check("Long stream keeps internal Len at most K", maxLen == 7 && long.Len() == 7)

	fmt.Printf("SUMMARY: %d/8 checks passed\n", passed)
}

func pushBatch(batch [][2]interface{}) []ontology.Element {
	selector, _ := ontology.NewSelector(3, ontology.Desc)
	for _, item := range batch {
		selector.Push(item[0].(string), item[1].(float64))
	}
	return selector.Snapshot()
}
