package sheet

import "sort"

// apply 实现 Apply。调用方须已持有锁。
func (s *Service) apply(user string, edits []Edit, now int64) Result {
	depth, ok := s.depths[user]
	if !ok || len(edits) == 0 || len(edits) > MaxEdits || !validNow(now) {
		return reject(CodeInvalidParam, s.revision)
	}
	seen := make(map[string]struct{}, len(edits))
	byKey := make(map[string]Edit, len(edits))
	for _, e := range edits {
		if e.Key == "" {
			return reject(CodeInvalidParam, s.revision)
		}
		if _, dup := seen[e.Key]; dup {
			return reject(CodeInvalidParam, s.revision)
		}
		if !e.Clear && !validValue(e.Value) {
			return reject(CodeInvalidParam, s.revision)
		}
		seen[e.Key] = struct{}{}
		byKey[e.Key] = e
	}
	if now < s.lastNow {
		return reject(CodeClockRollback, s.revision)
	}
	// 保护检查覆盖全部编辑键（含无效编辑），按键升序报第一个冲突。
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if owner, ok := s.protector[k]; ok && owner != user {
			return rejectCell(CodeProtected, k, s.revision)
		}
	}
	// 剔除无效编辑：写入值与当前值相同，或对空单元格清除。
	type validEdit struct {
		edit Edit
		old  cellState
	}
	valid := make([]validEdit, 0, len(edits))
	for _, k := range keys {
		edit := byKey[k]
		old, exists := s.cells[k]
		oldEmpty := !exists || old.empty
		if edit.Clear {
			if oldEmpty {
				continue
			}
		} else if !oldEmpty && old.value == edit.Value {
			continue
		}
		if !exists {
			old = cellState{empty: true}
		}
		valid = append(valid, validEdit{edit: edit, old: old})
	}
	if len(valid) == 0 {
		return reject(CodeNoChange, s.revision)
	}
	s.revision++
	rec := Record{Changes: make([]CellChange, len(valid))}
	for i, ve := range valid {
		newState := cellState{version: s.revision}
		if !ve.edit.Clear {
			newState.value = ve.edit.Value
		} else {
			newState.empty = true
		}
		s.cells[ve.edit.Key] = newState
		rec.Changes[i] = CellChange{
			Key:      ve.edit.Key,
			OldValue: ve.old.value,
			OldEmpty: ve.old.empty,
			NewValue: newState.value,
			NewEmpty: newState.empty,
			Version:  s.revision,
		}
	}
	h := s.hist[user]
	h.pushUndo(rec, depth)
	h.clearRedo()
	s.lastNow = now
	return okResult(s.revision)
}

// checkVersions 校验记录内每个单元格的当前版本与期望版本一致。
// 返回按键排序的第一个不符单元格；全部一致时返回空串与 true。
func (s *Service) checkVersions(rec Record) (string, bool) {
	for _, c := range rec.Changes {
		var current int64
		if st, ok := s.cells[c.Key]; ok {
			current = st.version
		}
		if current != c.Version {
			return c.Key, false
		}
	}
	return "", true
}

// checkProtection 校验记录涉及的单元格未被他人保护。
func (s *Service) checkProtection(user string, rec Record) (string, bool) {
	for _, c := range rec.Changes {
		if owner, ok := s.protector[c.Key]; ok && owner != user {
			return c.Key, false
		}
	}
	return "", true
}

// undo 实现 Undo。调用方须已持有锁。
func (s *Service) undo(user string, now int64) Result {
	if _, ok := s.depths[user]; !ok || !validNow(now) {
		return reject(CodeInvalidParam, s.revision)
	}
	if now < s.lastNow {
		return reject(CodeClockRollback, s.revision)
	}
	h := s.hist[user]
	rec, ok := h.peekUndo()
	if !ok {
		return reject(CodeEmptyStack, s.revision)
	}
	if cell, ok := s.checkVersions(rec); !ok {
		// 被覆盖例外：弹出并丢弃该记录，不进入重做栈。
		h.popUndo()
		return Result{OK: false, Code: CodeOverwritten, Cell: cell, Dropped: true, Revision: s.revision}
	}
	if cell, ok := s.checkProtection(user, rec); !ok {
		return rejectCell(CodeProtected, cell, s.revision)
	}
	s.revision++
	newRec := Record{Changes: make([]CellChange, len(rec.Changes))}
	for i, c := range rec.Changes {
		s.cells[c.Key] = cellState{value: c.OldValue, empty: c.OldEmpty, version: s.revision}
		nc := c
		nc.Version = s.revision
		newRec.Changes[i] = nc
	}
	h.popUndo()
	h.pushRedo(newRec)
	s.lastNow = now
	return okResult(s.revision)
}

// redo 实现 Redo。调用方须已持有锁。
func (s *Service) redo(user string, now int64) Result {
	depth, ok := s.depths[user]
	if !ok || !validNow(now) {
		return reject(CodeInvalidParam, s.revision)
	}
	if now < s.lastNow {
		return reject(CodeClockRollback, s.revision)
	}
	h := s.hist[user]
	rec, ok := h.peekRedo()
	if !ok {
		return reject(CodeEmptyStack, s.revision)
	}
	if cell, ok := s.checkVersions(rec); !ok {
		h.popRedo()
		return Result{OK: false, Code: CodeOverwritten, Cell: cell, Dropped: true, Revision: s.revision}
	}
	if cell, ok := s.checkProtection(user, rec); !ok {
		return rejectCell(CodeProtected, cell, s.revision)
	}
	s.revision++
	newRec := Record{Changes: make([]CellChange, len(rec.Changes))}
	for i, c := range rec.Changes {
		s.cells[c.Key] = cellState{value: c.NewValue, empty: c.NewEmpty, version: s.revision}
		nc := c
		nc.Version = s.revision
		newRec.Changes[i] = nc
	}
	h.popRedo()
	h.pushUndo(newRec, depth)
	s.lastNow = now
	return okResult(s.revision)
}

// protect 实现 Protect。保护与解除不改变修订号、单元格版本与任何历史记录。
func (s *Service) protect(owner, cell string, now int64) Result {
	if _, ok := s.depths[owner]; !ok || cell == "" || !validNow(now) {
		return reject(CodeInvalidParam, s.revision)
	}
	if now < s.lastNow {
		return reject(CodeClockRollback, s.revision)
	}
	if cur, ok := s.protector[cell]; ok {
		if cur != owner {
			return rejectCell(CodeProtected, cell, s.revision)
		}
		// 已被自己保护：视为成功且无变化。
		s.lastNow = now
		return okResult(s.revision)
	}
	s.protector[cell] = owner
	s.lastNow = now
	return okResult(s.revision)
}

// unprotect 实现 Unprotect。解除未保护的单元格视为成功且无变化。
func (s *Service) unprotect(user, cell string, now int64) Result {
	if _, ok := s.depths[user]; !ok || cell == "" || !validNow(now) {
		return reject(CodeInvalidParam, s.revision)
	}
	if now < s.lastNow {
		return reject(CodeClockRollback, s.revision)
	}
	cur, ok := s.protector[cell]
	if !ok {
		s.lastNow = now
		return okResult(s.revision)
	}
	if cur != user {
		return rejectCell(CodeProtected, cell, s.revision)
	}
	delete(s.protector, cell)
	s.lastNow = now
	return okResult(s.revision)
}
