package reserve

import "ontology"

// Ledger 是预占账：
// 键为（订单，库位），值为未拣数量；同一订单同一库位的多次取货合并为一条。
// byLoc 保存库位上的订单集合，供 ShortPick 枚举与计数。
// 本类型自身不加锁，由 wave.Manager 的全局锁保证并发安全。
type Ledger struct {
	byOrder map[ontology.ID]map[ontology.ID]int64
	byLoc   map[ontology.ID]map[ontology.ID]int64
}

func NewLedger() *Ledger {
	return &Ledger{
		byOrder: map[ontology.ID]map[ontology.ID]int64{},
		byLoc:   map[ontology.ID]map[ontology.ID]int64{},
	}
}

// Get 返回（订单，库位）记录的未拣数量。
func (l *Ledger) Get(order, loc ontology.ID) (int64, bool) {
	q, ok := l.byOrder[order][loc]
	return q, ok
}

// Add 追加预占；同键合并。
func (l *Ledger) Add(order, loc ontology.ID, qty int64) {
	if qty == 0 {
		return
	}
	row, ok := l.byOrder[order]
	if !ok {
		row = map[ontology.ID]int64{}
		l.byOrder[order] = row
	}
	row[loc] += qty
	if row[loc] == 0 {
		delete(row, loc)
		if len(row) == 0 {
			delete(l.byOrder, order)
		}
	}
	col, ok := l.byLoc[loc]
	if !ok {
		col = map[ontology.ID]int64{}
		l.byLoc[loc] = col
	}
	col[order] += qty
	if col[order] == 0 {
		delete(col, order)
		if len(col) == 0 {
			delete(l.byLoc, loc)
		}
	}
}

// Remove 删除整条记录，返回其未拣数量与该库位上被触碰的记录数。
// records 恒为 1（记录存在时）或 0，供 touched 计数使用。
func (l *Ledger) Remove(order, loc ontology.ID) (qty int64, records int) {
	row, ok := l.byOrder[order]
	if !ok {
		return 0, 0
	}
	q, ok := row[loc]
	if !ok {
		return 0, 0
	}
	delete(row, loc)
	if len(row) == 0 {
		delete(l.byOrder, order)
	}
	delete(l.byLoc[loc], order)
	if len(l.byLoc[loc]) == 0 {
		delete(l.byLoc, loc)
	}
	return q, 1
}

// RemoveAllAt 删除某库位上的全部预占记录。
// 返回每个受影响订单被删除的未拣量，以及触碰记录总数。
func (l *Ledger) RemoveAllAt(loc ontology.ID) (map[ontology.ID]int64, int) {
	col := l.byLoc[loc]
	if len(col) == 0 {
		return map[ontology.ID]int64{}, 0
	}
	affected := make(map[ontology.ID]int64, len(col))
	for order, q := range col {
		affected[order] = q
		row := l.byOrder[order]
		delete(row, loc)
		if len(row) == 0 {
			delete(l.byOrder, order)
		}
	}
	touched := len(col)
	delete(l.byLoc, loc)
	return affected, touched
}

// LocsOf 返回某订单当前有预占的库位（无序，调用方按需排序）。
func (l *Ledger) LocsOf(order ontology.ID) map[ontology.ID]int64 {
	return l.byOrder[order]
}

// OrdersAt 返回某库位上的订单集合（无序）。
func (l *Ledger) OrdersAt(loc ontology.ID) map[ontology.ID]int64 {
	return l.byLoc[loc]
}

// CountAt 返回某库位上的预占记录数。
func (l *Ledger) CountAt(loc ontology.ID) int {
	return len(l.byLoc[loc])
}
