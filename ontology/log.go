package ontology

import "sync"

// DefaultMaxActiveExports 允许同时处于未结束状态的导出会话上限。
const DefaultMaxActiveExports = 8

// Record 是日志中的一条记录。序号在 Append 时连续递增分配；
// 不携带时间戳等非确定性字段，保证导出结果可复现、可逐字段比较。
type Record struct {
	Seq   int64
	Key   string
	Value string
}

// Log 是只追加记录日志。序号 1..lastSeq 连续无空洞。
type Log struct {
	mu               sync.Mutex
	records          []Record
	sealed           bool
	active           map[*ExportSession]struct{}
	maxActiveExports int
}

// NewLog 创建空日志。
func NewLog() *Log {
	return &Log{
		active:           make(map[*ExportSession]struct{}),
		maxActiveExports: DefaultMaxActiveExports,
	}
}

// SetMaxActiveExports 调整允许并发未结束导出会话数；<=0 表示不加限制。
// 必须在任何导出开始前调用。
func (l *Log) SetMaxActiveExports(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maxActiveExports = n
}

// Append 校验通过后才分配连续序号并追加。
// 键为空或日志已密封时整体拒绝：不分配序号、不改变日志。
func (l *Log) Append(key, value string) (Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if key == "" {
		return Record{}, ErrEmptyKey
	}
	if l.sealed {
		return Record{}, ErrLogSealed
	}
	rec := Record{Seq: int64(len(l.records)) + 1, Key: key, Value: value}
	l.records = append(l.records, rec)
	return rec, nil
}

// At 返回指定序号的记录；记录尚不存在（序号超过当前末端）时 ok 为 false。
func (l *Log) At(seq int64) (Record, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq < 1 || seq > int64(len(l.records)) {
		return Record{}, false
	}
	return l.records[seq-1], true
}

// LastSeq 返回当前已分配的最大序号；空日志返回 0。
func (l *Log) LastSeq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(len(l.records))
}

// IsSealed 报告日志是否已结束写入。
func (l *Log) IsSealed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sealed
}

// Seal 结束日志：密封后 Append 一律被拒绝。可重复调用，结果幂等。
func (l *Log) Seal() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sealed = true
}

// startExport 在互斥区内完成导出开始的全部判定与登记，
// 任何失败都不登记会话、不改变日志。
func (l *Log) startExport(s *ExportSession) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.maxActiveExports > 0 && len(l.active) >= l.maxActiveExports {
		return 0, ErrExportLimitExceeded
	}
	from := int64(len(l.records))
	l.active[s] = struct{}{}
	return from, nil
}

// finishExport 注销一个已结束的导出会话。
func (l *Log) finishExport(s *ExportSession) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.active, s)
}
