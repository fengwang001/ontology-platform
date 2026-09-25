package main

import (
	"fmt"
	"math"

	"ontology/change"
	"ontology/agg"
)

type check struct {
	name string
	fn   func() bool
}

func aggChecks() []check {
	return []check{{
		name: "agg 撤回声明：Count/Sum 不需成员，Min/Max/Distinct 需要",
		fn: func() bool {
			no := []agg.Kind{agg.Count, agg.Sum}
			for _, k := range no {
				if agg.NeedsMembers(k, change.Delete) {
					return false
				}
			}
			yes := []agg.Kind{agg.Min, agg.Max, agg.DistinctCount}
			for _, k := range yes {
				if !agg.NeedsMembers(k, change.Delete) {
					return false
				}
			}
			m := agg.New(agg.Min)
			m.Insert(1)
			m.Insert(2)
			return m.Delete(1) == false
		},
	}}
}

func changeChecks() []check {
	return []check{{
		name: "change 编解码往返、缺失键与 NaN 被拒",
		fn: func() bool {
			src := change.Change{Version: 7, Op: change.Update, Key: "g", Value: 1.5, OldKey: "h", OldValue: -0.0}
			got, n, err := change.Decode(src.Encode(nil))
			if err != nil || n != src.EncodedLen() || got != src {
				return false
			}
			if (change.Change{Op: change.Insert, Key: "g", Value: 1}).Valid() != nil {
				return false
			}
			return change.Change{Op: change.Insert, Key: "g", Value: math.NaN()}.Valid() == change.ErrNaN
		},
	}}
}

func main() {
	checks := append(changeChecks(), aggChecks()...)
	pass := 0
	for _, c := range checks {
		if c.fn() {
			pass++
			fmt.Println("OK  " + c.name)
		} else {
			fmt.Println("FAIL " + c.name)
		}
	}
	fmt.Printf("总计 %d/%d\n", pass, len(checks))
	if pass != len(checks) {
		panic("demo checks failed")
	}
}
