package pharmacy

import "fmt"

// Snapshot 直接读取当前状态（不推进事件、不走钟），供测试与审计全量比对。
func (e *Engine) Snapshot() (map[string]DrugState, map[string]RxState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.snapshotLocked()
}

func (e *Engine) snapshotLocked() (map[string]DrugState, map[string]RxState) {
	drugs := make(map[string]DrugState, len(e.drugs))
	for id, d := range e.drugs {
		drugs[id] = DrugState{
			ID: id, BoxSize: d.box, Splittable: d.splittable,
			OnHand: d.onHand, Reserved: d.reserved,
			Available: d.onHand - d.reserved, BackorderTotal: d.backorderTotal,
		}
	}
	rxs := make(map[string]RxState, len(e.rxs))
	for id, p := range e.rxs {
		st := RxState{ID: p.id, Patient: p.patient, IssueTime: p.issue, WholeOrder: p.whole, Status: p.status}
		for _, l := range p.lines {
			st.Lines = append(st.Lines, LineState{
				DrugID: l.drug.id, Demand: l.demand, Reserved: l.reserved,
				Dispensed: l.dispensed, Backorder: l.backorder, Status: lineStatus(l),
			})
		}
		rxs[id] = st
	}
	return drugs, rxs
}

// CheckInvariants 校验规格要求的全局不变量，返回首个违例（无则 nil）：
//   - 任一药品可用量不为负；
//   - 已发放量与有效预留量之和不超过累计入库量；
//   - 不可拆零药品的预留与已发放量均为整盒量整数倍；
//   - 欠药总量计数、欠药队列与各行状态一致；
//   - 每个有效预留恰有一个失效事件。
func (e *Engine) CheckInvariants() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	resEvents := map[*Reservation]int{}
	for _, ev := range e.heap.s {
		if ev.kind == evResExpiry {
			resEvents[ev.res]++
		}
	}
	for _, d := range e.drugs {
		if d.reserved < 0 || d.onHand < d.reserved {
			return fmt.Errorf("药品 %s 可用量为负 onHand=%d reserved=%d", d.id, d.onHand, d.reserved)
		}
		sumRes, sumBo, sumDisp, liveBo := 0, 0, 0, 0
		for _, p := range e.rxs {
			for _, l := range p.lines {
				if l.drug != d {
					continue
				}
				sumBo += l.backorder
				sumDisp += l.dispensed
				if !d.splittable && l.dispensed%d.box != 0 {
					return fmt.Errorf("不可拆零药品 %s 已发放 %d 非整盒倍数", d.id, l.dispensed)
				}
				if l.backorder > 0 {
					liveBo++
					if l.bo == nil {
						return fmt.Errorf("药品 %s 欠药行未挂队列", d.id)
					}
				} else if l.bo != nil {
					return fmt.Errorf("药品 %s 无欠药行仍挂队列", d.id)
				}
				for _, r := range l.reservations {
					if !r.active {
						continue
					}
					sumRes += r.qty
					if !d.splittable && r.qty%d.box != 0 {
						return fmt.Errorf("不可拆零药品 %s 预留 %d 非整盒倍数", d.id, r.qty)
					}
					if resEvents[r] != 1 {
						return fmt.Errorf("药品 %s 有效预留缺少失效事件", d.id)
					}
				}
			}
		}
		if sumRes != d.reserved {
			return fmt.Errorf("药品 %s 预留计数 %d != 行合计 %d", d.id, d.reserved, sumRes)
		}
		if sumBo != d.backorderTotal {
			return fmt.Errorf("药品 %s 欠药计数 %d != 行合计 %d", d.id, d.backorderTotal, sumBo)
		}
		if sumDisp+d.reserved > d.totalInbound {
			return fmt.Errorf("药品 %s 已发放+有效预留 %d 超过累计入库 %d", d.id, sumDisp+d.reserved, d.totalInbound)
		}
		qlen := 0
		for n := d.bq.head.next; n != d.bq.tail; n = n.next {
			qlen++
			if n.line.backorder <= 0 || n.line.bo != n {
				return fmt.Errorf("药品 %s 欠药队列含失效节点", d.id)
			}
		}
		if qlen != liveBo || qlen != d.bq.length {
			return fmt.Errorf("药品 %s 欠药队列长度 %d 与活欠药行 %d 不符", d.id, qlen, liveBo)
		}
	}
	return nil
}
