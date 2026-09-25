package main

import (
	"errors"
	"fmt"

	"ontology/bit"
	checknaive "ontology/check"
	"ontology/idx"
)

func main() {
	total := 8
	pass := 0
	mustOK := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Println("OK  ", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}

	tr, err := bit.New(5)
	ref := checknaive.NewNaive(5)
	for i := 0; i < 5; i++ {
		_ = tr.Add(i, int64(i+1))
		_ = ref.Add(i, int64(i+1))
	}
	p15, _ := tr.PrefixSum(4)
	n15, _ := ref.PrefixSum(4)
	empty, _ := tr.PrefixSum(-1)
	rng, _ := tr.RangeSum(1, 3)
	rnn, _ := ref.RangeSum(1, 3)
	badIdx := tr.Add(9, 1)
	_, badRange := tr.RangeSum(3, 1)
	_, badSize := bit.New(-1)
	st, err := idx.New(3)
	if err == nil {
		_ = st.Add(idx.Index(1), 7)
	}
	typed, _ := st.PrefixSum(idx.Index(2))
	zero0, _ := bit.New(0)
	emptyZero, errZero := zero0.PrefixSum(-1)

	mustOK("new-zero-and-negative", errors.Is(badSize, bit.ErrBadSize) && zero0.Len() == 0)
	mustOK("add-prefix-matches-naive", p15 == n15 && p15 == 15)
	mustOK("prefix-minus-one-is-zero", empty == 0 && emptyZero == 0 && errZero == nil)
	mustOK("range-closed-matches-naive", rng == rnn && rng == 2+3+4)
	mustOK("bad-index-sentinel", errors.Is(badIdx, idx.ErrBadIndex))
	mustOK("bad-range-sentinel", errors.Is(badRange, idx.ErrBadRange))
	mustOK("idx-typed-wrapper", typed == 7)
	mustOK("nodes-visit-log-bounded", tr.LastNodes() <= 5)
	fmt.Printf("TOTAL %d/%d OK\n", pass, total)
}
