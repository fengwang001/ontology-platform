package toollife

import (
	"fmt"
	"sync"
)

// Logger 测试日志钩子；默认不打印。
type Logger interface {
	Logf(format string, args ...any)
}

// Service 刀具库寿命管理服务。并发安全。
type Service struct {
	mu     sync.RWMutex
	groups map[string]*group

	// owner 申请编号 -> 所属刀组。用于跨刀组冲突判定。
	ownerMu sync.RWMutex
	owner   map[string]string

	logger Logger
}

// New 创建空服务。
func New() *Service {
	return &Service{groups: make(map[string]*group), owner: make(map[string]string)}
}

// SetLogger 注入日志钩子（可在测试中打印输入、输出与判定依据）。
func (s *Service) SetLogger(l Logger) { s.logger = l }

func (s *Service) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Logf(format, args...)
	}
}

// validateConfig 在不触碰共享状态的前提下校验刀组配置参数。
func validateConfig(cfg GroupConfig) error {
	if !cfg.Basis.valid() {
		return errInvalid("unknown life basis %d", cfg.Basis)
	}
	if !cfg.Mode.valid() {
		return errInvalid("unknown select mode %d", cfg.Mode)
	}
	if cfg.LifeLimit <= 0 {
		return errInvalid("life limit must be positive, got %d", cfg.LifeLimit)
	}
	if cfg.WarnPermille < 0 || cfg.WarnPermille > 1000 {
		return errInvalid("warn permille must be in [0,1000], got %d", cfg.WarnPermille)
	}
	if len(cfg.ToolIDs) == 0 {
		return errInvalid("group must contain at least one tool")
	}
	seen := make(map[string]bool, len(cfg.ToolIDs))
	for _, tid := range cfg.ToolIDs {
		if tid == "" {
			return errInvalid("tool id must not be empty")
		}
		if seen[tid] {
			return errInvalid("duplicated tool id %q in group", tid)
		}
		seen[tid] = true
	}
	return nil
}

// lookupGroup 取刀组；groupID 非法优先于不存在。
func (s *Service) lookupGroup(groupID string) (*group, error) {
	if groupID == "" {
		return nil, errInvalid("group id must not be empty")
	}
	s.mu.RLock()
	g := s.groups[groupID]
	s.mu.RUnlock()
	if g == nil {
		return nil, errNotFound("group %q not found", groupID)
	}
	return g, nil
}

// lookupTool 取刀组与刀具；错误优先级：参数非法 > 刀组不存在 > 刀具不存在。
// 返回时已持有刀组锁，调用方不得再次加锁。
func (s *Service) lookupToolLocked(groupID, toolID string) (*group, *holder, error) {
	g, err := s.lookupGroup(groupID)
	if err != nil {
		return nil, nil, err
	}
	if toolID == "" {
		return nil, nil, errInvalid("tool id must not be empty")
	}
	g.mu.Lock()
	h := g.byID[toolID]
	if h == nil {
		g.mu.Unlock()
		return nil, nil, errNotFound("tool %q not found in group %q", toolID, groupID)
	}
	return g, h, nil
}

// AddGroup 注册刀组。
func (s *Service) AddGroup(id string, cfg GroupConfig) error {
	if id == "" {
		return errInvalid("group id must not be empty")
	}
	if err := validateConfig(cfg); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.groups[id]; exists {
		return errConflict("group %q already exists", id)
	}
	s.groups[id] = newGroup(id, cfg)
	s.logf("AddGroup id=%q cfg={basis=%d limit=%d warnPermille=%d mode=%d tools=%v} -> ok",
		id, cfg.Basis, cfg.LifeLimit, cfg.WarnPermille, cfg.Mode, cfg.ToolIDs)
	return nil
}

