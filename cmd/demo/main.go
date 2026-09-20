// Command demo exercises the dedup package end to end and prints one
// OK/FAIL line per property plus a final summary. It takes no
// arguments, never touches the network, and always exits 0.
package main

import (
	"fmt"
	"math"

	"ontology/dedup"
)

var passed, failed int

func check(name string, ok bool, detail string) {
	if ok {
		passed++
		fmt.Printf("OK   %s %s\n", name, detail)
		return
	}
	failed++
	fmt.Printf("FAIL %s %s\n", name, detail)
}

func keySeq(rows []map[string]any) string {
	s := ""
	for _, r := range rows {
		s += fmt.Sprintf("%v;", r["id"])
	}
	return s
}

func main() {
	// 1. Keep-first vs keep-last keep different non-dedup columns.
	first, last := dedup.New([]string{"id"}, dedup.KeepFirst), dedup.New([]string{"id"}, dedup.KeepLast)
	for _, r := range []map[string]any{
		{"id": int64(1), "payload": "alpha"},
		{"id": int64(1), "payload": "omega"},
	} {
		first.Add(r)
		last.Add(r)
	}
	fp, lp := first.Snapshot()[0]["payload"], last.Snapshot()[0]["payload"]
	check("keep-first/last differ:", fp != lp,
		fmt.Sprintf("(first kept %q, last kept %q)", fp, lp))

	// 2. Shuffled arrival order yields the same output sequence.
	rows := []map[string]any{
		{"id": int64(3)}, {"id": int64(1)}, {"id": int64(2)},
		{"id": int64(1)}, {"id": int64(3)},
	}
	a, b := dedup.New([]string{"id"}, dedup.KeepFirst), dedup.New([]string{"id"}, dedup.KeepFirst)
	for _, i := range []int{0, 1, 2, 3, 4} {
		a.Add(rows[i])
	}
	for _, i := range []int{4, 2, 0, 3, 1} {
		b.Add(rows[i])
	}
	ka, kb := keySeq(a.Snapshot()), keySeq(b.Snapshot())
	check("order independent:", ka == kb, fmt.Sprintf("(sequence %s)", ka))

	// 3. int64(3) and float64(3.0) share a group.
	num := dedup.New([]string{"v"}, dedup.KeepFirst)
	num.Add(map[string]any{"v": int64(3)})
	num.Add(map[string]any{"v": float64(3.0)})
	check("int64(3)==float64(3.0):", num.Groups() == 1,
		fmt.Sprintf("(groups=%d processed=%d)", num.Groups(), num.Processed()))

	// 4. +0.0 and -0.0 share a group.
	zero := dedup.New([]string{"v"}, dedup.KeepFirst)
	zero.Add(map[string]any{"v": 0.0})
	zero.Add(map[string]any{"v": math.Copysign(0, -1)})
	check("+0.0==-0.0:", zero.Groups() == 1,
		fmt.Sprintf("(groups=%d processed=%d)", zero.Groups(), zero.Processed()))

	// 5. Two NaN rows form two groups; NaN group count is 2.
	nan := dedup.New([]string{"v"}, dedup.KeepFirst)
	nan.Add(map[string]any{"v": math.NaN(), "tag": "x"})
	nan.Add(map[string]any{"v": math.NaN(), "tag": "y"})
	check("NaN rows never merge:", nan.Groups() == 2 && nan.NaNGroups() == 2,
		fmt.Sprintf("(groups=%d nanGroups=%d)", nan.Groups(), nan.NaNGroups()))

	// 6. Missing, nil and empty string land in three distinct groups.
	nul := dedup.New([]string{"a"}, dedup.KeepFirst)
	nul.Add(map[string]any{"other": 1})
	nul.Add(map[string]any{"a": nil})
	nul.Add(map[string]any{"a": ""})
	ok := nul.Groups() == 3 && nul.MissingRows() == 1 && nul.NilRows() == 1 && nul.EmptyStringRows() == 1
	check("missing/nil/empty split:", ok,
		fmt.Sprintf("(groups=%d missing=%d nil=%d empty=%d)",
			nul.Groups(), nul.MissingRows(), nul.NilRows(), nul.EmptyStringRows()))

	// 7. A million duplicates still form exactly one group.
	mill := dedup.New([]string{"id"}, dedup.KeepLast)
	for i := 0; i < 1_000_000; i++ {
		mill.Add(map[string]any{"id": int64(42), "payload": i})
	}
	check("1M repeats stay one group:", mill.Groups() == 1 && mill.Processed() == 1_000_000,
		fmt.Sprintf("(groups=%d processed=%d)", mill.Groups(), mill.Processed()))

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, passed+failed)
}
