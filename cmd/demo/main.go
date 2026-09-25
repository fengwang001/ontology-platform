package main

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"

	"ontology"
)

var failed int

func report(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func ptr(s string) *string { return &s }

func rows(values ...float64) []ontology.Row {
	out := make([]ontology.Row, len(values))
	for i, v := range values {
		out[i] = ontology.Row{Partition: ptr("p"), Value: v, ID: string(rune('a' + i))}
	}
	return out
}

func cols(res ontology.RankResult) (rn, rank, dense []int) {
	for _, r := range res.Rows {
		rn = append(rn, r.RowNumber)
		rank = append(rank, r.Rank)
		dense = append(dense, r.DenseRank)
	}
	return
}

func ids(res ontology.RankResult) []string {
	out := make([]string, len(res.Rows))
	for i, r := range res.Rows {
		out[i] = r.ID
	}
	return out
}

func main() {
	// 1. [10,20,20,30]: the three tie policies side by side.
	r1 := ontology.Rank(rows(10, 20, 20, 30), ontology.Ascending)
	rn, rk, dr := cols(r1)
	ok1 := reflect.DeepEqual(rn, []int{1, 2, 3, 4}) &&
		reflect.DeepEqual(rk, []int{1, 2, 2, 4}) &&
		reflect.DeepEqual(dr, []int{1, 2, 2, 3})
	report("[10,20,20,30] three columns", ok1,
		fmt.Sprintf("ROW_NUMBER=%v RANK=%v DENSE_RANK=%v", rn, rk, dr))

	// 2. All tied: row numbers advance, both ranks stay at 1.
	r2 := ontology.Rank(rows(5, 5, 5, 5), ontology.Ascending)
	rn, rk, dr = cols(r2)
	ok2 := reflect.DeepEqual(rn, []int{1, 2, 3, 4}) &&
		reflect.DeepEqual(rk, []int{1, 1, 1, 1}) &&
		reflect.DeepEqual(dr, []int{1, 1, 1, 1})
	report("[5,5,5,5] all tied", ok2,
		fmt.Sprintf("ROW_NUMBER=%v RANK=%v DENSE_RANK=%v", rn, rk, dr))

	// 3. Shuffling input must not change a single output row.
	base := rows(10, 20, 20, 30, 20, 10, 30, 5)
	want := ontology.Rank(base, ontology.Ascending)
	shuf := make([]ontology.Row, len(base))
	copy(shuf, base)
	rand.New(rand.NewSource(7)).Shuffle(len(shuf), func(i, j int) {
		shuf[i], shuf[j] = shuf[j], shuf[i]
	})
	report("shuffled input gives identical output",
		reflect.DeepEqual(ontology.Rank(shuf, ontology.Ascending).Rows, want.Rows), "")

	// 4. Descending reverses values only; ties still break by ascending ID.
	a := ontology.Rank(rows(10, 20, 20, 30), ontology.Ascending)
	d := ontology.Rank(rows(10, 20, 20, 30), ontology.Descending)
	dids := ids(d)
	report("desc != plain reversal of asc (tie IDs stay ascending)",
		reflect.DeepEqual(dids, []string{"d", "b", "c", "a"}),
		fmt.Sprintf("asc=%v desc=%v", ids(a), dids))

	// 5. Partitions are isolated (each starts at 1) and ordered by key.
	parts := []ontology.Row{
		{Partition: ptr("y"), Value: 7, ID: "y1"},
		{Partition: ptr("x"), Value: 7, ID: "x1"},
		{Partition: ptr("y"), Value: 7, ID: "y2"},
		{Partition: ptr("x"), Value: 7, ID: "x2"},
	}
	rp := ontology.Rank(parts, ontology.Ascending)
	ok5 := rp.Rows[0].Partition == "x" && rp.Rows[0].Rank == 1 &&
		rp.Rows[2].Partition == "y" && rp.Rows[2].Rank == 1
	report("two partitions, lexicographic, each RANK starts at 1", ok5,
		fmt.Sprintf("order=%s,%s,%s,%s", rp.Rows[0].ID, rp.Rows[1].ID,
			rp.Rows[2].ID, rp.Rows[3].ID))

	// 6. Nil partition key and NaN value are rejected with a readable count.
	bad := []ontology.Row{
		{Partition: ptr("p"), Value: 1, ID: "ok"},
		{Partition: nil, Value: 1, ID: "nil-key"},
		{Partition: ptr("p"), Value: math.NaN(), ID: "nan"},
	}
	rb := ontology.Rank(bad, ontology.Ascending)
	report("nil partition key + NaN skipped", rb.Skipped == 2 && len(rb.Rows) == 1,
		fmt.Sprintf("Skipped=%d ranked=%d", rb.Skipped, len(rb.Rows)))

	// 7. Input slice and every Row remain field-for-field unchanged.
	before := make([]ontology.Row, len(parts))
	copy(before, parts)
	ontology.Rank(parts, ontology.Ascending)
	report("input untouched after Rank", reflect.DeepEqual(parts, before), "")

	// 8. Comparison count stays within 10*n*ceil(log2(n+1)).
	const n = 256
	big := make([]ontology.Row, n)
	for i := range big {
		big[i] = ontology.Row{Partition: ptr("p"), Value: float64(n - i),
			ID: fmt.Sprintf("id%04d", i)}
	}
	_, comparisons := ontology.RankWithCount(big, ontology.Ascending)
	bound := 10 * n * int(math.Ceil(math.Log2(float64(n+1))))
	report(fmt.Sprintf("comparisons O(n log n): %d <= bound %d", comparisons, bound),
		0 < comparisons && comparisons <= bound, "")

	if failed == 0 {
		fmt.Println("TOTAL: 8/8 checks OK")
	} else {
		fmt.Printf("TOTAL: %d/8 checks OK\n", 8-failed)
	}
}
