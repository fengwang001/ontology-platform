// demo 实际演练 ranking 包的关键语义，每步打印一行 OK/FAIL 判定。
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"

	"ontology/ranking"
)

var failures int

func check(name string, ok bool, detail string) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %s\n", verdict, name, detail)
}

func sp(s string) *string { return &s }

func ranksOf(res ranking.Result) string {
	out := ""
	for i, r := range res.Rows {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("%s=(%d,%d,%d)", r.Row.ID, r.RowNumber, r.Rank, r.DenseRank)
	}
	return out
}

func rows(part string, ids []string, vals []float64) []ranking.Row {
	out := make([]ranking.Row, len(vals))
	for i := range vals {
		out[i] = ranking.Row{Partition: sp(part), Value: vals[i], ID: ids[i]}
	}
	return out
}

func main() {
	// 1. [10,20,20,30] 的三列排名。
	r1 := ranking.Rank(rows("p", []string{"a", "b", "c", "d"}, []float64{10, 20, 20, 30}), ranking.Options{})
	got1 := ranksOf(r1)
	check("classic [10,20,20,30]", got1 == "a=(1,1,1) b=(2,2,2) c=(3,2,2) d=(4,4,3)", got1)

	// 2. 全并列 [5,5,5,5] 的三列排名。
	r2 := ranking.Rank(rows("p", []string{"a", "b", "c", "d"}, []float64{5, 5, 5, 5}), ranking.Options{})
	got2 := ranksOf(r2)
	check("all tied [5,5,5,5]", got2 == "a=(1,1,1) b=(2,1,1) c=(3,1,1) d=(4,1,1)", got2)

	// 3. 打乱输入后结果一致。
	base := rows("p", []string{"a", "b", "c", "d", "e"}, []float64{2, 1, 2, 3, 2})
	want := ranksOf(ranking.Rank(base, ranking.Options{}))
	same := true
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 20; i++ {
		sh := append([]ranking.Row(nil), base...)
		rng.Shuffle(len(sh), func(x, y int) { sh[x], sh[y] = sh[y], sh[x] })
		if ranksOf(ranking.Rank(sh, ranking.Options{})) != want {
			same = false
		}
	}
	check("20 shuffles identical", same, want)

	// 4. 降序与升序对照：降序不是升序的倒置。
	asc := ranking.Rank(base, ranking.Options{})
	desc := ranking.Rank(base, ranking.Options{Descending: true})
	rev := ""
	for i := len(asc.Rows) - 1; i >= 0; i-- {
		rev += asc.Rows[i].Row.ID
	}
	descIDs := ""
	for _, r := range desc.Rows {
		descIDs += r.Row.ID
	}
	check("desc != reversed asc", descIDs != rev, "desc order="+descIDs+" ranks="+ranksOf(desc))

	// 5. 两个分区各自从 1 起。
	two := append(rows("b", []string{"b1", "b2"}, []float64{9, 9}),
		rows("a", []string{"a1", "a2"}, []float64{1, 2})...)
	r5 := ranking.Rank(two, ranking.Options{})
	ok5 := r5.Rows[0].Row.ID == "a1" && r5.Rows[2].Row.ID == "b1" &&
		r5.Rows[2].RowNumber == 1 && r5.Rows[2].Rank == 1 && r5.Rows[2].DenseRank == 1
	check("partitions restart at 1", ok5, ranksOf(r5))

	// 6. nil 分区键与 NaN 跳过计数。
	mixed := append(rows("p", []string{"ok"}, []float64{1}),
		ranking.Row{Partition: nil, Value: 1, ID: "nil"},
		ranking.Row{Partition: sp("p"), Value: math.NaN(), ID: "nan"})
	r6 := ranking.Rank(mixed, ranking.Options{})
	check("nil+NaN skipped", r6.Skipped == 2 && len(r6.Rows) == 1,
		fmt.Sprintf("skipped=%d ranked=%d", r6.Skipped, len(r6.Rows)))

	// 7. 输入未被修改。
	in := rows("p", []string{"x", "y"}, []float64{2, 1})
	before := fmt.Sprintf("%+v", in)
	ranking.Rank(in, ranking.Options{})
	check("input unmodified", fmt.Sprintf("%+v", in) == before, before)

	// 8. 比较次数与上界对比。
	const n = 1000
	big := make([]ranking.Row, n)
	for i := range big {
		big[i] = ranking.Row{Partition: sp("p"), Value: float64(i % 50), ID: fmt.Sprintf("id%05d", i)}
	}
	var count int64
	cmp := ranking.Options{Compare: func(a, b ranking.Row) int {
		count++
		switch {
		case a.Value != b.Value:
			if a.Value < b.Value {
				return -1
			}
			return 1
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	}}
	ranking.Rank(big, cmp)
	bound := int64(0)
	for ceil := 1; ceil < n+1; ceil *= 2 {
		bound += 10 * int64(n)
	}
	check("comparison bound", count <= bound, fmt.Sprintf("n=%d comparisons=%d bound=%d", n, count, bound))

	// 总计。
	if failures > 0 {
		fmt.Printf("TOTAL FAIL: %d check(s) failed\n", failures)
		os.Exit(1)
	}
	fmt.Println("TOTAL OK: all 8 checks passed")
}
