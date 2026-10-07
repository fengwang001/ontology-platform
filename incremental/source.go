package incremental

import "sync"

// Cursor 是源写日志中的单调递增位点。
type Cursor uint64

// GenesisCursor 是任何链路尚未确认任何周期时的起始位点。
const GenesisCursor Cursor = 0

// Write 是本体数据源中的一条写入。同一写入因重试被多次提交时 ID 相同。
type Write struct {
	Cursor  Cursor
	ID      string
	Payload string
}

// Source 是本体数据源的写日志抽象。实现必须保证位点单调且扫描结果确定。
type Source interface {
	// Scan 返回位点落在 (from, to] 区间内的全部写入，按位点升序。
	Scan(from, to Cursor) ([]Write, error)
	// Head 返回当前写日志的最大位点。
	Head() (Cursor, error)
}

// MemSource 是 Source 的内存实现，追加式写日志，位点即序号（从 1 开始）。
type MemSource struct {
	mu     sync.RWMutex
	writes []Write
}

// NewMemSource 创建空的内存写日志。
func NewMemSource() *MemSource { return &MemSource{} }

// Append 追加一条写入并返回其位点。同一 ID 可因重试被追加多次，
// 在日志中表现为多条记录（导出组件负责去重）。
func (s *MemSource) Append(w Write) Cursor {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Cursor = Cursor(len(s.writes) + 1)
	s.writes = append(s.writes, w)
	return w.Cursor
}

// Scan 实现 Source。
func (s *MemSource) Scan(from, to Cursor) ([]Write, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Write
	for _, w := range s.writes {
		if w.Cursor > from && w.Cursor <= to {
			out = append(out, w)
		}
	}
	return out, nil
}

// Head 实现 Source。
func (s *MemSource) Head() (Cursor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Cursor(len(s.writes)), nil
}
