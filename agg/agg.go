package agg

import (
	"sort"
	"sync/atomic"
)

type Event struct {
	Ts   int64
	Dims map[string]string
}

type Matrix struct {
	Rows  []string
	Cols  []string
	Cells [][]int64
	Total int64
}

var examined atomic.Int64

// ExaminedCount 返回自上次重置以来 CrossTab 考察过的事件总数。
func ExaminedCount() int64 { return examined.Load() }

func resetExamined() { examined.Store(0) }

// CrossTab 对已裁剪到查询范围内的事件单趟完成：
// 行/列取值集合收集、下标建立与单元格计数。
// 调用方负责保证 events 全部落在 [from,to) 内，因此这里每事件恰好考察一次。
func CrossTab(events []Event, rowDim, colDim string) Matrix {
	examined.Add(int64(len(events)))

	rowIdx := make(map[string]int)
	colIdx := make(map[string]int)
	var rows, cols []string
	counts := make(map[[2]int]int64)
	var total int64

	for _, ev := range events {
		rv := ev.Dims[rowDim]
		cv := ev.Dims[colDim]
		ri, ok := rowIdx[rv]
		if !ok {
			ri = len(rows)
			rowIdx[rv] = ri
			rows = append(rows, rv)
		}
		ci, ok := colIdx[cv]
		if !ok {
			ci = len(cols)
			colIdx[cv] = ci
			cols = append(cols, cv)
		}
		counts[[2]int{ri, ci}]++
		total++
	}

	sort.Strings(rows)
	sort.Strings(cols)

	newRow := make(map[string]int, len(rows))
	for i, rv := range rows {
		newRow[rv] = i
	}
	newCol := make(map[string]int, len(cols))
	for j, cv := range cols {
		newCol[cv] = j
	}

	oldRowName := make([]string, len(rows))
	for name, oi := range rowIdx {
		oldRowName[oi] = name
	}
	oldColName := make([]string, len(cols))
	for name, oi := range colIdx {
		oldColName[oi] = name
	}

	cells := make([][]int64, len(rows))
	for i := range cells {
		cells[i] = make([]int64, len(cols))
	}
	for key, c := range counts {
		ni := newRow[oldRowName[key[0]]]
		nj := newCol[oldColName[key[1]]]
		cells[ni][nj] = c
	}

	return Matrix{Rows: rows, Cols: cols, Cells: cells, Total: total}
}
