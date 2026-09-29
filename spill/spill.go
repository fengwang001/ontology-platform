// Package spill 实现大事务的内存缓冲溢写与按序回放。
package spill

// Row 是事务追加的一行数据。
type Row = string

// ErrorCode 描述一次操作被拒绝的可区分原因。
type ErrorCode int

const (
	OK                 ErrorCode = iota
	ErrInvalidParam              // 非法参数（nil/空行、nil 事务、非正上限等）
	ErrTxnNotFound               // 事务号不存在（已结束或从未开始）
	ErrTxnDuplicate              // 事务号与一个未结束事务重复
	ErrSpillFull                 // 溢写块数已达上限，且仍需继续溢写
	ErrBlockNotFound             // 查询的溢写块不存在
)

// BlockInfo 是溢写块的查询视图。
type BlockInfo struct {
	TxnID   uint64 // 所属事务号
	Seq     uint64 // 该事务内的追加序号（从 1 开始）
	BlockID uint64 // 全局块号（单调递增、不复用）
	Rows    []Row  // 块内行（快照，顺序与追加顺序一致）
}

// Stats 是管理器自检/观测快照。
type Stats struct {
	ActiveTxns     int    // 未结束事务数
	MemoryRows     int    // 当前全部未结束事务的内存行数之和
	SpillBlocks    int    // 当前存活溢写块数
	MemoryLimit    int    // 内存行数上限
	MaxSpillBlocks int    // 溢写块数上限
	NextBlockID    uint64 // 下一个将分配的全局块号
	NextSeq        uint64 // 下一个将分配的追加序号
	LogRows        int    // 已提交并输出到下游日志的总行数
}

// Manager 管理多个未结束事务的内存缓冲、溢写块与提交日志。
type Manager struct {
	memoryLimit    int
	maxSpillBlocks int
	logger         func(string)

	_ struct{}
}

// Config 构造 Manager 的参数。
type Config struct {
	MemoryLimit   int          // 任意时刻内存行数之和上限，必须 >= 1
	MaxSpillBlocks int         // 存活溢ill块数上限，必须 >= 1
	Logger        func(string) // 可选：接收每步输入、内存行数与判定依据
}

// NewManager 创建管理器骨架。
func NewManager(cfg Config) *Manager {
	return &Manager{}
}

// Begin 开启事务骨架。
func (m *Manager) Begin(txnID uint64) error { return nil }

// Append 追加行骨架。
func (m *Manager) Append(txnID uint64, rows []Row) error { return nil }

// Commit 提交事务，按追加顺序返回事务全部行。
func (m *Manager) Commit(txnID uint64) ([]Row, error) { return nil, nil }

// Rollback 回滚事务，丢弃内存行并删除全部溢写块。
func (m *Manager) Rollback(txnID uint64) error { return nil }

// CommittedLog 返回已提交事务输出日志的快照。
func (m *Manager) CommittedLog() []Row { return nil }

// Block 按全局块号查询溢写块。
func (m *Manager) Block(blockID uint64) (BlockInfo, bool) { return BlockInfo{}, false }

// BlocksOf 查询事务当前全部溢写块，按块号升序。
func (m *Manager) BlocksOf(txnID uint64) []BlockInfo { return nil }

// Snapshot 返回观测快照。
func (m *Manager) Snapshot() Stats { return Stats{} }

// Check 执行内部不变量自检，返回发现的第一个问题；nil 表示一致。
func (m *Manager) Check() error { return nil }
