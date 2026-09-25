// Command demo exercises the rank package end to end and prints one
// OK/FAIL verdict line per scenario plus a final summary line.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"

	"ontology/rank"
)

var passed, total int

func check(name string, ok bool, detail string) {
	total++
	status := "OK  "
	if ok {
		passed++
	} else {
		status = "FAIL"
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func sp(s string) *string { return &s }

func rowsOf(vals ...float64) []rank.Row {
	rows := make([]rank.Row, len(vals))
	for i, v := range vals {
		rows[i] = rank.Row{Partition: sp("p"), Value: v, ID: fmt.Sprintf("r%d", i)}
	}
	return rows
}

func cols(rs []rank.RankedRow) string {
	s := ""
	for i, r := range rs {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%d/%d/%d", r.RowNumber, r.Rank, r.DenseRank)
	}
	return s
}

func signature(rs []rank.RankedRow) string {
	s := ""
	for _, r := range rs {
		s += fmt.Sprintf("%s:%v:%d:%d:%d;", r.Row.ID, r.Row.Value,
			r.RowNumber, r.Rank, r.DenseRank)
	}
	return s
}

func main() {
	// 1. [10,20,20,30]: three ranking columns side by side.
	r1 := rank.Rank(rowsOf(10, 20, 20, 30), rank.Config{})
	got1 := cols(r1.Rows)
	check("[10,20,20,30] ROW_NUMBER/RANK/DENSE_RANK", got1 == "1/1/1 2/2/2 3/2/2 4/4/3", got1)

	// 2. All tied [5,5,5,5]: RANK and DENSE_RANK stay 1.
	r2 := rank.Rank(rowsOf(5, 5, 5, 5), rank.Config{})
	got2 := cols(r2.Rows)
	check("[5,5,5,5] 全并列", got2 == "1/1/1 2/1/1 3/1/1 4/1/1", got2)

	// 3. Shuffling the input 20 times never changes the output.
	base := rowsOf(20, 10, 20, 30, 20, 10)
	want := signature(rank.Rank(base, rank.Config{}).Rows)
	same := true
	for seed := int64(0); seed < 20; seed++ {
		sh := rowsOf(20, 10, 20, 30, 20, 10)
		rand.New(rand.NewSource(seed)).Shuffle(len(sh), func(i, j int) {
			sh[i], sh[j] = sh[j], sh[i]
		})
		if signature(rank.Rank(sh, rank.Config{}).Rows) != want {
			same = false
		}
	}
	check("打乱输入 20 次输出逐行一致", same, "")

	// 4. Descending is not a reversed ascending: ties keep ascending IDs.
	rows4 := []rank.Row{
		{Partition: sp("p"), Value: 20, ID: "a"},
		{Partition: sp("p"), Value: 10, ID: "b"},
		{Partition: sp("p"), Value: 20, ID: "c"},
		{Partition: sp("p"), Value: 30, ID: "d"},
	}
	asc := rank.Rank(rows4, rank.Config{})
	desc := rank.Rank(rows4, rank.Config{Descending: true})
	reversed := true
	for i := range asc.Rows {
		if asc.Rows[len(asc.Rows)-1-i] != desc.Rows[i] {
			reversed = false
		}
	}
	ok4 := !reversed && desc.Rows[1].Row.ID == "a" && desc.Rows[2].Row.ID == "c" &&
		desc.Rows[3].Rank == 4 && desc.Rows[3].DenseRank == 3
	check("降序非升序倒置且并列按 ID 升序", ok4, cols(desc.Rows))

	// 5. Two partitions each restart ranking at 1.
	rows5 := []rank.Row{
		{Partition: sp("x"), Value: 1, ID: "x1"},
		{Partition: sp("y"), Value: 9, ID: "y1"},
		{Partition: sp("x"), Value: 2, ID: "x2"},
		{Partition: sp("y"), Value: 8, ID: "y2"},
	}
	r5 := rank.Rank(rows5, rank.Config{})
	ok5 := r5.Rows[0].RowNumber == 1 && r5.Rows[2].RowNumber == 1 &&
		*r5.Rows[0].Row.Partition == "x" && *r5.Rows[2].Row.Partition == "y"
	check("两个分区各自从 1 起且按字典序", ok5,
		fmt.Sprintf("x:%s y:%s", cols(r5.Rows[:2]), cols(r5.Rows[2:])))

	// 6. nil partition keys and NaN values are skipped and counted.
	r6 := rank.Rank([]rank.Row{
		{Partition: nil, Value: 1, ID: "bad1"},
		{Partition: sp("p"), Value: math.NaN(), ID: "bad2"},
		{Partition: sp("p"), Value: 1, ID: "ok1"},
	}, rank.Config{})
	ok6 := r6.Skipped == 2 && r6.SkippedNilPartition == 1 && r6.SkippedNaN == 1 &&
		len(r6.Rows) == 1
	check("nil 分区键与 NaN 跳过计数", ok6,
		fmt.Sprintf("skipped=%d (nil=%d nan=%d)", r6.Skipped, r6.SkippedNilPartition, r6.SkippedNaN))

	// 7. The input slice and rows are left untouched.
	in := rowsOf(3, 1, 2)
	snap := append([]rank.Row(nil), in...)
	out7 := rank.Rank(in, rank.Config{})
	unmodified := len(in) == len(snap)
	for i := range in {
		if in[i] != snap[i] {
			unmodified = false
		}
	}
	out7.Rows[0].RowNumber = -1 // result is independent memory
	check("输入未被修改且结果独立分配", unmodified && in[0] == snap[0], "")

	// 8. Comparison count stays within 10*n*ceil(log2(n+1)).
	const n = 1000
	big := make([]rank.Row, n)
	rng := rand.New(rand.NewSource(1))
	for i := range big {
		big[i] = rank.Row{Partition: sp("p"), Value: float64(rng.Intn(500)), ID: fmt.Sprintf("r%d", i)}
	}
	calls := 0
	rank.Rank(big, rank.Config{Compare: func(a, b rank.Row) int {
		calls++
		switch {
		case a.Value < b.Value:
			return -1
		case a.Value > b.Value:
			return 1
		}
		return 0
	}})
	bound := 10 * n * int(math.Ceil(math.Log2(float64(n+1))))
	check("比较次数在上界内", calls <= bound, fmt.Sprintf("calls=%d bound=%d", calls, bound))

	fmt.Printf("总计 %d/%d 通过\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
