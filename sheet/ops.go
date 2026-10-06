package sheet

import "sort"

// 本文件放置各操作的内部实现（调用方已持有锁）。
// 所有操作只访问事务所涉单元格与操作用户的栈，
// 开销与表格总规模、全体历史规模、用户总数无关。

// applyLocked 实现 Apply。
// 拒绝次序：参数非法 > 时钟回退 > 受保护 > 无变化。
func (s *Service) applyLocked(user string, edits []Edit, now int64) Result {
	u, ok := s.users[user]
	if !ok || user == "" || !validateEdits(edits) || !validNow(now) {
		return Result{Code: ErrInvalidParam}
	}
	if !s.clockOK(now) {
		return Result{Code: ErrClockRollback}
	}
	// 保护检查覆盖全部编辑（含将被判无效的编辑），
	// 任一编辑触及他人保护的单元格则整批被拒。
	keys := make([]string, 0, len(edits))
	for _, e := range edits {
		keys = append(keys, e.Key)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if owner, ok := s.protections[k]; ok && owner != user {
			return Result{Code: ErrProtected, Cell: k}
		}
	}
	// 剔除无效编辑：写入值与当前值相同，或对空单元格清除。
	valid := make([]Edit, 0, len(edits))
	for _, e := range edits {
		st := s.cells[e.Key]
		if e.Clear {
			if st == nil || !st.set {
				continue // 空单元格清除，无效
			}
		} else if st != nil && st.set && st.value == e.Value {
			continue // 写入值与当前值相同，无效
		}
		valid = append(valid, e)
	}
	if len(valid) == 0 {
		return Result{Code: ErrNoChange}
	}
	// 事务整体生效。
	s.revision++
	rev := s.revision
	rec := txnRecord{version: rev, changes: make([]cellChange, 0, len(valid))}
	for _, e := range valid {
		st := s.cells[e.Key]
		ch := cellChange{key: e.Key, newValue: e.Value, newSet: !e.Clear}
		if st != nil {
			ch.oldValue, ch.oldSet = st.value, st.set
		}
		s.cells[e.Key] = &cellState{value: e.Value, set: !e.Clear, version: rev}
		rec.changes = append(rec.changes, ch)
	}
	// rec.changes 的顺序与 valid 一致；按键排序保证“第一个”确定。
	sort.Slice(rec.changes, func(i, j int) bool { return rec.changes[i].key < rec.changes[j].key })
	u.undo.push(rec)
	u.undo.trimOldest(u.depth)
	u.redo.clear() // 仅清空自己的重做栈，他人不受影响
	s.acceptClock(now)
	return Result{Code: OK, Revision: rev}
}

// undoLocked 实现 Undo。
// 拒绝次序：参数非法 > 时钟回退 > 栈空 > 被覆盖 > 受保护。
func (s *Service) undoLocked(user string, now int64) Result {
	u, ok := s.users[user]
	if !ok || user == "" || !validNow(now) {
		return Result{Code: ErrInvalidParam}
	}
	if !s.clockOK(now) {
		return Result{Code: ErrClockRollback}
	}
	rec, ok := u.undo.top()
	if !ok {
		return Result{Code: ErrEmptyStack}
	}
	// 版本检查：每个单元格当前版本须恰等于记录版本。
	// 当前值与原值相同但版本不同同样视为被覆盖。
	for _, ch := range rec.changes {
		if s.versionOf(ch.key) != rec.version {
			u.undo.pop() // 被覆盖例外：弹出并丢弃，不进入重做栈
			return Result{Code: ErrOverwritten, Cell: ch.key, Popped: true}
		}
	}
	// 保护检查：任一单元格被他人保护则整体拒绝，两栈不变。
	for _, ch := range rec.changes {
		if owner, ok := s.protections[ch.key]; ok && owner != user {
			return Result{Code: ErrProtected, Cell: ch.key}
		}
	}
	// 整体恢复原值（原值为空即清除）。
	s.revision++
	rev := s.revision
	for _, ch := range rec.changes {
		s.cells[ch.key] = &cellState{value: ch.oldValue, set: ch.oldSet, version: rev}
	}
	u.undo.pop()
	rec.version = rev // 以新修订号作为期望版本压入重做栈
	u.redo.push(rec)
	s.acceptClock(now)
	return Result{Code: OK, Revision: rev}
}

// redoLocked 实现 Redo，规则与 Undo 对称。
func (s *Service) redoLocked(user string, now int64) Result {
	u, ok := s.users[user]
	if !ok || user == "" || !validNow(now) {
		return Result{Code: ErrInvalidParam}
	}
	if !s.clockOK(now) {
		return Result{Code: ErrClockRollback}
	}
	rec, ok := u.redo.top()
	if !ok {
		return Result{Code: ErrEmptyStack}
	}
	for _, ch := range rec.changes {
		if s.versionOf(ch.key) != rec.version {
			u.redo.pop() // 被覆盖例外：弹出并丢弃
			return Result{Code: ErrOverwritten, Cell: ch.key, Popped: true}
		}
	}
	for _, ch := range rec.changes {
		if owner, ok := s.protections[ch.key]; ok && owner != user {
			return Result{Code: ErrProtected, Cell: ch.key}
		}
	}
	// 重新写入撤销前的新值。
	s.revision++
	rev := s.revision
	for _, ch := range rec.changes {
		s.cells[ch.key] = &cellState{value: ch.newValue, set: ch.newSet, version: rev}
	}
	u.redo.pop()
	rec.version = rev // 以新修订号为期望版本回到撤销栈顶
	u.undo.push(rec)
	u.undo.trimOldest(u.depth)
	s.acceptClock(now)
	return Result{Code: OK, Revision: rev}
}

// protectLocked 实现 Protect。已被自己保护视为成功且无变化。
func (s *Service) protectLocked(owner, cell string, now int64) Result {
	if _, ok := s.users[owner]; !ok || owner == "" || cell == "" || !validNow(now) {
		return Result{Code: ErrInvalidParam}
	}
	if !s.clockOK(now) {
		return Result{Code: ErrClockRollback}
	}
	if cur, ok := s.protections[cell]; ok {
		if cur != owner {
			return Result{Code: ErrProtected, Cell: cell}
		}
		s.acceptClock(now) // 自己重复保护：成功、无变化
		return Result{Code: OK}
	}
	s.protections[cell] = owner
	s.acceptClock(now)
	return Result{Code: OK}
}

// unprotectLocked 实现 Unprotect。仅保护者可解除；
// 单元格未被保护时视为成功且无变化（幂等）。
func (s *Service) unprotectLocked(user, cell string, now int64) Result {
	if _, ok := s.users[user]; !ok || user == "" || cell == "" || !validNow(now) {
		return Result{Code: ErrInvalidParam}
	}
	if !s.clockOK(now) {
		return Result{Code: ErrClockRollback}
	}
	if cur, ok := s.protections[cell]; ok {
		if cur != user {
			return Result{Code: ErrProtected, Cell: cell}
		}
		delete(s.protections, cell)
	}
	s.acceptClock(now)
	return Result{Code: OK}
}

// versionOf 返回单元格当前版本，从未写入为 0。
func (s *Service) versionOf(key string) int64 {
	if st, ok := s.cells[key]; ok {
		return st.version
	}
	return 0
}
