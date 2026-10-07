package incremental

import (
	"errors"
	"sync"
)

// ErrCursorCorrupt 由 CursorStore.Load 返回，表示位点记录介质损坏、
// 当前无法读出可信的已确认结束位点。
var ErrCursorCorrupt = errors.New("incremental: cursor record unreadable")

// CursorStore 负责每条导出链路“上一次已确认结束位点”的持久化。
// 它只是历史台账（Ledger）的缓存：真正的事实来源是 Ledger 中连续
// 的已确认增量记录，因此 CursorStore 损坏时可以从 Ledger 安全推导。
type CursorStore interface {
	// Load 读取链路最近一次确认的结束位点；介质损坏时返回
	// 包装了 ErrCursorCorrupt 的错误。
	Load(chain string) (Cursor, error)
	// Store 确认并持久化链路的结束位点。
	Store(chain string, c Cursor) error
}

// MemCursorStore 是 CursorStore 的内存实现，支持故障注入以模拟
// 位点记录介质损坏。
type MemCursorStore struct {
	mu        sync.RWMutex
	cursors   map[string]Cursor
	corrupted map[string]bool
}

// NewMemCursorStore 创建空的内存位点存储。
func NewMemCursorStore() *MemCursorStore {
	return &MemCursorStore{
		cursors:   make(map[string]Cursor),
		corrupted: make(map[string]bool),
	}
}

// Load 实现 CursorStore。未记录的链路返回 GenesisCursor。
func (s *MemCursorStore) Load(chain string) (Cursor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.corrupted[chain] {
		return 0, ErrCursorCorrupt
	}
	return s.cursors[chain], nil
}

// Store 实现 CursorStore。
func (s *MemCursorStore) Store(chain string, c Cursor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursors[chain] = c
	return nil
}

// Corrupt 注入故障：使链路的位点记录不可读（模拟介质损坏）。
func (s *MemCursorStore) Corrupt(chain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.corrupted[chain] = true
}

// Repair 修复故障：位点记录恢复可读（内容保持损坏前最后一次写入值，
// 用于模拟介质修复后数据仍在的场景）。
func (s *MemCursorStore) Repair(chain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.corrupted, chain)
}
