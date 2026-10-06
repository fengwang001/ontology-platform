package standby

// 本文件负责：
//  1. settleExpirations —— 把截至某时刻的所有待确认过期按时序精确结算（同刻合并、链式兑现）；
//  2. fulfill —— 在给定释放时刻按优先规则做一次全有/全无兑现。
//
// 所有状态改动都经过 txn 记录，被拒绝的操作可整体撤销。

// waitKey 返回候补有序集合键：(登记时刻, 条目标识)，均以无符号比较。
func waitKey(e *Entry) (uint64, uint64) {
	return uint64(e.Registered), uint64(e.ID)
}

// pendKey 返回待确认有序集合键：(确认期限, 条目标识)。
func pendKey(e *Entry) (uint64, uint64) {
	return uint64(e.Deadline), uint64(e.ID)
}

// freeSeats 返回 容量 - 已确认人数 - 待确认人数（容量下调后可为负）。
func (f *flight) freeSeats() int {
	return f.capacity - f.confirmed - f.pendingN
}

// removePending 把待确认条目移出 pending 集合并释放其人数占用。
func (f *flight) removePending(t *txn, e *Entry) {
	leaf := e.pendLeaf
	deadline := e.Deadline
	f.pending.delete(leaf)
	e.pendLeaf = nil
	f.pendingN -= e.Party
	_, hadActive := f.active[e.Passenger]
	delete(f.active, e.Passenger)
	t.undo = append(t.undo, func() {
		l := &trieLeaf{entry: e, keyHi: uint64(deadline), keyLo: uint64(e.ID)}
		f.pending.insert(l)
		e.pendLeaf = l
		f.pendingN += e.Party
		if hadActive {
			f.active[e.Passenger] = e
		}
	})
}

// moveWaitingToPending 将候补条目转为待确认，期限为 at + 配置时长。
func (f *flight) moveWaitingToPending(t *txn, e *Entry, at int64) {
	oldLeaf := e.waitLeaf
	prio := e.Priority
	f.waiting[prio].delete(oldLeaf)
	f.waitingN--
	_, hadActive := f.active[e.Passenger]
	newLeaf := &trieLeaf{entry: e}
	newLeaf.keyHi, newLeaf.keyLo = uint64(at+f.delay), uint64(e.ID)
	f.pending.insert(newLeaf)
	f.pendingN += e.Party
	oldDeadline := e.Deadline
	e.waitLeaf = nil
	e.pendLeaf = newLeaf
	e.Deadline = at + f.delay
	e.State = StatePending
	t.undo = append(t.undo, func() {
		e.Deadline = oldDeadline
		e.State = StateWaiting
		e.pendLeaf = nil
		f.pending.delete(newLeaf)
		f.pendingN -= e.Party
		f.waiting[prio].insert(oldLeaf)
		f.waitingN++
		e.waitLeaf = oldLeaf
		if hadActive {
			f.active[e.Passenger] = e
		}
	})
}

// expireEntry 把待确认条目置为已过期并释放其占用。
func (f *flight) expireEntry(t *txn, e *Entry) {
	oldLeaf := e.pendLeaf
	deadline := e.Deadline
	f.pending.delete(oldLeaf)
	f.pendingN -= e.Party
	_, hadActive := f.active[e.Passenger]
	delete(f.active, e.Passenger)
	e.pendLeaf = nil
	e.State = StateExpired
	t.undo = append(t.undo, func() {
		e.State = StatePending
		l := &trieLeaf{entry: e, keyHi: uint64(deadline), keyLo: uint64(e.ID)}
		f.pending.insert(l)
		e.pendLeaf = l
		f.pendingN += e.Party
		if hadActive {
			f.active[e.Passenger] = e
		}
	})
}

// settleExpirations 结算所有 deadline <= now 的待确认条目。
// 同一 deadline 的多条条目合并释放后只兑现一次；两次过期之间登记的新条目
// （registered > 上一释放时刻）在本次兑现中被 seek 边界排除，不能提前参与。
func (f *flight) settleExpirations(t *txn, now int64) {
	for {
		leaf := f.pending.seek(0, 0)
		if leaf == nil || leaf.entry.Deadline > now {
			return
		}
		deadline := leaf.entry.Deadline
		// 收集同一时刻过期的全部条目，合并释放。
		var batch []*Entry
		for leaf != nil && leaf.entry.Deadline == deadline {
			batch = append(batch, leaf.entry)
			leaf = f.pending.next(leaf)
		}
		for _, e := range batch {
			f.expireEntry(t, e)
		}
		// 合并后在过期时刻只兑现一次。
		f.fulfill(t, deadline)
	}
}

// fulfill 在释放时刻 at 触发一次全有/全无兑现。
// 仅考虑 registered <= at 的候补条目；空余数为 0 立即停止。
// 被跳过的大条目原样留在集合中（只是扫描时越过），位置天然保留。
func (f *flight) fulfill(t *txn, at int64) {
	free := f.freeSeats()
	if free <= 0 {
		return
	}
	for p := int(PrioHigh); p >= int(PrioLow) && free > 0; p-- {
		set := f.waiting[p]
		leaf := set.seek(0, 0)
		// 越过 registered > at 的条目：这些条目在释放时刻之后才登记。
		for leaf != nil && leaf.entry.Registered > at {
			leaf = set.next(leaf)
		}
		for leaf != nil && free > 0 {
			e := leaf.entry
			if e.Registered > at {
				break
			}
			nextHi, nextLo := leaf.keyHi, leaf.keyLo
			if e.Party <= free {
				free -= e.Party
				f.moveWaitingToPending(t, e, at)
				// 节点可能已被收缩删除，按后继键重新定位。
				leaf = set.seek(nextHi, nextLo+1)
			} else {
				leaf = set.next(leaf)
			}
		}
	}
}

// txn 是一次操作内的撤销日志。
// 规则要求“被拒绝的操作不推进时钟、不触发过期结算、不改任何条目”，
// 而过期结算必须在部分拒绝检查（重复登记/队列满）之前完成；
// 因此先试探性结算，若操作随后被拒绝，则按日志逆序完全撤销。
type txn struct {
	undo    []func()
	idTaken bool // 本次事务是否消费了一个新的条目标识
	sys     *System
}

func (s *System) begin() *txn {
	return &txn{sys: s}
}

func (t *txn) rollback() {
	for i := len(t.undo) - 1; i >= 0; i-- {
		t.undo[i]()
	}
	if t.idTaken {
		t.sys.nextID--
	}
}
