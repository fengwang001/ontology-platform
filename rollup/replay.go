package rollup

import "fmt"

// ReplayState 为无状态的日志重放器：下游按序应用变更日志，
// 得到与上游完全一致的三层结果。
type ReplayState struct {
	detail   map[GroupKey]*groupState
	subtotal map[GroupKey]*groupState
	grand    groupState
}

// NewReplay 创建空的重放状态。
func NewReplay() *ReplayState {
	return &ReplayState{
		detail:   make(map[GroupKey]*groupState),
		subtotal: make(map[GroupKey]*groupState),
	}
}

// ApplyEntry 按序应用一条日志。
func (r *ReplayState) ApplyEntry(e LogEntry) {
	for i := range e.Changes {
		c := e.Changes[i]
		var m map[GroupKey]*groupState
		switch c.Layer {
		case LayerDetail:
			m = r.detail
		case LayerSubtotal:
			m = r.subtotal
		case LayerGrand:
			r.grand.count += c.CountDelta
			r.grand.sum += c.SumDelta
			continue
		default:
			continue
		}
		g := m[c.Key]
		if g == nil {
			g = &groupState{}
			m[c.Key] = g
		}
		g.count += c.CountDelta
		g.sum += c.SumDelta
		if g.count == 0 {
			delete(m, c.Key)
		}
	}
}

// DetailGroups 返回重放得到的明细组快照。
func (r *ReplayState) DetailGroups() []GroupView {
	return snapshotGroups(r.detail)
}

// Subtotals 返回重放得到的小计快照。
func (r *ReplayState) Subtotals() []GroupView {
	return snapshotGroups(r.subtotal)
}

// Total 返回重放得到的总计。
func (r *ReplayState) Total() Stats {
	return Stats{Count: r.grand.count, Sum: r.grand.sum}
}

// BatchRecompute 对一组行直接做三层批量重算。
func BatchRecompute(rows []Row) *ReplayState {
	r := NewReplay()
	for _, row := range rows {
		d1v, hasD1 := keyDim(row.Dim1)
		d2v, hasD2 := keyDim(row.Dim2)
		dk := GroupKey{Layer: LayerDetail, Dim1: d1v, HasDim1: hasD1, Dim2: d2v, HasDim2: hasD2}
		sk := GroupKey{Layer: LayerSubtotal, Dim1: d1v, HasDim1: hasD1}
		addReplay(r.detail, dk, row.Value)
		addReplay(r.subtotal, sk, row.Value)
		r.grand.count++
		r.grand.sum += row.Value
	}
	return r
}

func addReplay(m map[GroupKey]*groupState, k GroupKey, value int64) {
	g := m[k]
	if g == nil {
		g = &groupState{}
		m[k] = g
	}
	g.count++
	g.sum += value
}

// checkInvariant 校验任意日志前缀重放结果的三层恒等式。
func (r *ReplayState) checkInvariant() error {
	var detailCount, detailSum int64
	subAgg := make(map[GroupKey]groupState, len(r.subtotal))
	for k, g := range r.detail {
		if g.count <= 0 {
			return fmt.Errorf("detail group %v has non-positive count %d", k, g.count)
		}
		detailCount += g.count
		detailSum += g.sum
		sk := GroupKey{Layer: LayerSubtotal, Dim1: k.Dim1, HasDim1: k.HasDim1}
		agg := subAgg[sk]
		agg.count += g.count
		agg.sum += g.sum
		subAgg[sk] = agg
	}
	if detailCount != r.grand.count || detailSum != r.grand.sum {
		return fmt.Errorf("detail total {count:%d sum:%d} != grand {count:%d sum:%d}",
			detailCount, detailSum, r.grand.count, r.grand.sum)
	}
	for k, g := range r.subtotal {
		agg, ok := subAgg[k]
		if !ok || agg != *g {
			return fmt.Errorf("subtotal %v {count:%d sum:%d} != sum of detail groups", k, g.count, g.sum)
		}
	}
	if len(subAgg) != len(r.subtotal) {
		return fmt.Errorf("subtotal has %d groups but detail aggregates to %d",
			len(r.subtotal), len(subAgg))
	}
	return nil
}
