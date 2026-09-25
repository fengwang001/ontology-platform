// Command demo exercises the ontology ranking functions and prints one
// OK/FAIL verdict line per check, followed by a total line.
//
// Usage: go run ./cmd/demo
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"reflect"

	"ontology"
)

type triplet struct {
	id        string
	rowNumber int
	rank      int
	denseRank int
}

func triples(res ontology.Result) []triplet {
	out := make([]triplet, len(res.Rows))
	for i, r := range res.Rows {
		out[i] = triplet{r.Row.ID, r.RowNumber, r.Rank, r.DenseRank}
	}
	return out
}

func valueRows(values []float64) []ontology.Row {
	rows := make([]ontology.Row, len(values))
	for i, v := range values {
		id := fmt.Sprintf("id%d", i+1)
		rows[i] = ontology.Row{Partition: sp("p"), Value: v, ID: id}
	}
	return rows
}

func main() {
	passed, total := 0, 0
	check := func(name string, ok bool, detail string) {
		total++
		status := "OK"
		if ok {
			passed++
		} else {
			status = "FAIL"
		}
		fmt.Printf("%s %s: %s\n", status, name, detail)
	}

	// 1. [10,20,20,30]: the three tie policies side by side.
	got1 := triples(ontology.Rank(valueRows([]float64{10, 20, 20, 30}), ontology.Options{}))
	want1 := []triplet{{"id1", 1, 1, 1}, {"id2", 2, 2, 2},
		{"id3", 3, 2, 2}, {"id4", 4, 4, 3}}
	check("10,20,20,30 three columns", reflect.DeepEqual(got1, want1),
		fmt.Sprintf("ROW_NUMBER=%v RANK=%v DENSE_RANK=%v",
			[]int{1, 2, 3, 4}, []int{1, 2, 2, 4}, []int{1, 2, 2, 3}))

	// 2. [5,5,5,5]: ROW_NUMBER increments, RANK/DENSE_RANK stay at 1.
	got2 := triples(ontology.Rank(valueRows([]float64{5, 5, 5, 5}), ontology.Options{}))
	want2 := []triplet{{"id1", 1, 1, 1}, {"id2", 2, 1, 1},
		{"id3", 3, 1, 1}, {"id4", 4, 1, 1}}
	check("5,5,5,5 all tied", reflect.DeepEqual(got2, want2),
		"ROW_NUMBER=[1 2 3 4] RANK=[1 1 1 1] DENSE_RANK=[1 1 1 1]")

	// 3. Shuffle 30 times; every output must equal the reference.
	base := []ontology.Row{
		{Partition: sp("p"), Value: 20, ID: "id9"},
		{Partition: sp("p"), Value: 10, ID: "id2"},
		{Partition: sp("p"), Value: 20, ID: "id5"},
		{Partition: sp("p"), Value: 30, ID: "id7"},
		{Partition: sp("p"), Value: 20, ID: "id1"},
	}
	reference := ontology.Rank(base, ontology.Options{}).Rows
	stable := true
	for seed := int64(1); seed <= 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		shuffled := make([]ontology.Row, len(base))
		copy(shuffled, base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		if !reflect.DeepEqual(ontology.Rank(shuffled, ontology.Options{}).Rows, reference) {
			stable = false
		}
	}
	check("30 shuffles identical output", stable,
		fmt.Sprintf("ids=%s,%s,%s,%s,%s", reference[0].Row.ID, reference[1].Row.ID,
			reference[2].Row.ID, reference[3].Row.ID, reference[4].Row.ID))

	// 4. Descending is not a simple reversal of ascending (tie IDs asc).
	dRows := []ontology.Row{
		{Partition: sp("p"), Value: 1, ID: "b"},
		{Partition: sp("p"), Value: 2, ID: "d"},
		{Partition: sp("p"), Value: 2, ID: "a"},
		{Partition: sp("p"), Value: 3, ID: "c"},
	}
	asc := ontology.Rank(dRows, ontology.Options{})
	desc := ontology.Rank(dRows, ontology.Options{Descending: true})
	reversed := reverse(asc.Rows)
	descOK := !reflect.DeepEqual(desc.Rows, reversed) &&
		triples(desc)[1].id == "a" && triples(desc)[2].id == "d"
	check("descending != reversed ascending", descOK,
		fmt.Sprintf("asc IDs=[b a d c], desc IDs=[c %s %s b]",
			desc.Rows[1].Row.ID, desc.Rows[2].Row.ID))

	// 5. Two partitions each start numbering at 1, emitted key-sorted.
	partRows := []ontology.Row{
		{Partition: sp("b"), Value: 9, ID: "b1"},
		{Partition: sp("a"), Value: 8, ID: "a1"},
		{Partition: sp("b"), Value: 9, ID: "b0"},
	}
	pres := ontology.Rank(partRows, ontology.Options{})
	partOK := pres.Rows[0].RowNumber == 1 && *pres.Rows[0].Row.Partition == "a" &&
		pres.Rows[1].RowNumber == 1 && *pres.Rows[1].Row.Partition == "b" &&
		pres.Rows[2].RowNumber == 2
	check("two partitions restart at 1", partOK,
		"order=[a:rn1 b:rn1 b:rn2], tie inside b by ID (b0 before b1)")

	// 6. nil Partition and NaN Value are rejected with readable counts.
	skipRows := []ontology.Row{
		{Partition: nil, Value: 1, ID: "n1"},
		{Partition: sp("p"), Value: math.NaN(), ID: "nan1"},
		{Partition: nil, Value: 2, ID: "n2"},
		{Partition: sp("p"), Value: 1, ID: "ok"},
	}
	sres := ontology.Rank(skipRows, ontology.Options{})
	skipOK := sres.SkippedNilPartition == 2 && sres.SkippedNaN == 1 &&
		sres.Skipped() == 3 && len(sres.Rows) == 1
	check("nil-partition and NaN skipped", skipOK,
		fmt.Sprintf("nil=%d nan=%d total=%d ranked=%d",
			sres.SkippedNilPartition, sres.SkippedNaN, sres.Skipped(), len(sres.Rows)))

	// 7. Input is not modified by the call.
	before := []ontology.Row{
		{Partition: sp("q"), Value: 2, ID: "q2"},
		{Partition: sp("p"), Value: 1, ID: "p1"},
	}
	snapshot := append([]ontology.Row(nil), before...)
	_ = ontology.Rank(before, ontology.Options{})
	check("input unchanged after Rank", reflect.DeepEqual(before, snapshot),
		"slice order, values, IDs and partition pointers all identical")

	// 8. Comparison count is within O(n log n): 10*n*ceil(log2(n+1)).
	const n = 1000
	rng := rand.New(rand.NewSource(42))
	cmpRows := make([]ontology.Row, n)
	count := 0
	for i := range cmpRows {
		cmpRows[i] = ontology.Row{Partition: sp("p"),
			Value: float64(rng.Intn(n/2 + 1)), ID: fmt.Sprintf("id%04d", i)}
	}
	counter := func(a, b float64) int {
		count++
		return ontology.CompareFloat(a, b)
	}
	ores := ontology.Rank(cmpRows, ontology.Options{Compare: counter})
	bound := ontology.ComparisonBound(n)
	check("comparison count bound", count <= bound && len(ores.Rows) == n,
		fmt.Sprintf("n=%d comparisons=%d bound=%d (O(n log n))", n, count, bound))

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}

func sp(s string) *string { return &s }

func reverse(in []ontology.RankedRow) []ontology.RankedRow {
	out := make([]ontology.RankedRow, len(in))
	for i := range in {
		out[i] = in[len(in)-1-i]
	}
	return out
}
