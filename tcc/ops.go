package tcc

import "ontology/ledger"

const MaxAmount = 1_000_000_000_000 // amount ∈ [1,1e12]

// Try 预留资源：幂等规则、悬挂防护与容量/余额判定见 DESIGN.md。
func (m *Manager) Try(xid, br, acct []byte, amount, now int64) error {
	if len(xid) == 0 || len(br) == 0 || len(acct) == 0 || amount < 1 || amount > MaxAmount {
		return ledger.ErrParam
	}
	if err := checkNow(now); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.clock {
		return ledger.ErrClock
	}
	key := branchKey{string(xid), string(br)}
	due := m.popDueLocked(now)
	b, exists := m.branches[key]
	if exists && dueContains(due, key) && b.State == StateTried {
		b = &Branch{State: StateCancelled, Reason: CancelExpired} // 虚拟视图
		exists = true
	}
	if exists {
		switch b.State {
		case StateTried: // 虚拟到期后不会命中这里
			if string(b.Acct) != string(acct) || b.Amount != amount {
				m.restoreDueLocked(due)
				return ledger.ErrMismatch
			}
			m.applyDueLocked(due)
			m.clock = now // 幂等成功：不重复冻结，不刷新到期
			return nil
		case StateConfirmed:
			m.restoreDueLocked(due)
			return ledger.ErrState
		default:
			m.restoreDueLocked(due)
			return ledger.ErrHanging // Empty / Cancel / Expired 均阻止迟到 Try
		}
	}
	if len(m.branches) >= m.capacity {
		m.restoreDueLocked(due)
		return ledger.ErrCapacity
	}
	// 虚拟到期会先释放同账户冻结额，可用额据此判定。
	if m.lg.Avail(acct)+m.releasedOnAcct(due, string(acct)) < amount {
		m.restoreDueLocked(due)
		return ledger.ErrInsufficient // 被拒绝，不落实到期、不推进时钟
	}
	m.applyDueLocked(due)
	if err := m.lg.Freeze(acct, amount); err != nil {
		return err
	}
	nb := &Branch{
		Acct:     append([]byte(nil), acct...),
		Amount:   amount,
		Deadline: now + m.ttl,
		State:    StateTried,
	}
	m.branches[key] = nb
	e := &heapEntry{key: key, deadline: nb.Deadline}
	m.entries[key] = e
	m.heap.push(e)
	m.clock = now
	return nil
}

// Confirm 确认扣减；Tried → bal/fz 同减并 Confirmed。
func (m *Manager) Confirm(xid, br []byte, now int64) error {
	if len(xid) == 0 || len(br) == 0 {
		return ledger.ErrParam
	}
	if err := checkNow(now); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.clock {
		return ledger.ErrClock
	}
	key := branchKey{string(xid), string(br)}
	due := m.popDueLocked(now)
	b, ok := m.branches[key]
	if ok && b.State == StateTried && dueContains(due, key) {
		b = &Branch{State: StateCancelled, Reason: CancelExpired}
	}
	if !ok {
		m.restoreDueLocked(due)
		return ledger.ErrNoBranch
	}
	switch b.State {
	case StateConfirmed:
		m.applyDueLocked(due)
		m.clock = now
		return nil
	case StateTried:
		m.applyDueLocked(due)
		m.heap.remove(m.entries[key])
		delete(m.entries, key)
		m.lg.Confirm(b.Acct, b.Amount)
		b.State = StateConfirmed
		m.clock = now
		return nil
	default:
		m.restoreDueLocked(due)
		if b.Reason == CancelExpired {
			return ledger.ErrExpired
		}
		return ledger.ErrConflict
	}
}

// Cancel 取消：不存在则写空回滚标记（容量满则 ErrCapacity 不写）。
func (m *Manager) Cancel(xid, br []byte, now int64) error {
	if len(xid) == 0 || len(br) == 0 {
		return ledger.ErrParam
	}
	if err := checkNow(now); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if now < m.clock {
		return ledger.ErrClock
	}
	key := branchKey{string(xid), string(br)}
	due := m.popDueLocked(now)
	b, ok := m.branches[key]
	if ok && b.State == StateTried && dueContains(due, key) {
		b = &Branch{State: StateCancelled, Reason: CancelExpired}
	}
	if !ok {
		if len(m.branches) >= m.capacity {
			m.restoreDueLocked(due)
			return ledger.ErrCapacity
		}
		m.applyDueLocked(due)
		m.branches[key] = &Branch{State: StateCancelled, Reason: CancelEmpty}
		m.clock = now
		return nil
	}
	switch b.State {
	case StateCancelled: // 任一原因均幂等成功
		m.applyDueLocked(due)
		m.clock = now
		return nil
	case StateTried:
		m.applyDueLocked(due)
		m.heap.remove(m.entries[key])
		delete(m.entries, key)
		m.lg.Unfreeze(b.Acct, b.Amount)
		b.State = StateCancelled
		b.Reason = CancelExplicit
		m.clock = now
		return nil
	default:
		m.restoreDueLocked(due)
		return ledger.ErrConflict // Confirmed
	}
}
