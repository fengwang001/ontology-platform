package main

import (
	"fmt"
	"math"
	"reflect"

	"ontology"
)

var pass, fail int

func verdict(ok bool, format string, args ...any) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		fail++
	} else {
		pass++
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

func p(s string) *string { return &s }

func rows(vals []float64) []ontology.Row {
	out := make([]ontology.Row, len(vals))
	for i, v := range vals {
		out[i] = ontology.Row{Partition: p("p"), SortValue: v, ID: fmt.Sprintf("id%03d", i)}
	}
	return out
}

func line(r ontology.RankedRow) string {
	return fmt.Sprintf("(%g,%s rn=%d rank=%d dense=%d)",
		r.SortValue, r.ID, r.RowNumber, r.Rank, r.DenseRank)
}

func main() {
	// 1. [10,20,20,30]: the three semantics side by side.
	a := ontology.Rank(rows([]float64{10, 20, 20, 30}), ontology.Asc).Rows
	wantA := [][3]int{{1, 1, 1}, {2, 2, 2}, {3, 2, 2}, {4, 4, 3}}
	ok := len(a) == 4
	for i := range wantA {
		ok = ok && [3]int{a[i].RowNumber, a[i].Rank, a[i].DenseRank} == wantA[i]
	}
	verdict(ok, "[10,20,20,30] asc: %s | %s | %s | %s",
		line(a[0]), line(a[1]), line(a[2]), line(a[3]))

	// 2. All ties: ROW_NUMBER still enumerates, ranks stay 1.
	t := ontology.Rank(rows([]float64{5, 5, 5, 5}), ontology.Asc).Rows
	okT := true
	for i, r := range t {
		okT = okT && r.RowNumber == i+1 && r.Rank == 1 && r.DenseRank == 1
	}
	verdict(okT, "[5,5,5,5] all ties: rn=1,2,3,4 rank/dense all 1: %s,%s,%s,%s",
		t[0].ID, t[1].ID, t[2].ID, t[3].ID)

	// 3. Shuffled input yields identical output (20 permutations).
	base := rows([]float64{10, 20, 20, 30, 20, 10, 30})
	ref := ontology.Rank(base, ontology.Asc).Rows
	okS := true
	seed := uint64(0x12345)
	for k := 0; k < 20; k++ {
		perm := append([]ontology.Row(nil), base...)
		for i := len(perm) - 1; i > 0; i-- {
			seed = seed*6364136223846793005 + 1
			j := int((seed >> 33) % uint64(i+1))
			perm[i], perm[j] = perm[j], perm[i]
		}
		okS = okS && reflect.DeepEqual(ontology.Rank(perm, ontology.Asc).Rows, ref)
	}
	verdict(okS, "20 shuffled inputs produce identical rows (first tie id=%s)", ref[2].ID)

	// 4. Desc is not asc reversed: tie IDs stay ascending inside desc.
	d := ontology.Rank(rows([]float64{10, 20, 20, 30}), ontology.Desc).Rows
	okD := d[0].SortValue == 30 && d[3].SortValue == 10 &&
		d[1].ID == "id001" && d[2].ID == "id002"
	verdict(okD, "desc values 30,20,20,10 with tie ids asc %s<%s; asc tie ids were %s<%s",
		d[1].ID, d[2].ID, a[1].ID, a[2].ID)

	// 5. Two partitions each start at 1, output sorted by key.
	pa, pb := p("a"), p("b")
	multi := ontology.Rank([]ontology.Row{
		{Partition: pb, SortValue: 9, ID: "b9"},
		{Partition: pa, SortValue: 3, ID: "a3"},
		{Partition: pa, SortValue: 1, ID: "a1"},
	}, ontology.Asc).Rows
	okM := multi[0].Partition == "a" && multi[0].ID == "a1" && multi[0].RowNumber == 1 &&
		multi[2].Partition == "b" && multi[2].RowNumber == 1
	verdict(okM, "partitions a then b, first rn per partition = 1: %s then %s",
		multi[0].ID, multi[2].ID)

	// 6. Nil partition key and NaN rows are skipped with counters.
	res := ontology.Rank([]ontology.Row{
		{Partition: nil, SortValue: 1, ID: "nil"},
		{Partition: p("p"), SortValue: math.NaN(), ID: "nan"},
		{Partition: p("p"), SortValue: 2, ID: "ok"},
	}, ontology.Asc)
	verdict(res.Stats.SkippedNilPartition == 1 && res.Stats.SkippedNaN == 1 &&
		len(res.Rows) == 1, "skip counters: nil=%d nan=%d accepted=%d",
		res.Stats.SkippedNilPartition, res.Stats.SkippedNaN, len(res.Rows))

	// 7. Input untouched after the call.
	in := []ontology.Row{{Partition: p("p"), SortValue: 20, ID: "z"},
		{Partition: p("p"), SortValue: 10, ID: "y"}}
	snap := append([]ontology.Row(nil), in...)
	ontology.Rank(in, ontology.Desc)
	verdict(reflect.DeepEqual(in, snap), "input slice unchanged after Rank: %+v", in)

	// 8. Comparator budget vs O(n log n) bound.
	n := 1000
	big := make([]ontology.Row, n)
	for i := range big {
		big[i] = ontology.Row{Partition: p("p"), SortValue: float64(i), ID: fmt.Sprintf("%04d", i)}
	}
	// Shuffle first so the comparator cannot exploit an already-sorted run.
	rs := uint64(0xabcdef)
	for i := n - 1; i > 0; i-- {
		rs = rs*6364136223846793005 + 1
		j := int((rs >> 33) % uint64(i+1))
		big[i], big[j] = big[j], big[i]
	}
	c := ontology.Comparisons(big, ontology.Asc)
	bound := ontology.ComparisonBound(n)
	verdict(c <= bound, "n=%d comparisons=%d <= bound 10*n*ceil(log2(n+1))=%d", n, c, bound)

	if fail == 0 {
		fmt.Printf("TOTAL: %d/%d checks passed\n", pass, pass+fail)
	} else {
		fmt.Printf("TOTAL: %d passed, %d failed\n", pass, fail)
	}
}
