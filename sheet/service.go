package sheet

import (
	"errors"
	"fmt"
	"sync"
)

// cellState 是单元格的内部表示。被清除的单元格保留条目以维持版本号。
type cellState struct {
	value   int64
	empty   bool
	version int64
}

// Service 是协同表格的撤销/重做服务。
// 所有操作在内部互斥锁下串行化，并发调用等价于某个串行顺序。
type Service struct {
	mu        sync.Mutex
	revision  int64
	lastNow   int64
	depths    map[string]int
	cells     map[string]cellState
	protector map[string]string
	hist      map[string]*userHistory
}

// New 以服务创建时给定的每用户历史深度 D 构造服务。
func New(depths map[string]int) (*Service, error) {
	if len(depths) == 0 {
		return nil, errors.New("sheet: 至少需要一个用户")
	}
	s := &Service{
		depths:    make(map[string]int, len(depths)),
		cells:     make(map[string]cellState),
		protector: make(map[string]string),
		hist:      make(map[string]*userHistory, len(depths)),
	}
	for user, d := range depths {
		if user == "" {
			return nil, errors.New("sheet: 用户名不能为空")
		}
		if d < MinDepth || d > MaxDepth {
			return nil, fmt.Errorf("sheet: 用户 %q 的历史深度 %d 超出 [%d, %d]", user, d, MinDepth, MaxDepth)
		}
		s.depths[user] = d
		s.hist[user] = &userHistory{}
	}
	return s, nil
}

// Apply 将一批编辑作为一个事务生效。见包文档与设计的拒绝次序：
// 参数非法 > 时钟回退 > 受保护 > 无变化。
func (s *Service) Apply(user string, edits []Edit, now int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.apply(user, edits, now)
}

// Undo 撤销该用户撤销栈栈顶记录。
// 拒绝次序：参数非法 > 时钟回退 > 栈空 > 被覆盖 > 受保护。
func (s *Service) Undo(user string, now int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.undo(user, now)
}

// Redo 重做该用户重做栈栈顶记录，规则与 Undo 对称。
func (s *Service) Redo(user string, now int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.redo(user, now)
}

// Protect 使 cell 只有 owner 可写。
func (s *Service) Protect(owner, cell string, now int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.protect(owner, cell, now)
}

// Unprotect 解除保护，只有保护者可做。
func (s *Service) Unprotect(user, cell string, now int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unprotect(user, cell, now)
}

// Cell 返回单元格的值与版本；从未写入的单元格版本为 0 且为空。
func (s *Service) Cell(key string) CellState {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cells[key]
	if !ok {
		return CellState{Empty: true}
	}
	return CellState{Value: c.value, Empty: c.empty, Version: c.version}
}

// History 返回该用户撤销栈与重做栈的条数及栈顶记录的键集合。
func (s *Service) History(user string) (HistoryInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hist[user]
	if !ok {
		return HistoryInfo{}, false
	}
	info := HistoryInfo{UndoCount: len(h.undo), RedoCount: len(h.redo)}
	if top, ok := h.peekUndo(); ok {
		info.UndoTop = top.keys()
	}
	if top, ok := h.peekRedo(); ok {
		info.RedoTop = top.keys()
	}
	return info, true
}

// Revision 返回当前全局修订号。
func (s *Service) Revision() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision
}

// Protector 返回 cell 的保护者；未保护时 ok 为 false。
func (s *Service) Protector(cell string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owner, ok := s.protector[cell]
	return owner, ok
}
