package ontology

import "sync/atomic"

// Logger 记录每一步输入、合并结果与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// Table 是支持并发行查询、自检与串行提交的只增改表。
type Table struct {
	columns []string
	snap    atomic.Pointer[snapshot]
	logger  Logger
}

type snapshot struct {
	rows map[string]map[string]ColumnValue
	gen  uint64
}

// NewTable 创建一张列集合固定的空表。
func NewTable(columns []string, logger Logger) *Table {
	_ = columns
	_ = logger
	return nil
}

// Apply 原子地校验并提交一批事件，返回合并输出。
func (t *Table) Apply(events []Event) ([]Outcome, error) {
	_ = events
	return nil, nil
}

// Get 返回某键当前行的深拷贝；键不存在时 ok 为 false。
func (t *Table) Get(key string) (map[string]ColumnValue, bool) {
	_ = key
	return nil, false
}

// Snapshot 返回当前全表的深拷贝。
func (t *Table) Snapshot() map[string]map[string]ColumnValue {
	return nil
}

// CheckInvariants 执行自检：每行列集合与表列集合一致、值不允许缺席。
func (t *Table) CheckInvariants() error {
	return nil
}
