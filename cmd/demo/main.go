// Command demo 实际演练 ranking 包的三种排名语义并逐条打印判定。
// 不读命令行参数、不联网，退出码为 0。
package main

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"

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

// triples 把排名结果格式化为 "(行号,名次,稠密名次)" 序列。
func triples(rows []ranking.RankedRow) string {
	s := ""
	for i, r := range rows {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s=(%d,%d,%d)", r.Row.ID, r.RowNumber, r.Rank, r.DenseRank)
	}
	return s
}

func match(rows []ranking.RankedRow, want [][3]int) bool {
	if len(rows) != len(want) {
		return false
	}
	for i, r := range rows {
		if r.RowNumber != want[i][0] || r.Rank != want[i][1] || r.DenseRank != want[i][2] {
			return false
		}
	}
	return true
}

func rowsOf(key string, values ...float64) []ranking.Row {
	rows := make([]ranking.Row, len(values))
	for i, v := range values {
		rows[i] = ranking.Row{PartitionKey: ranking.StringPtr(key), SortValue: v, ID: fmt.Sprintf("r%d", i)}
	}
	return rows
}

func main() {
	// 1. [10,20,20,30] 的三列排名。
	r1 := ranking.Rank(rowsOf("p", 10, 20, 20, 30), ranking.Options{})
	want1 := [][3]int{{1, 1, 1}, {2, 2, 2}, {3, 2, 2}, {4, 4, 3}}
	check("[10,20,20,30] 三列", match(r1.Rows, want1), triples(r1.Rows))

	// 2. 全并列 [5,5,5,5] 的三列排名。
	r2 := ranking.Rank(rowsOf("p", 5, 5, 5, 5), ranking.Options{})
	want2 := [][3]int{{1, 1, 1}, {2, 1, 1}, {3, 1, 1}, {4, 1, 1}}
	check("[5,5,5,5] 全并列", match(r2.Rows, want2), triples(r2.Rows))

	// 3. 打乱输入后结果一致。
	base := rowsOf("p", 20, 10, 30, 20, 20)
	ref := ranking.Rank(base, ranking.Options{})
	rng := rand.New(rand.NewSource(1))
	same := true
	for i := 0; i < 20 && same; i++ {
		sh := make([]ranking.Row, len(base))
		copy(sh, base)
		rng.Shuffle(len(sh), func(a, b int) { sh[a], sh[b] = sh[b], sh[a] })
		same = reflect.DeepEqual(ranking.Rank(sh, ranking.Options{}).Rows, ref.Rows)
	}
	check("20 种排列结果一致", same, triples(ref.Rows))

	// 4. 降序与升序对照：降序不是升序的倒置。
	asc := ranking.Rank(rowsOf("p", 10, 20, 20, 30), ranking.Options{})
	desc := ranking.Rank(rowsOf("p", 10, 20, 20, 30), ranking.Options{Descending: true})
	rev := make([]ranking.RankedRow, len(asc.Rows))
	for i, r := range asc.Rows {
		rev[len(asc.Rows)-1-i] = r
	}
	notReversed := !reflect.DeepEqual(rev, desc.Rows)
	descOK := desc.Rows[0].Row.SortValue == 30 && desc.Rows[3].Row.SortValue == 10 &&
		desc.Rows[1].Row.ID < desc.Rows[2].Row.ID
	check("降序对照", notReversed && descOK, triples(desc.Rows))

	// 5. 两个分区各自从 1 起。
	two := append(rowsOf("b", 7, 7), rowsOf("a", 1, 2)...)
	r5 := ranking.Rank(two, ranking.Options{})
	partsOK := len(r5.Rows) == 4 &&
		*r5.Rows[0].Row.PartitionKey == "a" && *r5.Rows[3].Row.PartitionKey == "b" &&
		r5.Rows[0].RowNumber == 1 && r5.Rows[2].RowNumber == 1
	check("分区各自从 1 起", partsOK, triples(r5.Rows))

	// 6. nil 分区键与 NaN 跳过计数。
	bad := append(rowsOf("p", 1),
		ranking.Row{PartitionKey: nil, SortValue: 2, ID: "nilKey"},
		ranking.Row{PartitionKey: ranking.StringPtr("p"), SortValue: math.NaN(), ID: "nan"})
	r6 := ranking.Rank(bad, ranking.Options{})
	skipOK := r6.SkippedNilPartition == 1 && r6.SkippedNaN == 1 && len(r6.Rows) == 1
	check("nil 键与 NaN 跳过", skipOK,
		fmt.Sprintf("nilKey=%d NaN=%d 参与=%d", r6.SkippedNilPartition, r6.SkippedNaN, len(r6.Rows)))

	// 7. 输入未被修改。
	in := rowsOf("p", 3, 1, 2)
	backup := make([]ranking.Row, len(in))
	copy(backup, in)
	r7 := ranking.Rank(in, ranking.Options{})
	r7.Rows[0].RowNumber = -1 // 污染返回值，验证结果独立分配
	unmutated := reflect.DeepEqual(in, backup) && ranking.Rank(in, ranking.Options{}).Rows[0].RowNumber == 1
	check("输入未被修改", unmutated, "行切片逐字段不变，结果独立分配")

	// 8. 比较次数与上界对比。
	const n = 1000
	big := make([]ranking.Row, n)
	for i := range big {
		big[i] = ranking.Row{PartitionKey: ranking.StringPtr("p"), SortValue: rng.Float64(), ID: fmt.Sprintf("r%05d", i)}
	}
	count := 0
	ranking.Rank(big, ranking.Options{Compare: func(a, b float64) int {
		count++
		switch {
		case a < b:
			return -1
		case a > b:
			return 1
		}
		return 0
	}})
	log2 := 0
	for x := n + 1; x > 0; x >>= 1 {
		log2++
	}
	bound := 10 * n * log2
	check("比较次数上界", count <= bound, fmt.Sprintf("n=%d 比较=%d 上界=%d", n, count, bound))

	fmt.Printf("总计: %d 项检查，%d 项失败\n", 8, failures)
}
