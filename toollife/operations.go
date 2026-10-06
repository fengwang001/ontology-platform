package toollife

// Apply 提交刀具申请（幂等：同一申请编号返回原结果）。
func (s *Service) Apply(reqID, groupID string, estimated uint64) (ApplyResult, error) {
	if reqID == "" {
		return ApplyResult{}, errf(ErrInvalid, "request id is empty")
	}
	if estimated == 0 {
		return ApplyResult{}, errf(ErrInvalid, "estimated consumption must be positive")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	g, err := s.lockGroup(groupID)
	if err != nil {
		return ApplyResult{}, err
	}
	defer s.unlockGroup()

	// 错误次序：参数非法 > 刀组不存在 > 申请冲突。刀组确认存在后再判幂等/冲突。
	if r, ok := s.reqs[reqID]; ok {
		if r.groupID != groupID || r.estimated != estimated {
			return ApplyResult{}, errf(ErrConflict,
				"request %q already submitted with different content", reqID)
		}
		return ApplyResult{ToolID: r.tool.id, Reserved: r.estimated}, nil
	}

	t, err := chooseTool(g, estimated)
	if err != nil {
		// 选刀失败：不登记申请、不改变任何状态。
		return ApplyResult{}, err
	}
	t.reserved += estimated
	s.reqs[reqID] = &requestRecord{
		id:        reqID,
		groupID:   groupID,
		estimated: estimated,
		tool:      t,
		state:     reqOpen,
	}
	return ApplyResult{ToolID: t.id, Reserved: estimated}, nil
}

// Settle 记账：预占转已用，差额释放（幂等）。
func (s *Service) Settle(reqID string, actual uint64) error {
	if reqID == "" {
		return errf(ErrInvalid, "request id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	r, err := s.findRequest(reqID)
	if err != nil {
		return err
	}
	if r.state == reqSettled {
		return nil // 重复记账幂等，不重复累计
	}
	if r.state == reqAborted {
		return errf(ErrState, "request %q already aborted", reqID)
	}

	s.mag.mu.Lock()
	defer s.mag.mu.Unlock()
	g, err := s.mag.lookupGroup(r.groupID)
	if err != nil {
		return err
	}

	t := r.tool
	before := usedPermille(t.used, g.cfg.LifeLimit)

	// 预占转已用：预占整体释放，实际消耗计入已用（可能大于预计，宽松下照常记账）。
	t.reserved -= r.estimated
	t.used += actual
	r.state = reqSettled

	// 状态：达到上限转已耗尽；但已破损/已锁定的刀保持破损/锁定。
	if t.status == StatusAvailable && t.used >= g.cfg.LifeLimit {
		t.status = StatusExhausted
	}
	// 预警：仅由记账引起，首次跨线发一次（换新后重新具备资格）。
	after := usedPermille(t.used, g.cfg.LifeLimit)
	if !t.warned && before < uint64(g.cfg.WarnPermille) && after >= uint64(g.cfg.WarnPermille) {
		t.warned = true
		s.wares = append(s.wares, WarningEvent{
			GroupID:  g.id,
			ToolID:   t.id,
			Permille: after,
		})
	}
	return nil
}

// Abort 中止申请：释放预占，不计消耗（幂等）。
func (s *Service) Abort(reqID string) error {
	if reqID == "" {
		return errf(ErrInvalid, "request id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	r, err := s.findRequest(reqID)
	if err != nil {
		return err
	}
	if r.state == reqAborted {
		return nil
	}
	if r.state == reqSettled {
		return errf(ErrState, "request %q already settled", reqID)
	}

	s.mag.mu.Lock()
	defer s.mag.mu.Unlock()
	r.tool.reserved -= r.estimated
	r.state = reqAborted
	return nil
}

// ReportBroken 上报刀具破损。
func (s *Service) ReportBroken(groupID, toolID string) error {
	if groupID == "" || toolID == "" {
		return errf(ErrInvalid, "group/tool id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	g, err := s.lockGroup(groupID)
	if err != nil {
		return err
	}
	defer s.unlockGroup()
	t, err := g.tool(toolID)
	if err != nil {
		return err
	}
	switch t.status {
	case StatusBroken:
		return nil // 重复上报幂等
	case StatusAvailable, StatusLocked:
		// 未结算预占保持待结算，之后不再被选中。
		t.status = StatusBroken
		return nil
	default:
		return errf(ErrState, "exhausted tool %q cannot be marked broken", toolID)
	}
}

// Replace 用新刀替换破损刀，占据原顺序位置。
func (s *Service) Replace(groupID, toolID, newToolID string) error {
	if groupID == "" || toolID == "" || newToolID == "" {
		return errf(ErrInvalid, "group/tool id is empty")
	}
	if toolID == newToolID {
		return errf(ErrInvalid, "new tool id must differ from old tool id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	g, err := s.lockGroup(groupID)
	if err != nil {
		return err
	}
	defer s.unlockGroup()
	t, err := g.tool(toolID)
	if err != nil {
		return err
	}
	if t.status != StatusBroken {
		return errf(ErrState, "only broken tool can be replaced, %q is %s", toolID, t.status)
	}
	if t.reserved != 0 {
		return errf(ErrState, "cannot replace %q with unsettled reservations", toolID)
	}
	if _, dup := g.byID[newToolID]; dup {
		return errf(ErrInvalid, "tool %q already exists in group %q", newToolID, groupID)
	}
	nt := &Tool{id: newToolID, status: StatusAvailable}
	for i, cur := range g.order {
		if cur == t {
			g.order[i] = nt
			break
		}
	}
	delete(g.byID, toolID)
	g.byID[newToolID] = nt
	return nil
}

// Lock 锁定刀具。
func (s *Service) Lock(groupID, toolID string) error {
	if groupID == "" || toolID == "" {
		return errf(ErrInvalid, "group/tool id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	g, err := s.lockGroup(groupID)
	if err != nil {
		return err
	}
	defer s.unlockGroup()
	t, err := g.tool(toolID)
	if err != nil {
		return err
	}
	switch t.status {
	case StatusLocked:
		return nil // 重复锁定幂等
	case StatusAvailable:
		t.status = StatusLocked
		return nil
	default:
		return errf(ErrState, "cannot lock %s tool %q", t.status, toolID)
	}
}

// Unlock 解锁刀具（已耗尽/破损的刀不能解锁为可用）。
func (s *Service) Unlock(groupID, toolID string) error {
	if groupID == "" || toolID == "" {
		return errf(ErrInvalid, "group/tool id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	g, err := s.lockGroup(groupID)
	if err != nil {
		return err
	}
	defer s.unlockGroup()
	t, err := g.tool(toolID)
	if err != nil {
		return err
	}
	switch t.status {
	case StatusAvailable:
		return nil // 已是可用，幂等
	case StatusLocked:
		if t.used >= g.cfg.LifeLimit {
			return errf(ErrState, "cannot unlock exhausted tool %q to available", toolID)
		}
		t.status = StatusAvailable
		return nil
	default:
		return errf(ErrState, "cannot unlock %s tool %q to available", t.status, toolID)
	}
}

// Query 返回刀组快照。
func (s *Service) Query(groupID string) (GroupSnapshot, error) {
	if groupID == "" {
		return GroupSnapshot{}, errf(ErrInvalid, "group id is empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	g, err := s.lockGroup(groupID)
	if err != nil {
		return GroupSnapshot{}, err
	}
	defer s.unlockGroup()

	snap := GroupSnapshot{GroupID: groupID}
	for _, t := range g.order {
		snap.Order = append(snap.Order, ToolSnapshot{
			ID:        t.id,
			Status:    t.status,
			Used:      t.used,
			Reserved:  t.reserved,
			Remaining: remaining(t, g.cfg.LifeLimit),
		})
	}
	if chosen, cerr := chooseTool(g, 1); cerr == nil {
		snap.Selected = chosen.id
	}
	return snap, nil
}

// Warnings 返回已发出的预警副本。
func (s *Service) Warnings() []WarningEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]WarningEvent, len(s.wares))
	copy(out, s.wares)
	return out
}
