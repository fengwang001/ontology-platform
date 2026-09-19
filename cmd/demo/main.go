// Command demo exercises the ontology group aggregator end to end and
// prints one OK/FAIL verdict line per check plus a final summary.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"

	"ontology"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("OK   %s: %s\n", name, detail)
	} else {
		failed++
		fmt.Printf("FAIL %s: %s\n", name, detail)
	}
}

func aggregate(rows []map[string]any, keys ...string) []ontology.GroupResult {
	agg := ontology.NewAggregator(keys, "v")
	for _, row := range rows {
		agg.Add(row)
	}
	res, _ := agg.Snapshot()
	return res
}

func shuffled(rows []map[string]any, seed int64) []map[string]any {
	out := make([]map[string]any, len(rows))
	copy(out, rows)
	rand.New(rand.NewSource(seed)).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

func main() {
	// 1. Shuffled input yields bit-identical sums per group.
	rows := []map[string]any{
		{"g": "a", "v": 1e16}, {"g": "a", "v": 1.0}, {"g": "a", "v": -1e16},
		{"g": "a", "v": 0.1}, {"g": "b", "v": int64(7)}, {"g": "b", "v": 0.3},
	}
	r1 := aggregate(shuffled(rows, 1), "g")
	r2 := aggregate(shuffled(rows, 2), "g")
	bitEqual := len(r1) == len(r2)
	for i := range r1 {
		if math.Float64bits(r1[i].Sum()) != math.Float64bits(r2[i].Sum()) {
			bitEqual = false
		}
	}
	check("shuffle-bit-equal", bitEqual,
		fmt.Sprintf("group a sum bits=%x", math.Float64bits(r1[0].Sum())))

	// 2. Absent vs nil vs empty string form three distinct groups.
	missing := aggregate([]map[string]any{
		{"v": int64(1)}, {"g": nil, "v": int64(2)}, {"g": "", "v": int64(3)},
	}, "g")
	kinds := map[ontology.PartKind]bool{}
	for _, r := range missing {
		kinds[r.Key[0].Kind] = true
	}
	check("missing-three-groups", len(missing) == 3 &&
		kinds[ontology.PartAbsent] && kinds[ontology.PartNil] &&
		kinds[ontology.PartEmpty],
		fmt.Sprintf("order: %s %s %s", ontology.KeyString(missing[0].Key),
			ontology.KeyString(missing[1].Key), ontology.KeyString(missing[2].Key)))

	// 3. Multi-column key: one missing column routes, never drops.
	multi := aggregate([]map[string]any{
		{"a": "x", "b": "y", "v": int64(1)},
		{"b": "y", "v": int64(2)},
		{"a": "x", "v": int64(3)},
	}, "a", "b")
	var total int64
	for _, r := range multi {
		total += r.Count
	}
	check("multi-column-missing", len(multi) == 3 && total == 3,
		fmt.Sprintf("groups: %s %s %s", ontology.KeyString(multi[0].Key),
			ontology.KeyString(multi[1].Key), ontology.KeyString(multi[2].Key)))

	// 4. All-int64 overflow is a decidable error naming the group.
	agg := ontology.NewAggregator([]string{"g"}, "v")
	agg.Add(map[string]any{"g": "big", "v": int64(math.MaxInt64)})
	agg.Add(map[string]any{"g": "big", "v": int64(1)})
	_, err := agg.Snapshot()
	var ovf *ontology.OverflowError
	namesGroup := errors.As(err, &ovf) && len(ovf.Groups) == 1 &&
		ontology.KeyString(ovf.Groups[0]) == "(big)"
	check("int64-overflow", namesGroup, fmt.Sprintf("err: %v", err))

	// 5. Non-summable rows count toward Count and Skipped only.
	skipped := aggregate([]map[string]any{
		{"g": "a", "v": int64(10)}, {"g": "a", "v": "bad"},
		{"g": "a"}, {"g": "a", "v": true},
	}, "g")
	check("skip-non-summable", len(skipped) == 1 && skipped[0].Count == 4 &&
		skipped[0].Skipped == 3 && skipped[0].Sum() == 10,
		fmt.Sprintf("count=%d skipped=%d sum=%v",
			skipped[0].Count, skipped[0].Skipped, skipped[0].Sum()))

	// 6. NaN is skipped; the rest of the group still sums.
	nan := aggregate([]map[string]any{
		{"g": "a", "v": 1.5}, {"g": "a", "v": math.NaN()}, {"g": "a", "v": 2.5},
	}, "g")
	check("nan-skipped", len(nan) == 1 && nan[0].Sum() == 4.0 &&
		nan[0].Skipped == 1 && nan[0].Count == 3,
		fmt.Sprintf("sum=%v skipped=%d", nan[0].Sum(), nan[0].Skipped))

	// 7. Output sequence is repeatable across repeated aggregations.
	again := aggregate(shuffled(rows, 99), "g")
	repeatable := reflect.DeepEqual(r1, again)
	check("output-repeatable", repeatable,
		fmt.Sprintf("first group=%s last group=%s",
			ontology.KeyString(r1[0].Key), ontology.KeyString(r1[len(r1)-1].Key)))

	fmt.Printf("TOTAL %d passed, %d failed\n", passed, failed)
}
