package disclose

type Cell struct {
	Count      int64
	Suppressed bool
}

type Table struct {
	Rows          []string
	Cols          []string
	Cells         [][]Cell
	RowTotals     []int64
	ColTotals     []int64
	Total         int64
	Primary       int
	Complementary int
}

// Apply 在真实计数矩阵上执行主抑制与互补抑制不动点。
// 被抑制格在输出中 Count 恒为 0，真实计数不进入任何输出字段。
func Apply(rows, cols []string, cells [][]int64, k int) Table {
	nr, nc := len(rows), len(cols)
	suppressed := make([][]bool, nr)
	for i := range suppressed {
		suppressed[i] = make([]bool, nc)
	}

	primary := 0
	for i := 0; i < nr; i++ {
		for j := 0; j < nc; j++ {
			if c := cells[i][j]; 1 <= c && c < int64(k) {
				suppressed[i][j] = true
				primary++
			}
		}
	}

	complementary := 0
	for {
		added := pass(nr, nc, cells, suppressed, true)
		added += pass(nr, nc, cells, suppressed, false)
		if added == 0 {
			break
		}
		complementary += added
	}

	out := make([][]Cell, nr)
	rowTotals := make([]int64, nr)
	colTotals := make([]int64, nc)
	rowHidden := make([]bool, nr)
	colHidden := make([]bool, nc)
	var total int64

	for i := 0; i < nr; i++ {
		out[i] = make([]Cell, nc)
		suppInRow := 0
		for j := 0; j < nc; j++ {
			rowTotals[i] += cells[i][j]
			colTotals[j] += cells[i][j]
			total += cells[i][j]
			if suppressed[i][j] {
				suppInRow++
				out[i][j] = Cell{Count: 0, Suppressed: true}
				continue
			}
			out[i][j] = Cell{Count: cells[i][j]}
		}
		rowHidden[i] = suppInRow == 1
	}

	for j := 0; j < nc; j++ {
		suppInCol := 0
		for i := 0; i < nr; i++ {
			if suppressed[i][j] {
				suppInCol++
			}
		}
		colHidden[j] = suppInCol == 1
	}

	totalHidden := false
	for i := 0; i < nr; i++ {
		if rowHidden[i] {
			rowTotals[i] = -1
			totalHidden = true
		}
	}
	for j := 0; j < nc; j++ {
		if colHidden[j] {
			colTotals[j] = -1
			totalHidden = true
		}
	}
	if totalHidden {
		total = -1
	}

	return Table{
		Rows:          append([]string(nil), rows...),
		Cols:          append([]string(nil), cols...),
		Cells:         out,
		RowTotals:     rowTotals,
		ColTotals:     colTotals,
		Total:         total,
		Primary:       primary,
		Complementary: complementary,
	}
}

// pass 执行一趟：rowPass 为 true 时逐行处理，否则逐列处理。
// 一趟内新增的抑制立即对后续行/列生效。
func pass(nr, nc int, real [][]int64, suppressed [][]bool, rowPass bool) int {
	lines := nc
	if rowPass {
		lines = nr
	}
	added := 0
	for a := 0; a < lines; a++ {
		cnt := 0
		if rowPass {
			for j := 0; j < nc; j++ {
				if suppressed[a][j] {
					cnt++
				}
			}
		} else {
			for i := 0; i < nr; i++ {
				if suppressed[i][a] {
					cnt++
				}
			}
		}
		if cnt != 1 {
			continue
		}
		if bi, bj, ok := pickCandidate(nr, nc, real, suppressed, a, rowPass); ok {
			suppressed[bi][bj] = true
			added++
		}
	}
	return added
}

// pickCandidate 在某行（列）的未抑制、真实计数 > 0 的格中选最小真实计数；
// 并列时行处理取列下标较小者，列处理取行下标较小者。
func pickCandidate(nr, nc int, real [][]int64, suppressed [][]bool, a int, rowPass bool) (int, int, bool) {
	bestI, bestJ := -1, -1
	var bestVal int64
	found := false
	if rowPass {
		for j := 0; j < nc; j++ {
			c := real[a][j]
			if suppressed[a][j] || c <= 0 {
				continue
			}
			if !found || c < bestVal || (c == bestVal && j < bestJ) {
				found, bestVal, bestI, bestJ = true, c, a, j
			}
		}
	} else {
		for i := 0; i < nr; i++ {
			c := real[i][a]
			if suppressed[i][a] || c <= 0 {
				continue
			}
			if !found || c < bestVal || (c == bestVal && i < bestI) {
				found, bestVal, bestI, bestJ = true, c, i, a
			}
		}
	}
	return bestI, bestJ, found
}
