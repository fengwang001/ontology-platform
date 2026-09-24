package main

import (
	"fmt"

	"ontology/agg"
	"ontology/change"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK", name)
		} else {
			fmt.Println("FAIL", name)
			fails++
		}
	}

	// change package
	b, err := change.Encode(change.Change{Version: 1, Op: change.Insert, RecID: "r1", Group: change.G(""), Value: 0})
	roundTrip := err == nil
	if roundTrip {
		c2, derr := change.Decode(b)
		roundTrip = derr == nil && c2.RecID == "r1" && c2.Version == 1
	}
	check("change encode/decode (empty group key legal)", roundTrip)

	// agg package: withdraw policy matrix
	type row struct {
		k                    agg.Kind
		canAdd, canDel, mems bool
	}
	aggOK := true
	for _, r := range []row{
		{agg.Count, true, true, false},
		{agg.Sum, true, true, false},
		{agg.Min, true, false, true},
		{agg.Max, true, false, true},
		{agg.DistinctCount, true, false, true},
	} {
		a := agg.New(r.k)
		a.Add(1)
		need := a.Remove(1)
		if (need == false) != r.canDel || a.NeedsMembersOnDelete() != r.mems {
			aggOK = false
		}
	}
	_ = agg.SameValue
	check("agg withdraw policy (Count/Sum direct; Min/Max/Distinct need members)", aggOK)

	if fails == 0 {
		fmt.Println("TOTAL: all checks passed")
	} else {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
	}
}
