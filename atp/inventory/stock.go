package inventory

import "ontology/atp/clock"

// Inbound 为一条计划入库记录。
type Inbound struct {
	ID      string
	Arrival clock.Time
	Qty     int64
}

// stockState 为某仓库某商品的库存状态。
//
// 不变量：
//   - onHand >= 0；
//   - activeReserved <= onHand + 已到货未确认入库之和（即可承诺量非负）。
type stockState struct {
	warehouse *Warehouse
	onHand    int64
	// inbounds 为尚未确认到货的计划入库。
	inbounds []*Inbound
	// reservations 为按到期时刻的最小堆，含墓碑；activeReserved
	// 只统计 state==resvActive 且未到期（未被判过期）的记录。
	reservations   resvHeap
	activeReserved int64
}

// inboundUpTo 返回到货时刻不晚于 t 的未确认入库总量。
func (s *stockState) inboundUpTo(t clock.Time) int64 {
	var sum int64
	for _, in := range s.inbounds {
		if in.Arrival <= t {
			sum += in.Qty
		}
	}
	return sum
}

// inboundTotal 返回全部未确认入库总量（不看到货时刻）。
func (s *stockState) inboundTotal() int64 {
	var sum int64
	for _, in := range s.inbounds {
		sum += in.Qty
	}
	return sum
}

// available 计算可承诺量：现货 + 到货时刻不晚于 cutoff 的计划入库
// - 在 resNow 时刻仍有效的预留。调用前须已 sweep(resNow)。
func (s *stockState) available(cutoff clock.Time) int64 {
	return s.onHand + s.inboundUpTo(cutoff) - s.activeReserved
}

// foldArrivals 把到货时刻不晚于 now 的未确认入库转为现货。
// 对任何 t >= now 的查询/承诺，该转换不可观测（两边都计入），
// 因此不会改变任何可承诺量；它用于出库确认扣减现货时保持
// 现货非负。
func (s *stockState) foldArrivals(now clock.Time) {
	kept := s.inbounds[:0]
	for _, in := range s.inbounds {
		if in.Arrival <= now {
			s.onHand += in.Qty
		} else {
			kept = append(kept, in)
		}
	}
	// 清空尾部，避免保留已移除元素的引用。
	for i := len(kept); i < len(s.inbounds); i++ {
		s.inbounds[i] = nil
	}
	s.inbounds = kept
}
