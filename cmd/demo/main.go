// Command demo runs a self-contained walkthrough of the aggregate package.
// Run with: go run ./cmd/demo
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"

	"ontology/aggregate"
)

var fails int

func report(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func sumBits(rows []map[string]any) map[string]uint64 {
	agg := aggregate.NewAggregator([]string{"g"}, "v")
	for _, r := range rows {
		agg.Add(r)
	}
	res, err := agg.Snapshot()
	if err != nil {
		panic(err)
	}
	bits := make(map[string]uint64)
	for _, r := range res {
		s, _ := r.Sum()
		bits[r.Key.Columns[0].Value.(string)] = math.Float64bits(s)
	}
	return bits
}

func findKind(res []aggregate.Result, kind aggregate.MissingKind) *aggregate.Result {
	for i := range res {
		if res[i].Key.Columns[0].Kind == kind {
			return &res[i]
		}
	}
	return nil
}

func main() {
	// 1. Two shuffles of the same rows produce bit-identical sums.
	base := []map[string]any{
		{"g": "a", "v": 1e16}, {"g": "a", "v": 1.0}, {"g": "a", "v": -1e16},
		{"g": "a", "v": 0.1}, {"g": "a", "v": 0.2}, {"g": "b", "v": 3.25},
		{"g": "b", "v": -0.25}, {"g": "b", "v": 100.0},
	}
	rng := rand.New(rand.NewPCG(7, 9))
	s1 := append([]map[string]any(nil), base...)
	s2 := append([]map[string]any(nil), base...)
	rng.Shuffle(len(s1), func(i, j int) { s1[i], s1[j] = s1[j], s1[i] })
	rng.Shuffle(len(s2), func(i, j int) { s2[i], s2[j] = s2[j], s2[i] })
	equal := fmt.Sprint(sumBits(s1)) == fmt.Sprint(sumBits(s2))
	report("shuffled-sums bit-identical", equal, fmt.Sprintf("bits=%v", sumBits(base)))

	// 2. Absent / nil / empty-string are three distinct groups.
	agg := aggregate.NewAggregator([]string{"k"}, "v")
	agg.Add(map[string]any{"v": 1})
	agg.Add(map[string]any{"k": nil, "v": 1})
	agg.Add(map[string]any{"k": "", "v": 1})
	res, _ := agg.Snapshot()
	distinct := findKind(res, aggregate.Absent) != nil &&
		findKind(res, aggregate.Null) != nil &&
		findKind(res, aggregate.EmptyString) != nil && len(res) == 3
	report("absent/nil/empty are distinct groups", distinct,
		fmt.Sprintf("groups=%d", len(res)))

	// 3. Multi-column key with one missing column is still routed.
	magg := aggregate.NewAggregator([]string{"a", "b"}, "v")
	magg.Add(map[string]any{"a": "x", "b": "y", "v": 1})
	magg.Add(map[string]any{"b": "y", "v": 1}) // a absent
	magg.Add(map[string]any{"a": "x", "v": 1}) // b absent
	mres, _ := magg.Snapshot()
	routed := len(mres) == 3
	report("multi-column missing routing", routed,
		fmt.Sprintf("groups=%d (present, a=<absent>, b=<absent>)", len(mres)))

	// 4. All-int64 overflow is a typed error naming the group.
	oagg := aggregate.NewAggregator([]string{"k"}, "v")
	oagg.Add(map[string]any{"k": "big", "v": int64(math.MaxInt64)})
	oagg.Add(map[string]any{"k": "big", "v": int64(1)})
	_, oerr := oagg.Snapshot()
	var oe *aggregate.OverflowError
	named := errors.As(oerr, &oe) && len(oe.Groups) == 1 &&
		oe.Groups[0].Columns[0].Value == "big"
	report("int64 overflow named", named, fmt.Sprintf("err=%v", oerr))

	// 5. Unsummable rows count toward Count and Skipped, not Sum.
	uagg := aggregate.NewAggregator([]string{"k"}, "v")
	uagg.Add(map[string]any{"k": "g", "v": int64(3)})
	uagg.Add(map[string]any{"k": "g", "v": "str"})
	uagg.Add(map[string]any{"k": "g", "v": true})
	uagg.Add(map[string]any{"k": "g"})
	uagg.Add(map[string]any{"k": "g", "v": int64(4)})
	ures, _ := uagg.Snapshot()
	ur := ures[0]
	goodSkip := ur.Count == 5 && ur.Skipped == 3 && ur.IntSum == 7
	report("unsummable: count+skip, sum intact", goodSkip,
		fmt.Sprintf("Count=%d Skipped=%d Sum=%d", ur.Count, ur.Skipped, ur.IntSum))

	// 6. NaN is skipped; the rest of the group still sums.
	nagg := aggregate.NewAggregator([]string{"k"}, "v")
	nagg.Add(map[string]any{"k": "g", "v": 3.0})
	nagg.Add(map[string]any{"k": "g", "v": math.NaN()})
	nagg.Add(map[string]any{"k": "g", "v": 4.0})
	nres, _ := nagg.Snapshot()
	nr := nres[0]
	nanoK := nr.Count == 3 && nr.Skipped == 1 && nr.FloatSum == 7.0
	report("NaN skipped without poisoning sum", nanoK,
		fmt.Sprintf("Count=%d Skipped=%d Sum=%v", nr.Count, nr.Skipped, nr.FloatSum))

	// 7. Output order is repeatable across repeated aggregations.
	var first []aggregate.Result
	repeated := true
	for i := 0; i < 3; i++ {
		ragg := aggregate.NewAggregator([]string{"g"}, "v")
		for _, r := range base {
			ragg.Add(r)
		}
		cur, _ := ragg.Snapshot()
		if first == nil {
			first = cur
		} else if len(cur) != len(first) ||
			cur[0].Key.Columns[0].Value != first[0].Key.Columns[0].Value ||
			cur[1].Key.Columns[0].Value != first[1].Key.Columns[0].Value {
			repeated = false
		}
	}
	report("output order repeatable", repeated,
		fmt.Sprintf("order=[%s,%s]",
			first[0].Key.Columns[0].Value, first[1].Key.Columns[0].Value))

	if fails > 0 {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
		os.Exit(1)
	}
	fmt.Println("TOTAL: all 7 checks passed")
}
