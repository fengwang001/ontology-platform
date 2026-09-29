package spill

import (
	"fmt"
	"sort"
	"sync"
)

// Row 是事务追加的一行数据。
type Row struct {
	Data string
}

// Config 描述内存与溢写存储的上限。
// MemRowLimit：所有未结束事务在内存中的行数总和上限（必须 >= 1）。
// BlockLimit：溢写块总数上限，块号永不复用（必须 >= 1）。
type Config struct {
	MemRowLimit int
	BlockLimit  int
}

// txn 保存一个未结束事务的状态。
// memRows 中保留尚未溢写的追加行；blockIDs 按溢写发生顺序记录块号
// （块号单调递增，因此升序即为追加顺序）。
type txn struct {
	id       uint64
	memRows  []Row
	blockIDs []uint64
}

// Manager 是大事务溢写与按序回放管理器，可被多个执行体并发调用。
type Manager struct {
	mu sync.RWMutex

	memLimit   int
	blockLimit int

	txns      map[uint64]*txn
	blocks    map[uint64][]Row
	nextBlock uint64
	committed []Row
	logger    Logger
}

// Logger 记录每步输入、当前内存行数与判定依据。
type Logger interface {
	Log(event Event)
}

// Event 是一条判定日志事件。
type Event struct {
	Op        string
	TxnID     uint64
	InputRows int
	MemRows   int
	Decision  string
	Detail    string
}

// NewManager 创建管理器；非法参数返回 ReasonInvalidArgument。
func NewManager(cfg Config, logger Logger) (*Manager, error) {
	if cfg.MemRowLimit < 1 || cfg.BlockLimit < 1 {
		return nil, newError("new", ReasonInvalidArgument,
			"MemRowLimit and BlockLimit must both be >= 1")
	}
	return &Manager{
		memLimit:   cfg.MemRowLimit,
		blockLimit: cfg.BlockLimit,
		txns:       make(map[uint64]*txn),
		blocks:     make(map[uint64][]Row),
		logger:     logger,
	}, nil
}