// Apply 通道申请刀具。
func (s *Service) Apply(groupID, requestID string, estimated int) (ApplyResult, error) {
	if groupID == "" || requestID == "" {
		return ApplyResult{}, errInvalid("group id and request id must not be empty")
	}
	if estimated <= 0 {
		return ApplyResult{}, errInvalid("estimated consumption must be positive, got %d", estimated)
	}
	g, err := s.lookupGroup(groupID)
	if err != nil {
		return ApplyResult{}, err
	}

	// 加锁顺序固定：先刀组锁，再 owner 索引锁。所有路径一致，不会成环。
	g.mu.Lock()
	defer g.mu.Unlock()
	s.ownerMu.Lock()
	owner, ownerKnown := s.owner[requestID]
	s.ownerMu.Unlock()

	// 同编号：先判归属冲突，再判内容冲突，最后幂等回放原结果。
	if ownerKnown {
		if owner != groupID {
			return ApplyResult{}, errConflict("request %q already belongs to group %q", requestID, owner)
		}
		if prev, ok := g.requests[requestID]; ok && prev.estimated == estimated {
			res := ApplyResult{ToolID: prev.toolID, Reserved: prev.estimated, Replayed: true}
			s.logf("Apply group=%q req=%q est=%d -> replay tool=%q", groupID, requestID, estimated, res.ToolID)
			return res, nil
		} else if ok {
			return ApplyResult{}, errConflict(
				"request %q replayed with estimated %d, original %d",
				requestID, estimated, prev.estimated)
		}
	}

	h, reason := g.pick(estimated)
	if reason != pickOK {
		if reason == pickNoMargin {
			return ApplyResult{}, &Error{Code: ErrNoMargin,
				Msg: fmt.Sprintf("group %q has no free margin now (available tools are fully reserved)", groupID)}
		}
		return ApplyResult{}, &Error{Code: ErrNoTool,
			Msg: fmt.Sprintf("group %q has no selectable tool", groupID)}
	}

	h.reserved += estimated
	g.requests[requestID] = &request{
		id: requestID, groupID: groupID, estimated: estimated, toolID: h.id, open: true,
	}
	s.ownerMu.Lock()
	s.owner[requestID] = groupID
	s.ownerMu.Unlock()

	s.logf("Apply group=%q req=%q est=%d -> tool=%q reserved=%d used=%d",
		groupID, requestID, estimated, h.id, h.reserved, h.used)
	return ApplyResult{ToolID: h.id, Reserved: estimated}, nil
}

