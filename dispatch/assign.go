package dispatch

// insertion 描述一种插入位置及其评价指标。
type insertion struct {
	p, q      int   // 取货、送达停靠在插入后序列中的下标
	sumDelay  int64 // 对在途订单的送达总延后量（负变化不计）
	dropEta   int64 // 新订单自身送达推定到达时刻
	increment int64 // 插入后序列总耗时增量
}

// lessIns 实现位置选择次序：在途总延后量最小 -> 新订单送达最早 ->
// 取货位置更靠前 -> 送达位置更靠前（收尾保证完全确定）。
func lessIns(a, b insertion) bool {
	if a.sumDelay != b.sumDelay {
		return a.sumDelay < b.sumDelay
	}
	if a.dropEta != b.dropEta {
		return a.dropEta < b.dropEta
	}
	if a.p != b.p {
		return a.p < b.p
	}
	return a.q < b.q
}

// scanInsertions 枚举骑手当前序列的全部插入位置 (p, q)，返回最优可行位置。
// promiseOK 报告是否存在满足新订单自身承诺的位置（用于无可行骑手的原因分类）。
// 开销只与骑手当前未完成停靠数有关。
func (s *System) scanInsertions(r *riderState, o *orderState) (best insertion, feasible, promiseOK bool) {
	stops := r.stops
	n := len(stops)
	oldEta, oldLeave := project(s.src, s.lookup, r.pos, r.departAt, stops)
	oldTotal := totalDuration(r.departAt, oldLeave)
	oldDrop := make(map[string]int64, n)
	for i, st := range stops {
		if st.kind == StopDeliver {
			oldDrop[st.orderID] = oldEta[i]
		}
	}
	pk := stop{orderID: o.order.ID, kind: StopPickup, dwell: o.order.PickupDwell, etaCap: noEtaCap}
	dl := stop{orderID: o.order.ID, kind: StopDeliver, dwell: o.order.DropDwell, etaCap: noEtaCap}
	first := true
	for p := 0; p <= n; p++ {
		for q := p + 1; q <= n+1; q++ {
			cand := insertStops(stops, p, q, pk, dl)
			eta, leave := project(s.src, s.lookup, r.pos, r.departAt, cand)
			dropEta := eta[q]
			if dropEta > o.order.PromiseAt {
				continue
			}
			promiseOK = true
			ok := true
			var sumDelay int64
			for i, st := range cand {
				if st.kind != StopDeliver || st.orderID == o.order.ID {
					continue
				}
				d := eta[i] - oldDrop[st.orderID]
				if d > 0 {
					sumDelay += d
				}
				if eta[i] > s.orders[st.orderID].order.PromiseAt {
					ok = false
					break
				}
				if d > s.cfg.MaxDetour {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			cur := insertion{
				p: p, q: q,
				sumDelay:  sumDelay,
				dropEta:   dropEta,
				increment: totalDuration(r.departAt, leave) - oldTotal,
			}
			if first || lessIns(cur, best) {
				best, first = cur, false
			}
		}
	}
	feasible = !first
	return best, feasible, promiseOK
}

// riderPick 是一名可行骑手及其最优插入。
type riderPick struct {
	r   *riderState
	ins insertion
}

// betterPick 实现骑手选择次序：总耗时增量最小 -> 当前持有订单数更少 ->
// 骑手标识字典序更小。
func betterPick(a, b riderPick) bool {
	if a.ins.increment != b.ins.increment {
		return a.ins.increment < b.ins.increment
	}
	if a.r.held != b.r.held {
		return a.r.held < b.r.held
	}
	return a.r.id < b.r.id
}
