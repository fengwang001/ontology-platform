// Command demo exercises the multi-column deduplicator and prints one
// OK/FAIL verdict per property, then a total. It takes no arguments,
// uses no network, and exits 0 when every property holds.
package main

import (
	"fmt"
	"math"
	"os"
	"reflect"

	"ontology/dedup"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	verdict := "FAIL"
	if ok {
		verdict = "OK"
		passed++
	}
	fmt.Printf("%s %s: %s\n", verdict, name, detail)
}

func keysOf(groups []dedup.Group) []string {
	keys := make([]string, len(groups))
	for i, g := range groups {
		keys[i] = g.Key
	}
	return keys
}

func demoKeepModes() {
	mk := func(tag string) map[string]any { return map[string]any{"id": int64(1), "tag": tag} }
	first := dedup.New([]string{"id"}, dedup.KeepFirst)
	last := dedup.New([]string{"id"}, dedup.KeepLast)
	for _, r := range []map[string]any{mk("first"), mk("middle"), mk("last")} {
		first.Add(r)
		last.Add(r)
	}
	f := first.Snapshot()[0].Row["tag"]
	l := last.Snapshot()[0].Row["tag"]
	check("keep-modes", f == "first" && l == "last",
		fmt.Sprintf("KeepFirst kept tag=%v, KeepLast kept tag=%v", f, l))
}

func demoOrderIndependent() {
	rows := []map[string]any{
		{"a": int64(2)}, {"a": int64(1)}, {"a": int64(3)},
	}
	shuffled := []map[string]any{rows[2], rows[0], rows[1]}
	d1 := dedup.New([]string{"a"}, dedup.KeepFirst)
	d2 := dedup.New([]string{"a"}, dedup.KeepFirst)
	for _, r := range rows {
		d1.Add(r)
	}
	for _, r := range shuffled {
		d2.Add(r)
	}
	k1, k2 := keysOf(d1.Snapshot()), keysOf(d2.Snapshot())
	check("order-independent", reflect.DeepEqual(k1, k2),
		fmt.Sprintf("shuffled input yields same sequence %v", k1))
}

func demoNumericEquality() {
	d := dedup.New([]string{"v"}, dedup.KeepFirst)
	d.Add(map[string]any{"v": int64(3)})
	d.Add(map[string]any{"v": float64(3.0)})
	check("int64-float64", d.GroupCount() == 1,
		fmt.Sprintf("int64(3) & float64(3.0): groups=%d", d.GroupCount()))
}

func demoSignedZero() {
	d := dedup.New([]string{"v"}, dedup.KeepFirst)
	d.Add(map[string]any{"v": 0.0})
	d.Add(map[string]any{"v": math.Copysign(0, -1)})
	check("signed-zero", d.GroupCount() == 1,
		fmt.Sprintf("+0.0 & -0.0: groups=%d", d.GroupCount()))
}

func demoNaN() {
	d := dedup.New([]string{"v"}, dedup.KeepFirst)
	d.Add(map[string]any{"v": math.NaN()})
	d.Add(map[string]any{"v": math.NaN()})
	ok := d.GroupCount() == 2 && d.NaNGroupCount() == 2
	check("nan-own-groups", ok,
		fmt.Sprintf("two NaN rows: groups=%d nanGroups=%d", d.GroupCount(), d.NaNGroupCount()))
}

func demoEmptyThreeWay() {
	d := dedup.New([]string{"c"}, dedup.KeepFirst)
	d.Add(map[string]any{"x": 1})   // missing
	d.Add(map[string]any{"c": nil}) // nil
	d.Add(map[string]any{"c": ""})  // empty string
	counts := d.EmptyGroupCounts()
	ok := d.GroupCount() == 3 &&
		counts[dedup.EmptyMissing] == 1 &&
		counts[dedup.EmptyNil] == 1 &&
		counts[dedup.EmptyString] == 1
	check("empty-three-way", ok,
		fmt.Sprintf("missing/nil/empty-string: groups=%d counts=%v", d.GroupCount(), counts))
}

func demoMillionRepeats() {
	d := dedup.New([]string{"id"}, dedup.KeepLast)
	const n = 1_000_000
	for i := 0; i < n; i++ {
		d.Add(map[string]any{"id": int64(7), "payload": i})
	}
	ok := d.GroupCount() == 1 && d.Processed() == n
	check("million-repeats", ok,
		fmt.Sprintf("groups=%d processed=%d", d.GroupCount(), d.Processed()))
}

func main() {
	demoKeepModes()
	demoOrderIndependent()
	demoNumericEquality()
	demoSignedZero()
	demoNaN()
	demoEmptyThreeWay()
	demoMillionRepeats()
	fmt.Printf("TOTAL: %d/%d OK\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
