package replication

import "sort"

// DefaultMaxInProgress 是进行中事务数的默认上限。
const DefaultMaxInProgress = 1024

// txnState 是单个事务在状态机内部的生命周期状态。
type txnState struct {
	xid       uint64
	records   []Record // 该事务已收到的全部记录（含 Begin，不含 Commit/Abort 本身）
	commitLSN uint64   // 提交记录的 LSN；仅在提交后有效
}

// Machine 是复制槽的纯内存状态机，不涉及任何 I/O。
// 所有方法必须串行调用；Slot 在其外层加锁以支持并发。
//
// 不变量：
//   - log 中记录的 LSN 严格递增，且全部 >= restartLSN；
//   - 0 <= restartLSN <= confirmedLSN < nextLSN；
//   - active 为进行中事务，pending 为已提交但尚未确认的事务；
//   - 重启位点 = min(confirmedLSN, 所有保留事务的起点 LSN)。
type Machine struct {
	maxInProgress int
	nextLSN       uint64
	confirmedLSN  uint64
	restartLSN    uint64
	log           []Record
	active        map[uint64]*txnState
	pending       map[uint64]*txnState // key 为 XID，已提交未确认的事务
}

// NewMachine 创建一个初始状态机。maxInProgress <= 0 时使用默认上限。
func NewMachine(maxInProgress int) *Machine {
	if maxInProgress <= 0 {
		maxInProgress = DefaultMaxInProgress
	}
	return &Machine{
		maxInProgress: maxInProgress,
		nextLSN:       1,
		active:        make(map[uint64]*txnState),
		pending:       make(map[uint64]*txnState),
	}
}

// validateRecord 在不触碰任何状态的前提下校验一条日志记录。
func (m *Machine) validateRecord(r Record) error {
	return m.validateCheck(r, m.nextLSN)
}

// validateCheck 使用调用方指定的期望最小 LSN 进行校验。
// 恢复重放时日志可能因回收而存在 LSN 缺口，因此第一条记录只要求 LSN
// 严格递增关系（后续记录必须 > 前一条），不要求从 1 连续开始。
func (m *Machine) validateCheck(r Record, expectNext uint64) error {
	if r.XID == 0 {
		return ErrInvalidRecord
	}
	if r.LSN < expectNext {
		return ErrInvalidRecord
	}
	switch r.Kind {
	case KindBegin:
		if _, ok := m.active[r.XID]; ok {
			return ErrInvalidRecord // 事务已开始，重复 Begin
		}
		if _, ok := m.pending[r.XID]; ok {
			return ErrInvalidRecord // 已提交未确认的事务不能再以同一 XID 开始
		}
	case KindData, KindCommit, KindAbort:
		if _, ok := m.active[r.XID]; !ok {
			return ErrInvalidRecord // 没有对应的进行中事务
		}
	default:
		return ErrInvalidRecord // 未知记录类型
	}
	// Begin 必须在所有结构性校验之后才检查上限，保证错误类型可区分。
	if r.Kind == KindBegin && len(m.active) >= m.maxInProgress {
		return ErrTooManyInProgress
	}
	return nil
}

// ApplyRecord 追加并解码一条日志记录。返回非 nil error 时状态不发生任何变化。
// 当记录使某个事务提交时，整体返回该事务；其它情况返回 nil。
func (m *Machine) ApplyRecord(r Record) (*Transaction, error) {
	return m.applyCheck(r, m.nextLSN)
}

// applyCheck 以指定的期望最小 LSN 校验并应用记录，是 ApplyRecord 的内部实现。
func (m *Machine) applyCheck(r Record, expectNext uint64) (*Transaction, error) {
	if err := m.validateCheck(r, expectNext); err != nil {
		return nil, err
	}
	m.log = append(m.log, r)
	m.nextLSN = r.LSN + 1

	switch r.Kind {
	case KindBegin:
		m.active[r.XID] = &txnState{xid: r.XID, records: []Record{r}}
		return nil, nil
	case KindData:
		txn := m.active[r.XID]
		txn.records = append(txn.records, r)
		return nil, nil
	case KindCommit:
		txn := m.active[r.XID]
		delete(m.active, r.XID)
		txn.commitLSN = r.LSN
		m.pending[r.XID] = txn
		m.recomputeRestart()
		return txn.toTransaction(r), nil
	default: // KindAbort
		delete(m.active, r.XID)
		// 中止事务永远不会发出，其所有记录（含 Abort）立即丢弃。
		m.discardTxnRecords(r.XID, KindBegin, KindData, KindAbort)
		m.recomputeRestart()
		return nil, nil
	}
}

// discardTxnRecords 从保留日志中移除指定事务的给定类型记录。
func (m *Machine) discardTxnRecords(xid uint64, kinds ...Kind) {
	drop := make(map[Kind]bool, len(kinds))
	for _, k := range kinds {
		drop[k] = true
	}
	kept := m.log[:0]
	for _, r := range m.log {
		if r.XID == xid && drop[r.Kind] {
			continue
		}
		kept = append(kept, r)
	}
	m.log = kept
}

