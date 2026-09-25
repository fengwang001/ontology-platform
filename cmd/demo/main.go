// Command demo exercises the rank package end to end and prints one
// OK/FAIL verdict line per check plus a final summary. It takes no
// arguments, uses no network, and always exits 0.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"

	"ontology/rank"
)

var passed, failed int

func check(ok bool, label string, detail string) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failed++
	} else {
		passed++
	}
	fmt.Printf("%s %s: %s\n", verdict, label, detail)
}

func sp(s string) *string { return &s }

func rowsOf(part string, values ...float64) []rank.Row {
	rows := make([]rank.Row, len(values))
	for i, v := range values {
		rows[i] = rank.Row{Partition: sp(part), Value: v, ID: int64(i + 1)}
	}
	return rows
}

func triples(res rank.Result) [][3]int {
	out := make([][3]int, len(res.Rows))
	for i, r := range res.Rows {
		out[i] = [3]int{r.RowNumber, r.Rank, r.DenseRank}
	}
	return out
}

func main() {
	mixed := rank.Compute(rowsOf("p", 10, 20, 20, 30), rank.Options{})
	check(reflect.DeepEqual(triples(mixed), [][3]int{{1, 1, 1}, {2, 2, 2}, {3, 2, 2}, {4, 4, 3}}),
		"[10,20,20,30]", fmt.Sprintf("(rn,rank,dense)=%v", triples(mixed)))

	tied := rank.Compute(rowsOf("p", 5, 5, 5, 5), rank.Options{})
	check(reflect.DeepEqual(triples(tied), [][3]int{{1, 1, 1}, {2, 1, 1}, {3, 1, 1}, {4, 1, 1}}),
		"[5,5,5,5] all tied", fmt.Sprintf("(rn,rank,dense)=%v", triples(tied)))

	base := rowsOf("p", 20, 10, 20, 30, 20, 10)
	want := rank.Compute(base, rank.Options{})
	same := true
	for seed := int64(1); seed <= 20; seed++ {
		perm := make([]rank.Row, len(base))
		copy(perm, base)
		rand.New(rand.NewSource(seed)).Shuffle(len(perm), func(i, j int) {
			perm[i], perm[j] = perm[j], perm[i]
		})
		if !reflect.DeepEqual(rank.Compute(perm, rank.Options{}), want) {
			same = false
		}
	}
	check(same, "20 shuffles", "output identical incl. ROW_NUMBER")

	asc := rank.Compute(rowsOf("p", 10, 20, 20, 30), rank.Options{})
	desc := rank.Compute(rowsOf("p", 10, 20, 20, 30), rank.Options{Desc: true})
	descIDs, revIDs := "", ""
	for i, r := range desc.Rows {
		if i > 0 {
			descIDs += ","
			revIDs += ","
		}
		descIDs += fmt.Sprintf("%d", r.Row.ID)
		revIDs += fmt.Sprintf("%d", asc.Rows[len(asc.Rows)-1-i].Row.ID)
	}
	check(descIDs != revIDs, "desc vs asc",
		fmt.Sprintf("desc id order [%s] != reversed asc [%s]", descIDs, revIDs))

	two := rank.Compute([]rank.Row{
		{Partition: sp("b"), Value: 1, ID: 1},
		{Partition: sp("a"), Value: 2, ID: 2},
		{Partition: sp("b"), Value: 2, ID: 3},
		{Partition: sp("a"), Value: 1, ID: 4},
	}, rank.Options{})
	twoOK := two.Rows[0].RowNumber == 1 && two.Rows[1].RowNumber == 2 &&
		two.Rows[2].RowNumber == 1 && two.Rows[3].RowNumber == 2 &&
		*two.Rows[0].Row.Partition == "a" && *two.Rows[2].Row.Partition == "b"
	check(twoOK, "two partitions", "each restarts at 1, keys ordered a<b")

	skipped := rank.Compute([]rank.Row{
		{Partition: nil, Value: 1, ID: 1},
		{Partition: nil, Value: 2, ID: 2},
		{Partition: sp("p"), Value: math.NaN(), ID: 3},
		{Partition: sp("p"), Value: 1, ID: 4},
	}, rank.Options{})
	check(skipped.SkippedNilPartition == 2 && skipped.SkippedNaN == 1 && len(skipped.Rows) == 1,
		"skipped rows", fmt.Sprintf("nil-partition=%d nan=%d ranked=%d",
			skipped.SkippedNilPartition, skipped.SkippedNaN, len(skipped.Rows)))

	input := rowsOf("p", 3, 1, 2, 1)
	snapshot := make([]rank.Row, len(input))
	copy(snapshot, input)
	rank.Compute(input, rank.Options{})
	check(reflect.DeepEqual(input, snapshot), "input unchanged", "slice and rows intact")

	const n = 5000
	big := make([]rank.Row, n)
	rng := rand.New(rand.NewSource(42))
	for i := range big {
		big[i] = rank.Row{Partition: sp("p"), Value: float64(rng.Intn(2500)), ID: int64(i)}
	}
	compares := 0
	rank.Compute(big, rank.Options{OnCompare: func() { compares++ }})
	bound := 10 * n * 13 // 10*n*ceil(log2(n+1)), ceil(log2(5001))=13
	check(compares <= bound, "comparison bound",
		fmt.Sprintf("n=%d compares=%d <= bound=%d", n, compares, bound))

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, passed+failed)
}
