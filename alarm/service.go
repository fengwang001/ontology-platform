package alarm

import (
	"container/heap"
	"fmt"
	"sort"
	"sync"
)

// Logger 用于打印每个操作的输入、输出与判定依据（可置空）。
type Logger interface {
	Printf(format string, args ...any)
}

// Service 是报警生命周期管理服务。
type Service struct {
	mu     sync.Mutex
	cfg    Config
	points map[int]*activePoint

	lastTS int

	activeConditions map[string]struct{}

	tree activeTree

	// 出现时间线：每次报警“新出现在活动列表”记录一个 (时刻)，升序追加。
	appearAt []int

	expireHeap expireHeap

	log Logger
}

// New 创建服务并注册报警点。
func New(cfg Config, points []PointConfig, log Logger) *Service {
	s := &Service{
		cfg:              cfg,
		points:           make(map[int]*activePoint, len(points)),
		activeConditions: map[string]struct{}{},
		expireHeap:       expireHeap{},
		log:              log,
	}
	heap.Init(&s.expireHeap)
	for _, c := range points {
		p := &activePoint{
			id:       c.ID,
			priority: c.Priority,
			conds:    make(map[string]struct{}, len(c.SuppressConditions)),
		}
		for _, cond := range c.SuppressConditions {
			p.conds[cond] = struct{}{}
		}
		s.points[c.ID] = p
	}
	return s
}

// ---- 对外 API ----------------------------------------------------------------

// Trigger 处理报警点 id 在时刻 ts 的“触发”。
func (s *Service) Trigger(ts, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := fmt.Sprintf("Trigger(ts=%d,id=%d)", ts, id)
	if err := s.checkClock(ts); err != nil {
		return s.reject(op, err)
	}
	p, err := s.lookup(id)
	if err != nil {
		return s.reject(op, err)
	}

	s.processExpiry(ts)

	if p.disabled {
		// 停用期间的触发与返回被忽略，不改变状态；仍推进时钟。
		s.lastTS = ts
		s.accept(op, "disabled: ignored")
		return nil
	}

	st, entered := transitionTrigger(p.state)
	s.lastTS = ts
	if !entered {
		// 已处于激活：空操作，不重复计数，不改变时刻。
		s.accept(op, fmt.Sprintf("already %s: no-op", p.state))
		return nil
	}

	p.state = st
	p.lastActiveAt = ts

	if p.shelved {
		// 屏蔽期间触发：状态机照常演进，但不计入震荡窗口，也不进入活动列表。
		s.accept(op, "entered-active while shelved: not counted")
		return nil
	}

	// 非屏蔽下真正“进入激活”：计入震荡窗口（紧急点记录但永不自动屏蔽）。
	p.chat = append(p.chat, ts)
	s.trimChat(p, ts)

	if p.priority != PriorityEmergency && s.cfg.ChatCount > 0 && len(p.chat) >= s.cfg.ChatCount {
		// 达到震荡阈值：自动屏蔽。窗口清空，屏蔽期内的触发不再计入。
		p.chat = p.chat[:0]
		s.applyShelve(p, false, ts+s.cfg.ChatShelveSec, ChatReason)
		s.accept(op, "chattering threshold reached: auto-shelved")
		return nil
	}

	if s.refreshTree(p) {
		s.recordAppearance(p, ts)
	}
	s.accept(op, fmt.Sprintf("-> %s at %d", p.state, ts))
	return nil
}

// Return 处理报警点 id 在时刻 ts 的“返回”（工艺值恢复正常）。
func (s *Service) Return(ts, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := fmt.Sprintf("Return(ts=%d,id=%d)", ts, id)
	if err := s.checkClock(ts); err != nil {
		return s.reject(op, err)
	}
	p, err := s.lookup(id)
	if err != nil {
		return s.reject(op, err)
	}

	s.processExpiry(ts)
	s.lastTS = ts

	if p.disabled {
		s.accept(op, "disabled: ignored")
		return nil
	}

	st, changed := transitionReturn(p.state)
	if !changed {
		s.accept(op, fmt.Sprintf("state %s: return no-op", p.state))
		return nil
	}
	p.state = st
	if s.refreshTree(p) {
		s.recordAppearance(p, ts)
	}
	s.accept(op, fmt.Sprintf("-> %s", p.state))
	return nil
}

