package median_test

import (
	"fmt"

	"ontology/median"
)

func Example() {
	tr := median.New()

	for _, v := range []int{3, 1, 4} {
		if err := tr.Add(v); err != nil {
			panic(err)
		}
		m, _ := tr.Median()
		fmt.Printf("add %d -> len=%d median=%d\n", v, tr.Len(), m)
	}
	if err := tr.Remove(1); err != nil {
		panic(err)
	}
	m, _ := tr.Median()
	fmt.Printf("remove 1 -> len=%d median=%d\n", tr.Len(), m)

	if err := tr.Remove(9); err != nil {
		fmt.Printf("remove 9 -> %v\n", err)
	}

	// Output:
	// add 3 -> len=1 median=3
	// add 1 -> len=2 median=1
	// add 4 -> len=3 median=3
	// remove 1 -> len=2 median=3
	// remove 9 -> median: value not present in multiset: operation 0 (remove 9): no live copy
}
