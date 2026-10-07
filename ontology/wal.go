package ontology

import "sync"

// TxnState 表示一个处理单元（事务）在 WAL 中的状态。
type TxnState int

const (
	// TxnPending 已记录 undo 信息但尚未提交；恢复时必须回滚。
	TxnPending TxnState = iota
	// TxnCommitted 已写入提交记录；恢复时必须前滚（幂等重放）。
	TxnCommitted
)

// UndoEntry 记录单个实例-属性在写入前的完整状态，
// 恢复时据此同时回退属性值与所有相关索引条目，不允许只回退一边。
type UndoEntry struct {
	InstanceID string
	PropertyID string
	OldValue   Value
	// OldKeys[i] 是 OldValue 在第 i 个索引结构中的键（与属性的索引结构声明顺序一致）。
	OldKeys []Key
	// NewValue/NewKeys 用于提交后的幂等前滚。
	NewValue Value
	NewKeys  []Key
}

// TxnRecord 是 WAL 中一个处理单元的记录。
type TxnRecord struct {
	TxnID uint64
	Kind  string // "write" | "batch" | "delete"
	Undo  []UndoEntry
	State TxnState
	// Clock 是提交时赋予的逻辑时钟戳，恢复前滚时据此恢复时钟。
	Clock uint64
}

// WAL 是先写日志。恢复开销只取决于未完成事务数，
// 不随索引中已有条目总数增长。
type WAL interface {
	// Begin 追加一条 pending 记录并返回事务号。
	Begin(kind string, undo []UndoEntry) uint64
	// Commit 将事务标记为已提交，并记录提交时钟戳。
	Commit(txnID uint64, clock uint64)
	// End 事务收尾（回滚或前滚完成后）移除记录，保持 Pending 有界。
	End(txnID uint64)
	// Pending 返回所有未完成事务（恢复时只需扫描这些记录）。
	Pending() []*TxnRecord
}

// MemoryWAL 是 WAL 的内存实现，用于崩溃模拟与测试。
type MemoryWAL struct {
	mu      sync.Mutex
	nextID  uint64
	records map[uint64]*TxnRecord
}

func NewMemoryWAL() *MemoryWAL {
	return &MemoryWAL{records: make(map[uint64]*TxnRecord)}
}

func (w *MemoryWAL) Begin(kind string, undo []UndoEntry) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.nextID++
	id := w.nextID
	cp := make([]UndoEntry, len(undo))
	copy(cp, undo)
	w.records[id] = &TxnRecord{TxnID: id, Kind: kind, Undo: cp, State: TxnPending}
	return id
}

func (w *MemoryWAL) Commit(txnID uint64, clock uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if rec, ok := w.records[txnID]; ok {
		rec.State = TxnCommitted
		rec.Clock = clock
	}
}

func (w *MemoryWAL) End(txnID uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.records, txnID)
}

func (w *MemoryWAL) Pending() []*TxnRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*TxnRecord, 0, len(w.records))
	for _, rec := range w.records {
		out = append(out, rec)
	}
	return out
}