// Ack 由 role 对报警点 id 在时刻 ts 执行确认。
func (s *Service) Ack(ts, id int, role Role, actor string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := fmt.Sprintf("Ack(ts=%d,id=%d,role=%s,actor=%q)", ts, id, role, actor)
	if role != RoleOperator && role != RoleEngineer {
		return s.reject(op, &AlarmError{Code: ErrInvalidArg, Msg: "unknown role"})
	}
	if err := s.checkClock(ts); err != nil {
		return s.reject(op, err)
	}
	p, err := s.lookup(id)
	if err != nil {
		return s.reject(op, err)
	}

	s.processExpiry(ts)
	s.lastTS = ts

	st, ok := transitionAck(p.state)
	if !ok {
		return s.reject(op, &AlarmError{Code: ErrStateNotAllowed, Msg: fmt.Sprintf("ack not allowed in state %s", p.state)})
	}
	p.state = st
	if s.refreshTree(p) {
		s.recordAppearance(p, ts)
	}
	s.accept(op, fmt.Sprintf("-> %s", p.state))
	return nil
}

// Shelve 手动屏蔽非紧急报警一段时长，须附非空原因。
func (s *Service) Shelve(ts, id int, role Role, durationSec int, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := fmt.Sprintf("Shelve(ts=%d,id=%d,role=%s,dur=%d,reason=%q)", ts, id, role, durationSec, reason)
	// 参数非法先于一切：时长非正、原因空。
	if durationSec <= 0 {
		return s.reject(op, &AlarmError{Code: ErrInvalidArg, Msg: "duration must be positive"})
	}
	if reason == "" {
		return s.reject(op, &AlarmError{Code: ErrInvalidArg, Msg: "reason required"})
	}
	if role != RoleOperator && role != RoleEngineer {
		return s.reject(op, &AlarmError{Code: ErrInvalidArg, Msg: "unknown role"})
	}
	if err := s.checkClock(ts); err != nil {
		return s.reject(op, err)
	}
	p, err := s.lookup(id)
	if err != nil {
		return s.reject(op, err)
	}
	// 状态不允许：紧急不可屏蔽、已停用不可屏蔽、已屏蔽须先解除。
	if p.priority == PriorityEmergency {
		return s.reject(op, &AlarmError{Code: ErrStateNotAllowed, Msg: "emergency point cannot be shelved"})
	}
	if p.disabled {
		return s.reject(op, &AlarmError{Code: ErrStateNotAllowed, Msg: "disabled point cannot be shelved"})
	}
	if p.shelved {
		return s.reject(op, &AlarmError{Code: ErrStateNotAllowed, Msg: "already shelved; unshelve first"})
	}
	// 时长上限排在状态类错误之后。
	limit := s.cfg.HighShelveMaxSec
	if p.priority == PriorityLow {
		limit = s.cfg.LowShelveMaxSec
	}
	if durationSec > limit {
		return s.reject(op, &AlarmError{Code: ErrShelveTooLong, Msg: fmt.Sprintf("duration %d exceeds limit %d", durationSec, limit)})
	}

	s.processExpiry(ts)
	s.lastTS = ts

	s.applyShelve(p, true, ts+durationSec, reason)
	s.accept(op, fmt.Sprintf("shelved until %d", p.shelveUntil))
	return nil
}

// Unshelve 手动提前解除屏蔽。
func (s *Service) Unshelve(ts, id int, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := fmt.Sprintf("Unshelve(ts=%d,id=%d,role=%s)", ts, id, role)
	if role != RoleOperator && role != RoleEngineer {
		return s.reject(op, &AlarmError{Code: ErrInvalidArg, Msg: "unknown role"})
	}
	if err := s.checkClock(ts); err != nil {
		return s.reject(op, err)
	}
	p, err := s.lookup(id)
	if err != nil {
		return s.reject(op, err)
	}

	s.processExpiry(ts)
	s.lastTS = ts

	if !p.shelved {
		return s.reject(op, &AlarmError{Code: ErrStateNotAllowed, Msg: "not shelved"})
	}
	s.clearShelve(p)
	if s.refreshTree(p) {
		s.recordAppearance(p, ts)
	}
	s.accept(op, "manual unshelve")
	return nil
}

// Disable 仅工程师可停用，须附非空变更单号。
func (s *Service) Disable(ts, id int, role Role, ticket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := fmt.Sprintf("Disable(ts=%d,id=%d,role=%s,ticket=%q)", ts, id, role, ticket)
	if ticket == "" {
		return s.reject(op, &AlarmError{Code: ErrInvalidArg, Msg: "change ticket required"})
	}
	if role != RoleOperator && role != RoleEngineer {
		return s.reject(op, &AlarmError{Code: ErrInvalidArg, Msg: "unknown role"})
	}
	if err := s.checkClock(ts); err != nil {
		return s.reject(op, err)
	}
	p, err := s.lookup(id)
	if err != nil {
		return s.reject(op, err)
	}
	if role != RoleEngineer {
		return s.reject(op, &AlarmError{Code: ErrNoPermission, Msg: "engineer only"})
	}

	s.processExpiry(ts)
	s.lastTS = ts

	if p.disabled {
		return s.reject(op, &AlarmError{Code: ErrStateNotAllowed, Msg: "already disabled"})
	}
	p.disabled = true
	s.refreshTree(p)
	s.accept(op, "disabled")
	return nil
}

