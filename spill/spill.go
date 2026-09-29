package spill

// Manager 管理大事务的内存缓冲、溢写、按序回放提交与回滚。
type Manager struct{}

// Option 配置 Manager。
type Option func(*config)

type config struct{}

// New 创建一个 Manager。memLimit 为内存行数上限（>0），
// blockLimit 为溢写块数上限（>0）。
func New(memLimit, blockLimit int, opts ...Option) (*Manager, error) {
	return nil, nil
}

// Begin 开启事务。
func (m *Manager) Begin(txID uint64) error { return nil }

// Append 向事务追加行；超限时触发溢写，块数达上限则整体拒绝。
func (m *Manager) Append(txID uint64, rows []Row) error { return nil }

// Commit 提交事务：按块号升序回放溢写块，再输出内存行。
func (m *Manager) Commit(txID uint64) error { return nil }

// Rollback 回滚事务：丢弃内存行并删除全部溢写块，不输出。
func (m *Manager) Rollback(txID uint64) error { return nil }

// Blocks 返回给定事务当前全部溢写块的快照（按块号升序）。
func (m *Manager) Blocks(txID uint64) ([]Block, error) { return nil, nil }

// Log 返回下游日志快照。
func (m *Manager) Log() []LogEntry { return nil }

// Stats 是自检/观测快照。
type Stats struct {
	OpenTx           int
	MemRows          int
	Blocks           int
	NextBlockID      uint64
	CommittedTx      uint64
	RolledBackTx     uint64
	RejectedAppends  uint64
}

// Stats 返回当前统计快照。
func (m *Manager) Stats() Stats { return Stats{} }

// Check 执行不变量自检，违例时返回描述错误。
func (m *Manager) Check() error { return nil }
