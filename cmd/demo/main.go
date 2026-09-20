// Command demo exercises the ontology equi-join connector end to end and
// prints one OK/FAIL verdict per check, then a final tally. It takes no
// arguments, uses no network, and always exits 0.
package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"

	"ontology"
)

var checks, passed int

func check(name string, ok bool, detail string) {
	checks++
	status := "FAIL"
	if ok {
		status = "OK"
		passed++
	}
	fmt.Printf("%s %s: %s\n", status, name, detail)
}

// snapshot renders result rows deterministically for sequence comparison.
func snapshot(rows []ontology.Row) string {
	var b strings.Builder
	for _, r := range rows {
		names := make([]string, 0, len(r))
		for name := range r {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "%s=%v;", name, r[name])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func shuffled(rows []ontology.Row, seed int64) []ontology.Row {
	out := make([]ontology.Row, len(rows))
	copy(out, rows)
	rand.New(rand.NewSource(seed)).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

func main() {
	keys := []string{"id"}

	// 1. NULL keys never match in Inner, but survive in Left.
	left := []ontology.Row{{"id": nil, "tag": "nil"}, {"tag": "missing"}}
	right := []ontology.Row{{"id": nil}, {"id": 1}}
	inner, _, _ := ontology.Join(left, right, keys, ontology.Inner)
	lout, lst, _ := ontology.Join(left, right, keys, ontology.Left)
	check("null-key", len(inner) == 0 && len(lout) == 2 && lst.LeftNullKey == 2,
		fmt.Sprintf("inner rows=%d, left-mode keeps %d null-key rows", len(inner), lst.LeftNullKey))

	// 2. The two unmatched categories are counted separately.
	_, st, _ := ontology.Join(
		[]ontology.Row{{"id": nil}, {"id": 7}},
		[]ontology.Row{{"id": 1}}, keys, ontology.Left)
	check("unmatched-counts", st.LeftNullKey == 1 && st.LeftNoPartner == 1,
		fmt.Sprintf("null-key=%d no-partner=%d", st.LeftNullKey, st.LeftNoPartner))

	// 3. A 3x2 duplicate key expands to exactly 6 rows.
	fan, fst, _ := ontology.Join(
		[]ontology.Row{{"id": 1, "l": 0}, {"id": 1, "l": 1}, {"id": 1, "l": 2}},
		[]ontology.Row{{"id": 1, "r": 0}, {"id": 1, "r": 1}}, keys, ontology.Inner)
	check("fanout-3x2", len(fan) == 6 && fst.MaxFanOut == 6,
		fmt.Sprintf("rows=%d max-fanout=%d", len(fan), fst.MaxFanOut))

	// 4. Shuffling both inputs leaves the result sequence identical.
	var bigL, bigR []ontology.Row
	for i := 0; i < 30; i++ {
		bigL = append(bigL, ontology.Row{"id": i % 5, "li": i})
		bigR = append(bigR, ontology.Row{"id": i % 5, "rj": i})
	}
	base, _, _ := ontology.Join(bigL, bigR, keys, ontology.Inner)
	reshuffled, _, _ := ontology.Join(shuffled(bigL, 7), shuffled(bigR, 9), keys, ontology.Inner)
	check("shuffle-stable", snapshot(base) == snapshot(reshuffled),
		fmt.Sprintf("%d rows identical after shuffling both tables", len(base)))

	// 5. Conflicting key types are a decidable error.
	_, _, err := ontology.Join(
		[]ontology.Row{{"id": "abc"}}, []ontology.Row{{"id": int64(1)}}, keys, ontology.Inner)
	var ktErr *ontology.KeyTypeError
	check("type-conflict", errors.As(err, &ktErr), fmt.Sprintf("err=%v", err))

	// 6. Numeric edge semantics: int64~float64, NaN, signed zero.
	num, _, _ := ontology.Join(
		[]ontology.Row{{"id": int64(5)}}, []ontology.Row{{"id": 5.0}}, keys, ontology.Inner)
	check("int64-eq-float64", len(num) == 1, "int64(5) matches float64(5.0)")
	nan, nst, _ := ontology.Join(
		[]ontology.Row{{"id": math.NaN()}}, []ontology.Row{{"id": math.NaN()}}, keys, ontology.Inner)
	check("nan-never-equal", len(nan) == 0 && nst.LeftNullKey == 1,
		fmt.Sprintf("rows=%d counted as null-key=%d", len(nan), nst.LeftNullKey))
	zero, _, _ := ontology.Join(
		[]ontology.Row{{"id": 0.0}}, []ontology.Row{{"id": math.Copysign(0, -1)}}, keys, ontology.Inner)
	check("signed-zero-equal", len(zero) == 1, "+0.0 matches -0.0")

	// 7. Same-named non-key attributes: the left value is never overwritten.
	merge, _, _ := ontology.Join(
		[]ontology.Row{{"id": 1, "v": "L"}}, []ontology.Row{{"id": 1, "v": "R"}}, keys, ontology.Inner)
	row := merge[0]
	check("collision-rename", row["v"] == "L" && row["right.v"] == "R",
		fmt.Sprintf("v=%v right.v=%v", row["v"], row["right.v"]))

	fmt.Printf("TOTAL %d/%d OK\n", passed, checks)
}
