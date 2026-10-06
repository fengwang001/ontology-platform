package sheet

import "sync"

// cellState 是单个单元格的内部状态。
type cellState struct {
	value   int64
	set     bool
	version int64
}

// userState 是单个用户的内部状态。
type userState struct {
	depth int
	undo  recordStack
	redo  recordStack
}

// Service 是协同表格撤销/重做服务。所有方法可并发调用，
// 内部以互斥锁串行化，结果等价于某个串行顺序。
type Service struct {
	mu          sync.Mutex
	cells       map[string]*cellState
	protections map[string]string // 单元格键 -> 保护者
	users       map[string]*userState
	revision    int64 // 全局修订号，从 0 开始
	lastNow     int64 // 上一次被接受操作的 now
	clockSet    bool  // 是否已有被接受的操作
}

// NewService 以服务创建时给定的每用户历史深度构造服务。
// 任一用户名为空或深度不在 [MinDepth, MaxDepth] 时返回错误。
func NewService(depths map[string]int) (*Service, error) {
	s := &Service{
		cells:       make(map[string]*cellState),
		protections: make(map[string]string),
		users:       make(map[string]*userState, len(depths)),
	}
	for name, d := range depths {
		if name == "" || d < MinDepth || d > MaxDepth {
			return nil, ErrInvalidParamError{Reason: "invalid user or depth"}
		}
		s.users[name] = &userState{depth: d}
	}
	return s, nil
}

// ErrInvalidParamError 表示服务创建参数非法。
type ErrInvalidParamError struct{ Reason string }

func (e ErrInvalidParamError) Error() string { return "sheet: invalid parameter: " + e.Reason }

// Apply 将一批编辑作为一个事务生效。
func (s *Service) Apply(user string, edits []Edit, now int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyLocked(user, edits, now)
}

// Undo 撤销该用户撤销栈顶的事务。
func (s *Service) Undo(user string, now int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.undoLocked(user, now)
}

// Redo 重做该用户重做栈顶的事务。
func (s *Service) Redo(user string, now int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.redoLocked(user, now)
}

// Protect 使单元格只能被 owner 写入。
func (s *Service) Protect(owner, cell string, now int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.protectLocked(owner, cell, now)
}

// Unprotect 解除单元格保护，仅保护者可做。
func (s *Service) Unprotect(user, cell string, now int64) Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unprotectLocked(user, cell, now)
}

// Cell 查询单元格的值与版本。第二个返回值表示参数是否合法。
func (s *Service) Cell(key string) (CellInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		return CellInfo{}, false
	}
	if st, ok := s.cells[key]; ok {
		return CellInfo{Value: st.value, Set: st.set, Version: st.version}, true
	}
	return CellInfo{Version: 0}, true
}

// History 查询用户撤销栈与重做栈的规模及栈顶键集合。
// 第二个返回值表示参数是否合法。
func (s *Service) History(user string) (HistoryInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[user]
	if !ok || user == "" {
		return HistoryInfo{}, false
	}
	info := HistoryInfo{
		UndoCount: u.undo.len(),
		RedoCount: u.redo.len(),
	}
	if top, ok := u.undo.top(); ok {
		info.UndoTop = top.keys()
	}
	if top, ok := u.redo.top(); ok {
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
