package deadlock

// initiateProbeLocked 在事务开始等待时发起探测：沿其每条等待边发送
// 携带发起者与途经序列（初始仅含发起者）的探测消息。
func (m *Manager) initiateProbeLocked(t *Txn, waits []string) {
	if m.net == nil {
		return
	}
	for _, w := range waits {
		msg := Message{
			Probe: Probe{Initiator: t.id, Path: []string{t.id}},
			From:  t.id,
			To:    w,
		}
		m.logf("probe=send init=%s path=%v edge=%s->%s", t.id, msg.Probe.Path, t.id, w)
		m.net.Send(msg)
	}
}

// edgeExistsLocked 确认 from->to 这条等待边此刻仍存在。
func (m *Manager) edgeExistsLocked(from, to string) bool {
	t, ok := m.txns[from]
	if !ok || t.state != stWaiting {
		return false
	}
	for _, w := range m.waitsOnLocked(t) {
		if w == to {
			return true
		}
	}
	return false
}

// Deliver 由网络回调，投递一条探测消息。可并发调用。
//
// 丢弃规则：
//  1. 所沿等待边投递时已不存在；
//  2. 到达序列中已有的非发起者事务（环不含发起者，不再转发）。
//
// 回到发起者时，须确认途经的每条等待边此刻仍存在才判定死锁。
func (m *Manager) Deliver(msg Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := msg.Probe
	if !m.edgeExistsLocked(msg.From, msg.To) {
		m.logf("probe=drop init=%s path=%v edge=%s->%s reason=edge-gone", p.Initiator, p.Path, msg.From, msg.To)
		return
	}
	if msg.To == p.Initiator {
		m.confirmCycleLocked(p, msg.From)
		return
	}
	for _, id := range p.Path {
		if id == msg.To {
			m.logf("probe=drop init=%s path=%v edge=%s->%s reason=cycle-without-initiator", p.Initiator, p.Path, msg.From, msg.To)
			return
		}
	}
	to, ok := m.txns[msg.To]
	if !ok || to.state != stWaiting {
		m.logf("probe=drop init=%s path=%v edge=%s->%s reason=target-not-waiting", p.Initiator, p.Path, msg.From, msg.To)
		return
	}
	newPath := make([]string, len(p.Path)+1)
	copy(newPath, p.Path)
	newPath[len(p.Path)] = msg.To
	waits := m.waitsOnLocked(to)
	m.logf("probe=forward init=%s path=%v waits=%v", p.Initiator, newPath, waits)
	if m.net == nil {
		return
	}
	for _, w := range waits {
		m.net.Send(Message{Probe: Probe{Initiator: p.Initiator, Path: newPath}, From: msg.To, To: w})
	}
}

// confirmCycleLocked 在探测回到发起者时复核整条环：途经的每条等待边
// 此刻都必须仍然存在，全部成立才判定死锁并中止牺牲者，否则丢弃。
func (m *Manager) confirmCycleLocked(p Probe, lastFrom string) {
	cycle := p.Path
	for i := 0; i+1 < len(cycle); i++ {
		if !m.edgeExistsLocked(cycle[i], cycle[i+1]) {
			m.logf("probe=drop init=%s path=%v reason=stale-cycle edge=%s->%s gone", p.Initiator, cycle, cycle[i], cycle[i+1])
			return
		}
	}
	if !m.edgeExistsLocked(lastFrom, p.Initiator) {
		m.logf("probe=drop init=%s path=%v reason=stale-cycle edge=%s->%s gone", p.Initiator, cycle, lastFrom, p.Initiator)
		return
	}
	victim := cycle[0]
	ts := make([]int64, len(cycle))
	for i, id := range cycle {
		ts[i] = m.txns[id].ts
		if m.txns[id].ts > m.txns[victim].ts {
			victim = id
		}
	}
	m.logf("deadlock=confirmed cycle=%v ts=%v victim=%s reason=max-start-ts", cycle, ts, victim)
	m.abortLocked(victim)
}

// abortLocked 中止牺牲者：释放全部锁并按 FCFS 授予等待者。
func (m *Manager) abortLocked(victim string) {
	t, ok := m.txns[victim]
	if !ok || t.state == stAborted || t.state == stEnded {
		return
	}
	m.finishTxnLocked(t, stAborted)
	m.victims = append(m.victims, victim)
	m.logf("abort txn=%s victims=%v", victim, m.victims)
}