// Enable 仅工程师可启用；启用后状态一律为正常。
func (s *Service) Enable(ts, id int, role Role, ticket string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := fmt.Sprintf("Enable(ts=%d,id=%d,role=%s,ticket=%q)", ts, id, role, ticket)
	if ticket == "" {
		return s.reject(op, &AlarmError{Code: ErrInvalidArg, Msg: "change ticket required"})
	}
	if role != RoleOperator && role != RoleEngineer {
		return s.reject(op, &AlarmError{Code: ErrInvalidArg, Msg: "unknown role"})
	}
	if err := s.checkClock(ts); err != nil {
		return s.reject(op, err)
	}
	p, err := s.lookup(id)
	if err != nil {
		return s.reject(op, err)
	}
	if role != RoleEngineer {
		return s.reject(op, &AlarmError{Code: ErrNoPermission, Msg: "engineer only"})
	}

	s.processExpiry(ts)
	s.lastTS = ts

	if !p.disabled {
		return s.reject(op, &AlarmError{Code: ErrStateNotAllowed, Msg: "not disabled"})
	}
	p.disabled = false
	// 启用时状态一律正常；即使工艺值仍越限，也要等下一次触发才激活。
	p.state = StateNormal
	p.lastActiveAt = 0
	p.chat = p.chat[:0]
	s.refreshTree(p)
	s.accept(op, "enabled; state reset to normal")
	return nil
}

// SetActiveConditions 由系统按工况信号变化设置当前为真的工况集合（整体替换）。
// 不属于操作员行为，不计入震荡，不受屏蔽时长限制。
func (s *Service) SetActiveConditions(ts int, conditions []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := fmt.Sprintf("SetActiveConditions(ts=%d,conds=%v)", ts, conditions)
	if err := s.checkClock(ts); err != nil {
		return s.reject(op, err)
	}
	s.processExpiry(ts)
	s.lastTS = ts

	next := make(map[string]struct{}, len(conditions))
	for _, c := range conditions {
		next[c] = struct{}{}
	}

	// 只有抑制状态真正翻转的点才会改变活动列表。
	for _, p := range s.points {
		was := p.suppressed
		p.recomputeSuppressed(next)
		if was == p.suppressed {
			continue
		}
		if s.refreshTree(p) {
			s.recordAppearance(p, ts)
		}
	}
	s.activeConditions = next
	s.accept(op, "conditions updated")
	return nil
}

// ActiveList 返回时刻 ts 操作员可见的活动报警（按规定排序）。
func (s *Service) ActiveList(ts int) ([]ActiveAlarm, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := fmt.Sprintf("ActiveList(ts=%d)", ts)
	if err := s.checkClock(ts); err != nil {
		return nil, s.reject(op, err)
	}
	s.processExpiry(ts)
	s.lastTS = ts

	buf := make([]*activePoint, 0)
	ps := s.tree.inorder(buf)
	out := make([]ActiveAlarm, len(ps))
	for i, p := range ps {
		out[i] = ActiveAlarm{ID: p.id, Priority: p.priority, State: p.state, LastActiveAt: p.lastActiveAt}
	}
	s.accept(op, fmt.Sprintf("returned %d alarms", len(out)))
	return out, nil
}

// AlarmRate 返回区间 (ts-durationSec, ts]（左开右闭）内新出现在活动列表的
// 报警条数；同一报警多次出现按多次计。
func (s *Service) AlarmRate(ts, durationSec int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	op := fmt.Sprintf("AlarmRate(ts=%d,dur=%d)", ts, durationSec)
	if durationSec <= 0 {
		return 0, s.reject(op, &AlarmError{Code: ErrInvalidArg, Msg: "duration must be positive"})
	}
	if err := s.checkClock(ts); err != nil {
		return 0, s.reject(op, err)
	}
	s.processExpiry(ts)
	s.lastTS = ts

	lo := ts - durationSec
	// 出现时刻 > lo（左开）且 <= ts（右闭）。
	i := sort.Search(len(s.appearAt), func(i int) bool { return s.appearAt[i] > lo })
	j := sort.Search(len(s.appearAt), func(i int) bool { return s.appearAt[i] > ts })
	n := j - i
	s.accept(op, fmt.Sprintf("count=%d in (%d,%d]", n, lo, ts))
	return n, nil
}

