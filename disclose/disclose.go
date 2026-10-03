// Package disclose 对交叉计数表执行小格主抑制、互补抑制与合计隐藏。
package disclose

// Cell 是输出单元格；被抑制格的 Count 恒为 0。
type Cell struct {
	Count      int
	Suppressed bool
}

// Table 是抑制后的交叉表；被隐藏的合计一律记 -1。
type Table struct {
	Rows          []string
	Cols          []string
	Cells         [][]Cell
	RowTotals     []int
	ColTotals     []int
	Total         int
	Primary       int // 主抑制格数
	Complementary int // 互补抑制格数
}

// Suppress 对 counts（行×列的真实计数）按阈值 k 执行抑制，输出确定性表格。
func Suppress(rows, cols []string, counts [][]int, k int) Table {
	nr, nc := len(rows), len(cols)
	sup := make([][]bool, nr)
	for i := range sup {
		sup[i] = make([]bool, nc)
	}
	t := Table{Rows: rows, Cols: cols}
	// 主抑制：1 ≤ c < k；零格永不抑制。
	for i := 0; i < nr; i++ {
		for j := 0; j < nc; j++ {
			if c := counts[i][j]; 1 <= c && c < k {
				sup[i][j] = true
				t.Primary++
			}
		}
	}
	// 互补抑制：行趟再列趟，新增立即生效，整轮无新增则收敛。
	for {
		added := false
		for i := 0; i < nr; i++ {
			if countSuppressedRow(sup[i]) == 1 {
				if j, ok := pickInRow(counts[i], sup[i]); ok {
					sup[i][j] = true
					added = true
				}
			}
		}
		for j := 0; j < nc; j++ {
			if countSuppressedCol(sup, j) == 1 {
				if i, ok := pickInCol(counts, sup, j); ok {
					sup[i][j] = true
					added = true
				}
			}
		}
		if !added {
			break
		}
	}
	totalSup := 0
	t.Cells = make([][]Cell, nr)
	for i := 0; i < nr; i++ {
		t.Cells[i] = make([]Cell, nc)
		for j := 0; j < nc; j++ {
			if sup[i][j] {
				t.Cells[i][j] = Cell{Count: 0, Suppressed: true}
				totalSup++
			} else {
				t.Cells[i][j] = Cell{Count: counts[i][j]}
			}
		}
	}
	t.Complementary = totalSup - t.Primary
	// 合计：恰有 1 个被抑制格的行/列合计隐藏（-1）；任一隐藏则总计隐藏。
	t.RowTotals = make([]int, nr)
	rowHidden := false
	for i := 0; i < nr; i++ {
		if countSuppressedRow(sup[i]) == 1 {
			t.RowTotals[i] = -1
			rowHidden = true
		} else {
			t.RowTotals[i] = sumRow(counts[i])
		}
	}
	t.ColTotals = make([]int, nc)
	colHidden := false
	for j := 0; j < nc; j++ {
		if countSuppressedCol(sup, j) == 1 {
			t.ColTotals[j] = -1
			colHidden = true
		} else {
			t.ColTotals[j] = sumCol(counts, j)
		}
	}
	if rowHidden || colHidden {
		t.Total = -1
	} else {
		for _, s := range t.RowTotals {
			t.Total += s
		}
	}
	return t
}

func countSuppressedRow(row []bool) int {
	n := 0
	for _, s := range row {
		if s {
			n++
		}
	}
	return n
}

func countSuppressedCol(sup [][]bool, j int) int {
	n := 0
	for i := range sup {
		if sup[i][j] {
			n++
		}
	}
	return n
}

// pickInRow 在行内未抑制且真实计数 > 0 的格中选计数最小者，并列取列下标较小者。
func pickInRow(counts []int, sup []bool) (int, bool) {
	best, ok := -1, false
	for j, c := range counts {
		if sup[j] || c <= 0 {
			continue
		}
		if !ok || c < counts[best] {
			best, ok = j, true
		}
	}
	return best, ok
}

// pickInCol 在列内未抑制且真实计数 > 0 的格中选计数最小者，并列取行下标较小者。
func pickInCol(counts [][]int, sup [][]bool, j int) (int, bool) {
	best, ok := -1, false
	for i := range counts {
		if sup[i][j] || counts[i][j] <= 0 {
			continue
		}
		if !ok || counts[i][j] < counts[best][j] {
			best, ok = i, true
		}
	}
	return best, ok
}

func sumRow(row []int) int {
	s := 0
	for _, c := range row {
		s += c
	}
	return s
}

func sumCol(counts [][]int, j int) int {
	s := 0
	for i := range counts {
		s += counts[i][j]
	}
	return s
}
