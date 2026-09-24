// Command demo runs self-checks for the incremental aggregation view maintainer.
// It takes no arguments and performs no network access.
package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/agg"
	"ontology/change"
)

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Println("OK  " + name)
		} else {
			fmt.Println("FAIL " + name)
		}
	}

	check("skeleton runs", true)
	check("change codec + validation", func() bool {
		cs := []change.Change{
			change.Insert(1, "", 3),
			change.Delete(2, "g", math.Copysign(0, -1)),
			change.Update(3, "n", 1, "o", 2),
		}
		for _, c := range cs {
			b, err := change.MarshalPayload(c)
			if err != nil {
				return false
			}
			r, err := change.UnmarshalPayload(b)
			if err != nil || r != c {
				return false
			}
		}
		missing := change.Change{Op: change.OpInsert, Value: 1}
		nan := change.Insert(1, "g", math.NaN())
		return errors.Is(missing.Validate(), change.ErrMissingGroup) &&
			errors.Is(nan.Validate(), change.ErrNaNValue)
	}())
	check("agg retraction policy", func() bool {
		expect := map[agg.Kind]bool{agg.Count: false, agg.Sum: false,
			agg.Min: true, agg.Max: true, agg.DistinctCount: true}
		for k, want := range expect {
			if agg.New(k).NeedsMembers() != want {
				return false
			}
		}
		mn := agg.New(agg.Min)
		for _, v := range []float64{5, 1, 9, 1} {
			mn.Add(v)
		}
		mn.Remove(1)
		if !mn.NeedsRecompute(1) {
			return false
		}
		mn.Recompute(func(yield func(float64) bool) {
			for _, v := range []float64{5, 9, 1} {
				if !yield(v) {
					return
				}
			}
		})
		dc := agg.New(agg.DistinctCount)
		dc.Add(2)
		dc.Add(2)
		dc.Remove(2) // still held once
		if dc.Value() != 1 || !dc.NeedsRecompute(2) {
			return false
		}
		dc.Remove(2)
		return mn.Value() == 1 && dc.Value() == 0
	}())

	fmt.Printf("TOTAL %d/%d OK\n", pass, total)
	if pass != total {
		fmt.Println("FAIL overall")
	}
}