// ShelvingOf 查询某点当前屏蔽信息（供测试/运维核对）。
func (s *Service) ShelvingOf(ts, id int) (Shelving, State, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(ts); err != nil {
		return Shelving{}, StateNormal, false, err
	}
	p, err := s.lookup(id)
	if err != nil {
		return Shelving{}, StateNormal, false, err
	}
	s.processExpiry(ts)
	s.lastTS = ts
	return Shelving{Active: p.shelved, Manual: p.manual, Until: p.shelveUntil, Reason: p.reason}, p.state, p.suppressed, nil
}

// ---- 内部机制 ----------------------------------------------------------------

// checkClock 仅检查时钟回退与非法时刻，不推进时钟。
func (s *Service) checkClock(ts int) error {
	if ts < 0 {
		return &AlarmError{Code: ErrInvalidArg, Msg: "negative timestamp"}
	}
	if ts < s.lastTS {
		return &AlarmError{Code: ErrClockRollback, Msg: fmt.Sprintf("ts %d < last accepted %d", ts, s.lastTS)}
	}
	return nil
}

func (s *Service) lookup(id int) (*activePoint, error) {
	p, ok := s.points[id]
	if !ok {
		return nil, &AlarmError{Code: ErrNoSuchPoint, Msg: fmt.Sprintf("point %d not registered", id)}
	}
	return p, nil
}

// trimChat 删除窗口左边界外的记录：保留满足 now-w < t（左开右闭）的时刻。
func (s *Service) trimChat(p *activePoint, now int) {
	if s.cfg.ChatWindowSec <= 0 {
		p.chat = p.chat[:0]
		return
	}
	cutoff := now - s.cfg.ChatWindowSec
	keep := 0
	for _, t := range p.chat {
		if t > cutoff {
			p.chat[keep] = t
			keep++
		}
	}
	p.chat = p.chat[:keep]
}

func (s *Service) applyShelve(p *activePoint, manual bool, until int, reason string) {
	p.shelved = true
	p.manual = manual
	p.shelveUntil = until
	p.reason = reason
	s.pushExpire(p)
	s.refreshTree(p)
}

func (s *Service) clearShelve(p *activePoint) {
	p.shelved = false
	p.manual = false
	p.reason = ""
	p.shelveUntil = 0
}

// processExpiry 惰性解除所有 until <= now 的屏蔽。
// 到期的真实时刻为 until；因为事件按非降时刻串行处理，until 时刻点的可见性
// 与当前一致，故出现时间线直接记录在 until，保证区间边界精确。
func (s *Service) processExpiry(now int) {
	for s.expireHeap.Len() > 0 {
		top := s.expireHeap[0].at
		until, id := decodeExpire(top)
		if until > now {
			return
		}
		heap.Pop(&s.expireHeap)
		p, ok := s.points[id]
		if !ok {
			continue
		}
		// 堆项可能已失效（手动解除后又重新屏蔽）：核对身份。
		if !p.shelved || p.shelveUntil != until {
			continue
		}
		s.clearShelve(p)
		if s.refreshTree(p) {
			s.recordAppearance(p, until)
		}
	}
}

// shouldShow 判断该点当前是否应出现在活动列表。
func shouldShow(p *activePoint) bool {
	if p.disabled || p.shelved || p.suppressed {
		return false
	}
	switch p.state {
	case StateActiveUnacked, StateActiveAcked, StateReturnUnacked:
		return true
	default:
		return false
	}
}

// refreshTree 让点在活动列表树中的成员关系与其应可见性保持一致；
// 返回该点是否由“不可见”变为“可见”（即新出现在活动列表）。
// 已在树中时一律先删后插，以吸收排序键变化（如确认改变确认层级）。
func (s *Service) refreshTree(p *activePoint) bool {
	want := shouldShow(p)
	if want {
		becameVisible := !p.inTree
		if p.inTree {
			s.tree.erase(p)
		}
		s.tree.insert(p)
		p.inTree = true
		return becameVisible
	}
	if p.inTree {
		s.tree.erase(p)
		p.inTree = false
	}
	return false
}

// recordAppearance 记录一次“新出现在活动列表”。
func (s *Service) recordAppearance(p *activePoint, at int) {
	s.appearAt = append(s.appearAt, at)
}

func (s *Service) reject(op string, err error) error {
	if s.log != nil {
		s.log.Printf("IN  %s\nOUT REJECTED: %v", op, err)
	}
	return err
}

func (s *Service) accept(op, why string) {
	if s.log != nil {
		s.log.Printf("IN  %s\nOUT ACCEPTED: %s", op, why)
	}
}
