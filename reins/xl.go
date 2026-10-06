package reins

import "sort"

// eventAgg 是同一事故编号下所有赔款净自留的聚合。
type eventAgg struct {
	id        string
	time      int64
	claims    map[string]*claim
	netTotal  int64
	capBefore int64 // 排序在前的事故已消耗的层能力
	xlPaid    int64
}

// eventLess 定义事故处理次序：先事故时刻，时刻相同按事故编号字典序。
func eventLess(a, b *eventAgg) bool {
	if a.time != b.time {
		return a.time < b.time
	}
	return a.id < b.id
}

// insertEvent 把事故插入有序切片的应有位置，返回下标。
func (e *Engine) insertEvent(ev *eventAgg) int {
	pos := sort.Search(len(e.events), func(i int) bool { return !eventLess(e.events[i], ev) })
	e.events = append(e.events, nil)
	copy(e.events[pos+1:], e.events[pos:])
	e.events[pos] = ev
	return pos
}

func (e *Engine) removeEventAt(pos int) {
	e.events = append(e.events[:pos], e.events[pos+1:]...)
}

func (e *Engine) eventPos(ev *eventAgg) int {
	return sort.Search(len(e.events), func(i int) bool { return !eventLess(e.events[i], ev) })
}

// addToEvent 把赔款并入其事故（按事故编号聚合，必要时新建事故），
// 随后仅从受影响下标起重算；事故时刻取该事故全部赔款时刻的最小值，
// 因此晚报插入不影响排序更早的事故。
func (e *Engine) addToEvent(c *claim) {
	ev, ok := e.eventByID[c.eventID]
	if !ok {
		ev = &eventAgg{id: c.eventID, time: c.time, claims: make(map[string]*claim)}
		e.eventByID[c.eventID] = ev
		ev.claims[c.no] = c
		ev.netTotal = c.net
		e.recomputeFrom(e.insertEvent(ev))
		return
	}
	ev.claims[c.no] = c
	ev.netTotal += c.net
	if c.time < ev.time {
		e.removeEventAt(e.eventPos(ev))
		ev.time = c.time
		e.recomputeFrom(e.insertEvent(ev))
		return
	}
	e.recomputeFrom(e.eventPos(ev))
}

// removeFromEvent 从事故中移除赔款；事故空了则删除事故，
// 事故时刻可能因最小值持有者离开而后移，重算取新旧位置的较小者。
func (e *Engine) removeFromEvent(c *claim) {
	ev := e.eventByID[c.eventID]
	oldPos := e.eventPos(ev)
	delete(ev.claims, c.no)
	ev.netTotal -= c.net
	if len(ev.claims) == 0 {
		e.removeEventAt(oldPos)
		delete(e.eventByID, ev.id)
		e.recomputeFrom(oldPos)
		return
	}
	if c.time == ev.time {
		minT := int64(1<<63 - 1)
		for _, oc := range ev.claims {
			if oc.time < minT {
				minT = oc.time
			}
		}
		if minT != ev.time {
			e.removeEventAt(oldPos)
			ev.time = minT
			newPos := e.insertEvent(ev)
			if newPos < oldPos {
				oldPos = newPos
			}
			e.recomputeFrom(oldPos)
			return
		}
	}
	e.recomputeFrom(oldPos)
}

// xlEntitlement 事故按合并后净自留总额应得的层承担（不考虑能力余量）。
func (e *Engine) xlEntitlement(ev *eventAgg) int64 {
	if ev.netTotal <= e.treaty.XLRetention {
		return 0
	}
	x := ev.netTotal - e.treaty.XLRetention
	if x > e.treaty.XLLimit {
		x = e.treaty.XLLimit
	}
	return x
}

// recomputeFrom 只重算下标 idx 起的事故；idx 之前的事故未受影响，
// 其 capBefore 仍然有效，因此重算范围严格限于事故时刻不早于插入点的事故。
func (e *Engine) recomputeFrom(idx int) {
	capUsed := int64(0)
	if idx > 0 {
		prev := e.events[idx-1]
		capUsed = prev.capBefore + prev.xlPaid
	}
	total := e.treaty.xlTotalCapacity()
	for i := idx; i < len(e.events); i++ {
		ev := e.events[i]
		ev.capBefore = capUsed
		paid := e.xlEntitlement(ev)
		if remain := total - capUsed; paid > remain {
			paid = remain
		}
		ev.xlPaid = paid
		capUsed += paid
		e.distributeXL(ev)
		e.stats.RecomputedEvents++
	}
}

// distributeXL 把事故的层承担按各赔款净自留比例向下取整分摊，
// 尾差按赔款号字典序逐分补齐，保证分摊与赔款到达次序无关。
func (e *Engine) distributeXL(ev *eventAgg) {
	nos := make([]string, 0, len(ev.claims))
	for no := range ev.claims {
		nos = append(nos, no)
	}
	sort.Strings(nos)
	var assigned int64
	for _, no := range nos {
		c := ev.claims[no]
		c.xlRecovered = c.net * ev.xlPaid / ev.netTotal
		assigned += c.xlRecovered
	}
	// 尾差不足 len(nos) 分（向下取整性质），按赔款号顺序逐分补齐。
	for i := int64(0); i < ev.xlPaid-assigned; i++ {
		ev.claims[nos[i]].xlRecovered++
	}
}