// Settle 按申请编号记账，预占转已用，差额释放。
func (s *Service) Settle(requestID string, actual int) (SettleResult, error) {
	if requestID == "" {
		return SettleResult{}, errInvalid("request id must not be empty")
	}
	if actual < 0 {
		return SettleResult{}, errInvalid("actual consumption must be non-negative, got %d", actual)
	}

	s.ownerMu.RLock()
	groupID, known := s.owner[requestID]
	s.ownerMu.RUnlock()
	if !known {
		return SettleResult{}, errNotFound("request %q not found", requestID)
	}
	g, err := s.lookupGroup(groupID)
	if err != nil {
		return SettleResult{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	rec := g.requests[requestID]
	if rec == nil {
		return SettleResult{}, errNotFound("request %q not found", requestID)
	}
	// 记账幂等：已记账的重复提交不重复累计；actual 不一致属同编号换内容冲突。
	if rec.settled {
		if rec.actual != actual {
			return SettleResult{}, errConflict(
				"request %q settled with actual %d, replay got %d", requestID, rec.actual, actual)
		}
		s.logf("Settle req=%q actual=%d -> replay tool=%q", requestID, actual, rec.toolID)
		return SettleResult{ToolID: rec.toolID, Actual: actual,
			Exhausted: rec.exhaustedAtSettle}, nil
	}
	if !rec.open {
		return SettleResult{}, errState("request %q was cancelled", requestID)
	}

	h := g.byID[rec.toolID]
	beforePermille := usedPermille(h.used, g.cfg.LifeLimit)
	h.reserved -= rec.estimated
	h.used += actual
	rec.open = false
	rec.settled = true
	rec.actual = actual

	exhausted := h.used >= g.cfg.LifeLimit
	if exhausted && h.status == StatusAvailable {
		// 破损/锁定刀照常记账但保持原状态；仅可用刀达限后转为已耗尽。
		h.status = StatusExhausted
	}
	rec.exhaustedAtSettle = exhausted

	afterPermille := usedPermille(h.used, g.cfg.LifeLimit)
	warned := false
	// 仅由记账引起：首次从低于预警比例跨到不低于；阈值为 0 不预警；换新后 warned 重置。
	if g.cfg.WarnPermille > 0 && !h.warned &&
		beforePermille < g.cfg.WarnPermille && afterPermille >= g.cfg.WarnPermille {
		h.warned = true
		warned = true
	}

	s.logf("Settle req=%q tool=%q est=%d actual=%d -> used=%d reserved=%d status=%s exhausted=%v warned=%v permille=%d->%d(thr=%d)",
		requestID, h.id, rec.estimated, actual, h.used, h.reserved, h.status,
		exhausted, warned, beforePermille, afterPermille, g.cfg.WarnPermille)
	return SettleResult{ToolID: h.id, Actual: actual, Exhausted: exhausted, Warned: warned}, nil
}

// Cancel 中止申请，释放预占且不计消耗。
func (s *Service) Cancel(requestID string) error {
	if requestID == "" {
		return errInvalid("request id must not be empty")
	}
	s.ownerMu.RLock()
	groupID, known := s.owner[requestID]
	s.ownerMu.RUnlock()
	if !known {
		return errNotFound("request %q not found", requestID)
	}
	g, err := s.lookupGroup(groupID)
	if err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	rec := g.requests[requestID]
	if rec == nil {
		return errNotFound("request %q not found", requestID)
	}
	if rec.settled {
		return errState("request %q already settled, cannot cancel", requestID)
	}
	if !rec.open {
		// 中止幂等：重复中止直接成功。
		s.logf("Cancel req=%q -> replay (already cancelled)", requestID)
		return nil
	}
	h := g.byID[rec.toolID]
	h.reserved -= rec.estimated
	rec.open = false
	s.logf("Cancel req=%q tool=%q released=%d -> reserved=%d", requestID, h.id, rec.estimated, h.reserved)
	return nil
}

// ReportBroken 上报刀具破损。
func (s *Service) ReportBroken(groupID, toolID string) error {
	g, h, err := s.lookupToolLocked(groupID, toolID)
	if err != nil {
		return err
	}
	defer g.mu.Unlock()
	if h.status == StatusBroken {
		s.logf("ReportBroken group=%q tool=%q -> replay", groupID, toolID)
		return nil
	}
	if h.status != StatusAvailable {
		return errState("tool %q is %s, cannot mark broken", toolID, h.status)
	}
	h.status = StatusBroken
	s.logf("ReportBroken group=%q tool=%q -> broken (reserved=%d 保持待结算)", groupID, toolID, h.reserved)
	return nil
}

// Replace 换新：用新刀替换破损刀，占据原顺序位置。
func (s *Service) Replace(groupID, toolID, newToolID string) error {
	if newToolID == "" {
		return errInvalid("new tool id must not be empty")
	}
	g, h, err := s.lookupToolLocked(groupID, toolID)
	if err != nil {
		return err
	}
	defer g.mu.Unlock()
	if g.byID[newToolID] != nil {
		return errConflict("tool id %q already exists in group %q", newToolID, groupID)
	}
	if h.status != StatusBroken {
		return errState("tool %q is %s, only broken tools can be replaced", toolID, h.status)
	}
	if h.reserved != 0 {
		return errState("tool %q has %d unsettled reservation(s), replacement refused", toolID, h.reserved)
	}
	for i, cur := range g.tools {
		if cur == h {
			fresh := &holder{id: newToolID, status: StatusAvailable}
			g.tools[i] = fresh
			delete(g.byID, h.id)
			g.byID[newToolID] = fresh
			s.logf("Replace group=%q old=%q new=%q -> available at position %d", groupID, toolID, newToolID, i)
			return nil
		}
	}
	panic("toollife: holder missing from ordered slice")
}

// Lock 锁定刀具。
func (s *Service) Lock(groupID, toolID string) error {
	g, h, err := s.lookupToolLocked(groupID, toolID)
	if err != nil {
		return err
	}
	defer g.mu.Unlock()
	switch h.status {
	case StatusAvailable, StatusExhausted:
		h.status = StatusLocked
		s.logf("Lock group=%q tool=%q -> locked (reserved=%d 保留)", groupID, toolID, h.reserved)
		return nil
	case StatusLocked:
		s.logf("Lock group=%q tool=%q -> replay", groupID, toolID)
		return nil
	default:
		return errState("tool %q is %s, cannot lock", toolID, h.status)
	}
}

// Unlock 解锁刀具。
func (s *Service) Unlock(groupID, toolID string) error {
	g, h, err := s.lookupToolLocked(groupID, toolID)
	if err != nil {
		return err
	}
	defer g.mu.Unlock()
	if h.status != StatusLocked {
		return errState("tool %q is %s, not locked", toolID, h.status)
	}
	// 已耗尽的刀不能解锁为可用：按已用是否达限决定恢复状态。
	if h.used >= g.cfg.LifeLimit {
		h.status = StatusExhausted
	} else {
		h.status = StatusAvailable
	}
	s.logf("Unlock group=%q tool=%q -> %s (used=%d/%d)", groupID, toolID, h.status, h.used, g.cfg.LifeLimit)
	return nil
}

// Query 查询刀组视图。
func (s *Service) Query(groupID string) (GroupView, error) {
	g, err := s.lookupGroup(groupID)
	if err != nil {
		return GroupView{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	view := GroupView{GroupID: groupID, Tools: make([]ToolInfo, 0, len(g.tools))}
	for _, h := range g.tools {
		view.Tools = append(view.Tools, ToolInfo{
			ID:        h.id,
			Status:    h.status,
			Used:      h.used,
			Reserved:  h.reserved,
			Remaining: h.remaining(g.cfg.LifeLimit),
		})
	}
	if cur := g.currentPick(); cur != nil {
		view.CurrentPick = cur.id
	}
	s.logf("Query group=%q -> tools=%d pick=%q", groupID, len(view.Tools), view.CurrentPick)
	return view, nil
}