// Confirm 把确认位点推进到某个已发出（已提交）事务的提交位点。
// 确认位点只能前进；等于当前位点时视为幂等成功且不改变任何状态。
// 任何非 nil error 都不会改变状态。
func (m *Machine) Confirm(lsn uint64) error {
	if lsn < m.confirmedLSN {
		return ErrConfirmRewound
	}
	if lsn == m.confirmedLSN {
		return nil
	}
	if m.findPending(lsn) == nil {
		return ErrInvalidConfirm
	}
	// 一次确认提交位点会一并确认所有更早提交的事务。
	for xid, txn := range m.pending {
		if txn.commitLSN <= lsn {
			delete(m.pending, xid)
		}
	}
	m.confirmedLSN = lsn
	m.recomputeRestart()
	return nil
}

// findPending 返回提交位点等于 commitLSN 的已提交未确认事务，没有则返回 nil。
func (m *Machine) findPending(commitLSN uint64) *txnState {
	for _, txn := range m.pending {
		if txn.commitLSN == commitLSN {
			return txn
		}
	}
	return nil
}

// recomputeRestart 重新计算重启位点并回收早于该位点的日志。
// 重启位点 = min(确认位点, 所有保留事务的起点)；当不存在更老的保留事务时，
// 取确认位点（其之后的第一条保留记录构成新的保留起点，且不会被误回收）。
func (m *Machine) recomputeRestart() {
	restart := m.confirmedLSN
	consider := func(txn *txnState) {
		start := txn.records[0].LSN
		if start < restart {
			restart = start
		}
	}
	for _, txn := range m.active {
		consider(txn)
	}
	for _, txn := range m.pending {
		consider(txn)
	}
	m.restartLSN = restart

	// 回收 LSN < restartLSN 的日志前缀。
	cut := 0
	for cut < len(m.log) && m.log[cut].LSN < restart {
		cut++
	}
	if cut > 0 {
		m.log = append([]Record(nil), m.log[cut:]...)
	}
}

func (t *txnState) toTransaction(commit Record) *Transaction {
	records := make([]Record, 0, len(t.records)+1)
	records = append(records, t.records...)
	records = append(records, commit)
	return &Transaction{
		XID:       t.xid,
		CommitLSN: commit.LSN,
		Records:   records,
	}
}

// ConfirmedLSN 返回确认位点。
func (m *Machine) ConfirmedLSN() uint64 { return m.confirmedLSN }

// RestartLSN 返回重启位点。
func (m *Machine) RestartLSN() uint64 { return m.restartLSN }

// NextLSN 返回下一条日志记录应当使用的 LSN。
func (m *Machine) NextLSN() uint64 { return m.nextLSN }

// InProgress 返回当前进行中的事务数量。
func (m *Machine) InProgress() int { return len(m.active) }

// retainedLog 返回当前仍需保留的日志记录（LSN >= 重启位点）。
func (m *Machine) retainedLog() []Record { return m.log }

// PendingTransactions 返回已提交但未确认的事务，按提交 LSN 升序排列。
// 崩溃重启后用它重新发出消费端尚未确认的事务。
func (m *Machine) PendingTransactions() []Transaction {
	commitLSNs := make(uint64Slice, 0, len(m.pending))
	for _, txn := range m.pending {
		commitLSNs = append(commitLSNs, txn.commitLSN)
	}
	sort.Sort(commitLSNs)
	result := make([]Transaction, 0, len(commitLSNs))
	for _, commitLSN := range commitLSNs {
		txn := m.findPending(commitLSN)
		result = append(result, *txn.toTransaction(Record{
			LSN:  commitLSN,
			XID:  txn.xid,
			Kind: KindCommit,
		}))
	}
	return result
}

type uint64Slice []uint64

func (s uint64Slice) Len() int           { return len(s) }
func (s uint64Slice) Less(i, j int) bool { return s[i] < s[j] }
func (s uint64Slice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// Replay 从起始状态开始重放一批日志记录。
//
// confirmedLSN 是持久化的确认位点：提交 LSN <= confirmedLSN 的事务视为
// 已确认，不会重新发出；其余已提交事务按提交 LSN 升序作为输出返回，
// 保证崩溃重启后“不重不漏”。对相同输入必然产生相同输出。
func Replay(records []Record, confirmedLSN uint64, maxInProgress int) ([]Transaction, *Machine, error) {
	m := NewMachine(maxInProgress)
	emitted := make([]Transaction, 0)
	expectNext := uint64(1)
	for _, r := range records {
		// 已确认事务可能只剩提交标记（私有记录已随解码完成而丢弃），
		// 恢复时整体跳过，不作为事务边界重新处理。
		if r.Kind == KindCommit && r.LSN <= confirmedLSN {
			expectNext = r.LSN + 1
			continue
		}
		txn, err := m.applyCheck(r, expectNext)
		if err != nil {
			return nil, nil, err
		}
		expectNext = r.LSN + 1
		if txn != nil && txn.CommitLSN > confirmedLSN {
			emitted = append(emitted, *txn)
		}
	}
	// 对齐确认位点：已确认事务的提交记录可能已被回收，不能走普通 Confirm，
	// 直接校正内部状态（仅恢复路径使用）。
	if confirmedLSN > 0 {
		for xid, txn := range m.pending {
			if txn.commitLSN <= confirmedLSN {
				delete(m.pending, xid)
			}
		}
		m.confirmedLSN = confirmedLSN
		m.recomputeRestart()
	}
	sort.Slice(emitted, func(i, j int) bool { return emitted[i].CommitLSN < emitted[j].CommitLSN })
	return emitted, m, nil
}
