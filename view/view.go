package view

import (
	"errors"

	"ontology/agg"
	"ontology/disclose"
)

// ErrTooLarge 在行集×列集超过 Cmax 时返回。
var ErrTooLarge = errors.New("result too large")

type Table = disclose.Table
type Cell = disclose.Cell

// Tabulate 对已裁剪到范围内的事件做交叉统计与披露。
// 行列集确定后立即做规模判定（在逻辑上先于计数结果交付）。
func Tabulate(events []agg.Event, rowDim, colDim string, k, cMax int) (Table, error) {
	m := agg.CrossTab(events, rowDim, colDim)
	if len(m.Rows)*len(m.Cols) > cMax {
		return Table{}, ErrTooLarge
	}
	return disclose.Apply(m.Rows, m.Cols, m.Cells, k), nil
}
