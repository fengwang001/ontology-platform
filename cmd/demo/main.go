// Command demo 逐条判定采样剖析与热点归因器的核心性质。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/attrib"
	"ontology/dump"
	"ontology/sampler"
	"ontology/stack"
	"ontology/tree"
)

var passed, failed int

func check(ok bool, msg string) {
	if ok {
		passed++
		fmt.Println("OK " + msg)
	} else {
		failed++
		fmt.Println("FAIL " + msg)
	}
}

func main() {
	tr := tree.New()
	insert := func(frames ...string) {
		norm, trunc, err := stack.Normalize(frames, 8)
		if err != nil {
			return
		}
		tr.Insert(norm, trunc)
	}
	for i := 0; i < 5; i++ {
		insert("A", "F", "G", "F", "H")
	}
	for i := 0; i < 2; i++ {
		insert("A", "F", "G")
	}
	selfSum := tree.SumSelf(tr.Root)
	totalSum := tree.SumTotal(tr.Root)
	check(selfSum == tr.Samples && totalSum > selfSum,
		fmt.Sprintf("self-sum==samples (%d), total-sum (%d) > self-sum", selfSum, totalSum))

	tr2 := tree.New()
	deep := make([]string, 9)
	for i := range deep {
		deep[i] = fmt.Sprintf("D%d", i)
	}
	norm, trunc, _ := stack.Normalize(deep, 8)
	tr2.Insert(norm, trunc)
	node := tr2.Root
	for _, f := range norm {
		node = node.Children[f]
	}
	marked := node.Truncated
	check(tr2.TruncatedSamples == 1 && trunc && marked, "depth 9 > max 8: truncated count=1, node marked")

	tr3 := tree.New()
	frames := make([]string, 20)
	for i := range frames {
		frames[i] = fmt.Sprintf("f%d", i)
	}
	for i := 0; i < 100000; i++ {
		tr3.Insert(frames, false)
	}
	lookups, bound := tr3.Lookups(), int64(100000*20*4)
	check(lookups > 0 && lookups <= bound,
		fmt.Sprintf("insert cost: %d lookups <= bound %d", lookups, bound))

	tr4 := tree.New()
	clock := int64(0)
	busyCount := 0
	sp := sampler.New(tr4, 8,
		func() int64 { clock += 10; return clock },
		func() []string { return []string{"main", "work"} },
		func() bool { busyCount++; return busyCount <= 137 })
	for i := 0; i < 1000; i++ {
		sp.Step()
	}
	selfSum4 := tree.SumSelf(tr4.Root)
	check(selfSum4+sp.Dropped() == 1000 && sp.Dropped() == 137,
		fmt.Sprintf("drops: self(%d)+dropped(%d)==1000", selfSum4, sp.Dropped()))

	tr5 := tree.New()
	script := []int64{10, 20, 30, 5, 40, 50}
	idx := 0
	sp2 := sampler.New(tr5, 8,
		func() int64 { v := script[idx]; idx++; return v },
		func() []string { return []string{"main"} },
		nil)
	for range script {
		sp2.Step()
	}
	check(sp2.Anomalous() == 1 && tree.SumSelf(tr5.Root) == int64(len(script))-1,
		fmt.Sprintf("clock rollback: anomalous=%d, identity holds", sp2.Anomalous()))

	ft := attrib.FunctionTotals(tr)
	check(ft["F"] == 7, fmt.Sprintf("recursive F: function total=%d == outermost (not 12)", ft["F"]))

	tr6 := tree.New()
	for i := 0; i < 5; i++ {
		tr6.Insert([]string{"A", "B", "C"}, false)
	}
	data := dump.Marshal(tr6)
	_, _, errH := dump.Recover(data[:10])
	_, _, errR := dump.Recover(data[:dump.HeaderSize+30])
	_, _, errC := dump.Recover(data[:len(data)-1])
	check(errors.Is(errH, dump.ErrHeaderIncomplete), "truncate@10 -> header incomplete")
	check(errors.Is(errR, dump.ErrRecordIncomplete), "truncate@58 -> record incomplete")
	check(errors.Is(errC, dump.ErrCRCMismatch), "truncate@126 -> crc mismatch")

	consistent := true
	for n := 1; n < len(data); n++ {
		rec, _, _ := dump.Recover(data[:n])
		if tree.SumSelf(rec.Root) != rec.Samples {
			consistent = false
		}
	}
	check(consistent, "recovered prefix trees self-consistent for all 126 cuts")

	fmt.Printf("SUMMARY %d/%d OK\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}
