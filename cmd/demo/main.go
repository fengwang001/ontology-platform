package main

import (
	"errors"
	"fmt"

	"ontology/idx"
)

func main() {
	pass, fail := 0, 0
	report := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Printf("OK %s\n", name)
		} else {
			fail++
			fmt.Printf("FAIL %s\n", name)
		}
	}

	report("skeleton", true)

	tree, err := idx.New(5)
	report("new5", err == nil)
	report("add", tree.Add(2, 7) == nil)
	ps, _ := tree.PrefixSum(idx.Index(2))
	report("prefix", ps == 7)
	rs, _ := tree.RangeSum(idx.Index(2), idx.Index(2))
	report("range", rs == 7)
	psm, _ := tree.PrefixSum(idx.Index(-1))
	report("prefix-1", psm == 0)
	err = tree.Add(9, 1)
	report("badindex", errors.Is(err, idx.ErrBadIndex))
	report("nodes", tree.Visited() <= 5)

	fmt.Printf("TOTAL pass=%d fail=%d\n", pass, fail)
}