// Begin 开启一个新事务；txnID 必须为正且不得重复。
func (m *Manager) Begin(txnID uint64) error {
	if txnID == 0 {
		return newError("begin", ReasonInvalidArgument, "transaction id must be > 0")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.txns[txnID]; ok {
		return newError("begin", ReasonTxnDuplicate, "transaction already open")
	}
	m.txns[txnID] = &txn{id: txnID}
	m.logLocked(Event{Op: "begin", TxnID: txnID, Decision: "accepted",
		Detail: "new open transaction"})
	return nil
}

// Append 向事务追加若干行；必要时触发溢写，溢写存储满则整体拒绝。
// 返回本次追加是否触发了溢写以及新产生的块号（按产生顺序）。
func (m *Manager) Append(txnID uint64, rows []Row) (spilled bool, blockIDs []uint64, err error) {
	if txnID == 0 {
		return false, nil, newError("append", ReasonInvalidArgument, "transaction id must be > 0")
	}
	if len(rows) == 0 {
		return false, nil, newError("append", ReasonInvalidArgument, "rows must not be empty")
	}
	for i := range rows {
		if rows[i].Data == "" {
			return false, nil, newError("append", ReasonInvalidArgument, "row data must not be empty")
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.txns[txnID]
	if !ok {
		return false, nil, newError("append", ReasonTxnNotFound, "no open transaction with that id")
	}

	before := m.totalMemRowsLocked()

	// Dry-run：在临时计数上模拟本次追加及逐次溢写。
	// 只有全部溢写块都有容量时才真正落状态，保证“失败不留痕”。
	mem := make(map[uint64]int, len(m.txns))
	for id, ot := range m.txns {
		mem[id] = len(ot.memRows)
	}
	mem[txnID] += len(rows)
	total := before + len(rows)
	var plan []uint64
	for total > m.memLimit {
		victim := uint64(0)
		victimRows := 0
		for id, n := range mem {
			if n == 0 {
				continue
			}
			if n > victimRows || (n == victimRows && (victim == 0 || id < victim)) {
				victim, victimRows = id, n
			}
		}
		if victim == 0 {
			return false, nil, newError("append", ReasonInvalidArgument, "no spillable in-memory rows")
		}
		plan = append(plan, victim)
		mem[victim] = 0
		total -= victimRows
	}
	if len(m.blocks)+len(plan) > m.blockLimit {
		m.logLocked(Event{Op: "append", TxnID: txnID, InputRows: len(rows), MemRows: before,
			Decision: "rejected: spill storage full",
			Detail: detailf("blocks=%d needed=%d limit=%d; state unchanged",
				len(m.blocks), len(plan), m.blockLimit)})
		return false, nil, newError("append", ReasonSpillStorageFull,
			detailf("spill blocks %d + needed %d exceed limit %d", len(m.blocks), len(plan), m.blockLimit))
	}

	// 正式落状态：先加入内存，再按计划逐次打包溢写块。
	t.memRows = append(t.memRows, rows...)
	for _, victim := range plan {
		vt := m.txns[victim]
		id := m.nextBlock
		m.nextBlock++
		packed := make([]Row, len(vt.memRows))
		copy(packed, vt.memRows)
		m.blocks[id] = packed
		vt.blockIDs = append(vt.blockIDs, id)
		vt.memRows = nil
		blockIDs = append(blockIDs, id)
		m.logLocked(Event{Op: "spill", TxnID: victim, InputRows: len(rows),
			MemRows: m.totalMemRowsLocked(), Decision: "spilled",
			Detail: detailf("victim=%d reason=max in-memory rows, tie->min txn id; block=%d rows=%d",
				victim, id, len(packed))})
	}

	spilled = len(plan) > 0
	m.logLocked(Event{Op: "append", TxnID: txnID, InputRows: len(rows),
		MemRows: m.totalMemRowsLocked(), Decision: detailf("accepted; spilled=%t", spilled),
		Detail: detailf("mem before=%d after=%d limit=%d blocks=%d",
			before, m.totalMemRowsLocked(), m.memLimit, len(m.blocks))})
	return spilled, blockIDs, nil
}

// Commit 提交事务：按块号升序回放全部溢写块，再输出内存行，
// 随后删除其全部块；返回的行序与追加顺序完全一致。
func (m *Manager) Commit(txnID uint64) ([]Row, error) {
	if txnID == 0 {
		return nil, newError("commit", ReasonInvalidArgument, "transaction id must be > 0")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.txns[txnID]
	if !ok {
		return nil, newError("commit", ReasonTxnNotFound, "no open transaction with that id")
	}

	blockIDs := append([]uint64(nil), t.blockIDs...)
	sort.Slice(blockIDs, func(i, j int) bool { return blockIDs[i] < blockIDs[j] })
	nRows := len(t.memRows)
	for _, bid := range blockIDs {
		nRows += len(m.blocks[bid])
	}
	out := make([]Row, 0, nRows)
	for _, bid := range blockIDs {
		out = append(out, m.blocks[bid]...)
		delete(m.blocks, bid)
	}
	out = append(out, t.memRows...)
	delete(m.txns, txnID)
	m.committed = append(m.committed, out...)

	m.logLocked(Event{Op: "commit", TxnID: txnID, MemRows: 0, Decision: "committed",
		Detail: detailf("replayed blocks=%d in ascending id then mem rows; output rows=%d; blocks deleted",
			len(blockIDs), len(out))})
	return out, nil
}

// Rollback 回滚事务：丢弃内存行并删除全部溢写块，不输出任何行。
func (m *Manager) Rollback(txnID uint64) error {
	if txnID == 0 {
		return newError("rollback", ReasonInvalidArgument, "transaction id must be > 0")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.txns[txnID]
	if !ok {
		return newError("rollback", ReasonTxnNotFound, "no open transaction with that id")
	}
	for _, bid := range t.blockIDs {
		delete(m.blocks, bid)
	}
	deleted := len(t.blockIDs)
	delete(m.txns, txnID)
	m.logLocked(Event{Op: "rollback", TxnID: txnID, MemRows: m.totalMemRowsLocked(),
		Decision: "rolled back",
		Detail:   detailf("dropped mem rows and deleted blocks=%d; no output", deleted)})
	return nil
}

// CommittedLog 返回已提交下游日志的快照，可与追加/提交并发调用。
func (m *Manager) CommittedLog() []Row {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Row, len(m.committed))
	copy(out, m.committed)
	return out
}

// BlocksView 是溢写块的快照：块号 -> 块内行（保持追加顺序）。
type BlocksView map[uint64][]Row

// Blocks 返回溢写块快照，可并发调用。
func (m *Manager) Blocks() BlocksView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(BlocksView, len(m.blocks))
	for id, rows := range m.blocks {
		cp := make([]Row, len(rows))
		copy(cp, rows)
		out[id] = cp
	}
	return out
}

// Stats 是自检所需的统计快照。
type Stats struct {
	MemRows       int
	BlockCount    int
	OpenTxnCount  int
	NextBlock     uint64
	CommittedRows int
}

// Stats 返回当前统计信息，可并发调用。
func (m *Manager) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Stats{
		MemRows:       m.totalMemRowsLocked(),
		BlockCount:    len(m.blocks),
		OpenTxnCount:  len(m.txns),
		NextBlock:     m.nextBlock,
		CommittedRows: len(m.committed),
	}
}

// CheckInvariants 自检：任意时刻内存行数不超过上限；每个溢写块都属于
// 某个未结束事务且只属于一个；事务引用的块都存在。
func (m *Manager) CheckInvariants() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	total := 0
	owned := make(map[uint64]bool)
	for id, t := range m.txns {
		total += len(t.memRows)
		for _, bid := range t.blockIDs {
			if _, ok := m.blocks[bid]; !ok {
				return newError("check", ReasonInvalidArgument,
					detailf("txn %d references missing block %d", id, bid))
			}
			if owned[bid] {
				return newError("check", ReasonInvalidArgument,
					detailf("block %d owned by multiple transactions", bid))
			}
			owned[bid] = true
		}
	}
	if total > m.memLimit {
		return newError("check", ReasonInvalidArgument,
			detailf("mem rows %d exceed limit %d", total, m.memLimit))
	}
	for bid := range m.blocks {
		if !owned[bid] {
			return newError("check", ReasonInvalidArgument,
				detailf("block %d does not belong to any open transaction", bid))
		}
	}
	return nil
}

func (m *Manager) totalMemRowsLocked() int {
	total := 0
	for _, t := range m.txns {
		total += len(t.memRows)
	}
	return total
}

func (m *Manager) logLocked(e Event) {
	if m.logger != nil {
		m.logger.Log(e)
	}
}

func detailf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
