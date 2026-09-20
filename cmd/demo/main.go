// Command demo exercises the ontology equi-joiner end to end and prints
// one OK/FAIL line per check plus a final summary. It takes no arguments,
// uses no network, and exits 0.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"

	"ontology"
)

var passed, total int

func check(name string, ok bool, detail ...any) {
	total++
	mark := "OK  "
	if !ok {
		mark = "FAIL"
	} else {
		passed++
	}
	fmt.Printf("%s %s%s\n", mark, name, fmt.Sprint(detail...))
}

func main() {
	// 1. NULL keys: never match in Inner, kept as unmatched in Left.
	nilLeft := []map[string]any{{"k": nil, "v": "ln"}, {"v": "lm"}}
	nilRight := []map[string]any{{"k": nil, "w": "rn"}}
	ri, _ := ontology.Join(nilLeft, nilRight, []string{"k"}, ontology.Inner)
	rl, _ := ontology.Join(nilLeft, nilRight, []string{"k"}, ontology.Left)
	check("nil keys: Inner 0 rows, Left keeps 2 left rows",
		ri.RowCount() == 0 && rl.RowCount() == 2)

	// 2. The two unmatched counters stay separate.
	left := []map[string]any{{"k": "a"}, {"k": nil}, {"k": "b"}}
	right := []map[string]any{{"k": "b"}}
	rc, _ := ontology.Join(left, right, []string{"k"}, ontology.Left)
	check("unmatched counts: null-key=1 key-unmatched=1",
		rc.LeftNullKeyCount() == 1 && rc.LeftUnmatchedCount() == 1,
		" (got ", rc.LeftNullKeyCount(), ",", rc.LeftUnmatchedCount(), ")")

	// 3. Duplicate keys: 3 left x 2 right on one key expand to 6 rows.
	var l3, r2 []map[string]any
	for i := 0; i < 3; i++ {
		l3 = append(l3, map[string]any{"k": "d", "lv": i})
	}
	for i := 0; i < 2; i++ {
		r2 = append(r2, map[string]any{"k": "d", "rv": i})
	}
	r6, _ := ontology.Join(l3, r2, []string{"k"}, ontology.Inner)
	check("3x2 duplicate key expands to 6 rows",
		r6.RowCount() == 6 && r6.MaxFanout() == 6)

	// 4. Shuffling both inputs leaves the output sequence identical.
	rng := rand.New(rand.NewSource(1))
	var bigL, bigR []map[string]any
	for i := 0; i < 300; i++ {
		bigL = append(bigL, map[string]any{"k": int64(rng.Intn(15)), "lv": i})
	}
	for i := 0; i < 200; i++ {
		bigR = append(bigR, map[string]any{"k": int64(rng.Intn(15)), "rv": i})
	}
	base, _ := ontology.Join(bigL, bigR, []string{"k"}, ontology.Left)
	shuf, _ := ontology.Join(shuffle(bigL, rng), shuffle(bigR, rng), []string{"k"}, ontology.Left)
	check("shuffled inputs give identical output",
		reflect.DeepEqual(base.Rows, shuf.Rows),
		" (", base.RowCount(), " rows)")

	// 5. Key type conflict is a decidable *KeyTypeError.
	_, err := ontology.Join(
		[]map[string]any{{"k": "1"}},
		[]map[string]any{{"k": int64(1)}},
		[]string{"k"}, ontology.Inner)
	var kte *ontology.KeyTypeError
	check("string vs int64 key returns KeyTypeError",
		errors.As(err, &kte) && kte.Key == "k",
		" (", err, ")")

	// 6. int64 equals float64 numerically.
	rn, _ := ontology.Join(
		[]map[string]any{{"k": int64(3)}},
		[]map[string]any{{"k": 3.0}},
		[]string{"k"}, ontology.Inner)
	check("int64(3) matches float64(3.0)", rn.RowCount() == 1)

	// 7. NaN never matches, counts as null-key.
	rnan, _ := ontology.Join(
		[]map[string]any{{"k": math.NaN()}},
		[]map[string]any{{"k": math.NaN()}},
		[]string{"k"}, ontology.Inner)
	check("NaN key never matches, counted as null-key",
		rnan.RowCount() == 0 && rnan.LeftNullKeyCount() == 1)

	// 8. +0.0 equals -0.0.
	rz, _ := ontology.Join(
		[]map[string]any{{"k": 0.0}},
		[]map[string]any{{"k": math.Copysign(0, -1)}},
		[]string{"k"}, ontology.Inner)
	check("+0.0 matches -0.0", rz.RowCount() == 1)

	// 9. Same-name non-key column: left wins, right kept as right.<name>.
	rcol, _ := ontology.Join(
		[]map[string]any{{"k": int64(1), "c": "L"}},
		[]map[string]any{{"k": int64(1), "c": "R"}},
		[]string{"k"}, ontology.Inner)
	row := rcol.Rows[0]
	check("name collision: left kept, right at right.c",
		row["c"] == "L" && row["right.c"] == "R")

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, total)
}

func shuffle(rows []map[string]any, rng *rand.Rand) []map[string]any {
	out := make([]map[string]any, len(rows))
	copy(out, rows)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}
