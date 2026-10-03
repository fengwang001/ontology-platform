package disclose

import (
	"reflect"
	"testing"
	"testing/quick"
)

// naiveSuppression 是按题目规则逐步直写的独立朴素实现，用于与 Apply 对照。
func naiveSuppression(real [][]int64, k int) (supp [][]bool, primary, complementary int) {
	nr, nc := len(real), len(real[0])
	supp = make([][]bool, nr)
	for i := range supp {
		supp[i] = make([]bool, nc)
	}
	for i := 0; i < nr; i++ {
		for j := 0; j < nc; j++ {
			if real[i][j] >= 1 && real[i][j] < int64(k) {
				supp[i][j] = true
				primary++
			}
		}
	}
	for {
		added := 0
		for i := 0; i < nr; i++ {
			cnt, only := 0, -1
			for j := 0; j < nc; j++ {
				if supp[i][j] {
					cnt++
					only = j
				}
			}
			if cnt != 1 {
				continue
			}
			bestJ, bestC := -1, int64(0)
			for j := 0; j < nc; j++ {
				if j == only || supp[i][j] || real[i][j] == 0 {
					continue
				}
				if bestJ == -1 || real[i][j] < bestC {
					bestJ, bestC = j, real[i][j]
				}
			}
			if bestJ >= 0 {
				supp[i][bestJ] = true
				added++
			}
		}
		for j := 0; j < nc; j++ {
			cnt, only := 0, -1
			for i := 0; i < nr; i++ {
				if supp[i][j] {
					cnt++
					only = i
				}
			}
			if cnt != 1 {
				continue
			}
			bestI, bestC := -1, int64(0)
			for i := 0; i < nr; i++ {
				if i == only || supp[i][j] || real[i][j] == 0 {
					continue
				}
				if bestI == -1 || real[i][j] < bestC {
					bestI, bestC = i, real[i][j]
				}
			}
			if bestI >= 0 {
				supp[bestI][j] = true
				added++
			}
		}
		if added == 0 {
			break
		}
		complementary += added
	}
	return supp, primary, complementary
}

func naiveTable(rows, cols []string, real [][]int64, k int) Table {
	supp, p, c := naiveSuppression(real, k)
	nr, nc := len(rows), len(cols)
	out := make([][]Cell, nr)
	rt := make([]int64, nr)
	ct := make([]int64, nc)
	rHidden := make([]bool, nr)
	cHidden := make([]bool, nc)
	var total int64
	for i := 0; i < nr; i++ {
		out[i] = make([]Cell, nc)
		n := 0
		for j := 0; j < nc; j++ {
			rt[i] += real[i][j]
			ct[j] += real[i][j]
			total += real[i][j]
			if supp[i][j] {
				n++
				out[i][j] = Cell{Suppressed: true}
			} else {
				out[i][j] = Cell{Count: real[i][j]}
			}
		}
		rHidden[i] = n == 1
	}
	for j := 0; j < nc; j++ {
		n := 0
		for i := 0; i < nr; i++ {
			if supp[i][j] {
				n++
			}
		}
		cHidden[j] = n == 1
	}
	hidden := false
	for i := 0; i < nr; i++ {
		if rHidden[i] {
			rt[i], hidden = -1, true
		}
	}
	for j := 0; j < nc; j++ {
		if cHidden[j] {
			ct[j], hidden = -1, true
		}
	}
	if hidden {
		total = -1
	}
	return Table{rows, cols, out, rt, ct, total, p, c}
}

func TestSpecExample1(t *testing.T) {
	rows := []string{"r1", "r2", "r3"}
	cols := []string{"c1", "c2", "c3"}
	real := [][]int64{{9, 1, 8}, {7, 6, 9}, {8, 9, 2}}
	got := Apply(rows, cols, real, 3)
	want := naiveTable(rows, cols, real, 3)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mismatch\n got=%+v\nwant=%+v", got, want)
	}
	if got.Primary != 2 || got.Complementary != 4 {
		t.Fatalf("counts primary=%d comp=%d", got.Primary, got.Complementary)
	}
	if got.Total != 59 {
		t.Fatalf("total=%d", got.Total)
	}
	t.Logf("example1 out=%+v rowTotals=%v colTotals=%v", got, got.RowTotals, got.ColTotals)
}

func TestSpecExample2(t *testing.T) {
	rows := []string{"r1", "r2"}
	cols := []string{"c1", "c2"}
	real := [][]int64{{1, 5}, {0, 4}}
	got := Apply(rows, cols, real, 3)
	if got.Primary != 1 || got.Complementary != 2 {
		t.Fatalf("counts p=%d c=%d", got.Primary, got.Complementary)
	}
	if !got.Cells[1][0].Suppressed && got.Cells[1][0].Count != 0 {
		t.Fatalf("r2c1 should be visible zero: %+v", got.Cells[1][0])
	}
	if got.RowTotals[0] != 6 || got.RowTotals[1] != -1 {
		t.Fatalf("row totals=%v", got.RowTotals)
	}
	if got.ColTotals[0] != -1 || got.ColTotals[1] != 9 {
		t.Fatalf("col totals=%v", got.ColTotals)
	}
	if got.Total != -1 {
		t.Fatalf("total=%d", got.Total)
	}
	t.Logf("example2 out=%+v", got)
}

func TestThresholdsAndZero(t *testing.T) {
	rows := []string{"r1", "r2"}
	cols := []string{"a", "b", "c"}
	// k=4：r1a=3 主抑制；r1b=4 恰等 k 不抑制；r1c=0 永不抑制且不被互补选中。
	// r2 行保证各列趟中“被抑制列”不恰好为 1（除 a 列外由 b/c 结构稳定），
	// 从而只验证主抑制规则本身：行 r1 恰 1 个时候选为 b=4，c=0 不被选中。
	real := [][]int64{{3, 4, 0}, {1, 9, 9}}
	got := Apply(rows, cols, real, 4)
	if !got.Cells[0][0].Suppressed || got.Cells[0][2].Suppressed {
		t.Fatalf("threshold wrong: %+v", got.Cells[0])
	}
	if got.Primary != 2 {
		t.Fatalf("primary=%d", got.Primary)
	}
	// c=0 格在主抑制与所有互补趟中永不被选中。
	if got.Cells[0][2].Count != 0 {
		t.Fatalf("visible zero must report count 0: %+v", got.Cells[0][2])
	}
	t.Logf("threshold table=%+v", got)
}

func TestTieAndRandomAgainstNaive(t *testing.T) {
	// 并列：同一行两个相等的最小正计数，取列下标较小者。
	rows := []string{"r1", "r2"}
	cols := []string{"c1", "c2", "c3"}
	real := [][]int64{{1, 5, 5}, {5, 5, 5}}
	got := Apply(rows, cols, real, 3)
	if !got.Cells[0][1].Suppressed || got.Cells[0][2].Suppressed {
		t.Fatalf("tie should pick smaller column index: %+v", got.Cells[0])
	}

	f := func(vals [4]int64, kv [1]uint8) bool {
		k := int(kv[0]%5) + 1
		mod := func(x int64) int64 {
			x %= 9
			if x < 0 {
				x = -x
			}
			return x
		}
		m := [][]int64{{mod(vals[0]), mod(vals[1])}, {mod(vals[2]), mod(vals[3])}}
		rs := []string{"r1", "r2"}
		cs := []string{"c1", "c2"}
		return reflect.DeepEqual(Apply(rs, cs, m, k), naiveTable(rs, cs, m, k))
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatal(err)
	}
}
