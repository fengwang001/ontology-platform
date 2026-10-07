package retention

import "sync"

// 对象内部状态只保存“当前态 + 至多一个截止时刻”，不保留任何历史：
// 可见性判定只读取这一定量字段，因此其触及的状态记录条数恒为 O(1)，
// 不随历史转换次数增长（测试通过 probes 探针验证，见 service_test.go）。
type object struct {
	state          State
	attrs          map[string]string
	graceDeadline  int64
	freezeDeadline int64
	probes         int // 测试探针：记录状态字段被判定逻辑读取的次数
}

// edge 是对象的一条有向出边链接。链接自身没有任何删除状态：
// 其可见性完全跟随源对象。
type edge struct {
	source string
	target string
}

// Service 是逻辑删除与可见性服务。
//
// 并发语义：一把 sync.RWMutex 串行化所有写操作，并为查询提供与某个写时刻
// 一致的快照读，因此所有操作的结果等价于按某个全局顺序依次执行（可线性化）。
// 查询持锁期间完成源对象与其出边的全部判定，同一次查询内不会出现
// 对象与出边可见性不一致。
type Service struct {
	mu      sync.RWMutex
	clock   Clock
	audit   *AuditLog
	objects map[string]*object
	edges   map[edge]struct{}
	// outOf 保存源对象的出边目标集合，仅用于枚举其全部出边。
	outOf map[string]map[string]struct{}
}

// NewService 创建服务。clock 为 nil 时使用系统墙钟。
func NewService(clock Clock, audit *AuditLog) *Service {
	if clock == nil {
		clock = systemClock{}
	}
	return &Service{
		clock:   clock,
		audit:   audit,
		objects: make(map[string]*object),
		edges:   make(map[edge]struct{}),
		outOf:   make(map[string]map[string]struct{}),
	}
}

// Advance 推进逻辑时钟（仅对 *LogicalClock 有意义），并立即为全部
// 宽限到期对象完成惰性到期所要求的归档。归档不消耗对象转换请求。
func (s *Service) Advance(t int64) {
	if lc, ok := s.clock.(*LogicalClock); ok {
		lc.Advance(t)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	for id, o := range s.objects {
		s.promoteLocked(id, o, now)
	}
}

func (s *Service) log(e AuditEntry) { s.audit.append(e) }

// promoteLocked 处理“宽限截止时刻恰好到达而未被撤销”的规则：
// 处于 GRACE 且 now >= graceDeadline 时，在下一次任何涉及该对象的
// 操作中无条件转入 ARCHIVED。
// FROZEN 的到期不做惰性处理：冻结对象只能通过显式 Archive 在期满后归档。
func (s *Service) promoteLocked(id string, o *object, now int64) {
	if o.state == StateGrace && now >= o.graceDeadline {
		o.state = StateArchived
		o.graceDeadline = 0
		s.log(AuditEntry{
			At:          now,
			Op:          "auto_archive_after_grace",
			ObjectID:    id,
			StateBefore: StateGrace,
			StateAfter:  StateArchived,
			Success:     true,
		})
	}
}

func (s *Service) failLog(op, id string, before, after State, now int64, code ErrorCode, in map[string]any) {
	s.log(AuditEntry{At: now, Op: op, ObjectID: id,
		StateBefore: before, StateAfter: after, Success: false,
		ErrorCode: code.String(), Input: in})
}

// CreateObject 以存活状态登记一个对象。
func (s *Service) CreateObject(id string, attrs map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if _, ok := s.objects[id]; ok {
		s.failLog("create_object", id, s.objects[id].state, s.objects[id].state, now,
			ErrIllegalTransition, map[string]any{"id": id})
		return errf(ErrIllegalTransition, "object %q already exists", id)
	}
	s.objects[id] = &object{state: StateAlive, attrs: copyAttrs(attrs)}
	s.log(AuditEntry{At: now, Op: "create_object", ObjectID: id,
		StateBefore: StateAlive, StateAfter: StateAlive, Success: true,
		Input: map[string]any{"id": id}})
	return nil
}

// AddEdge 登记一条出边链接；源与目标对象都必须存在。链接不带删除标记。
func (s *Service) AddEdge(sourceID, targetID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	src, ok := s.objects[sourceID]
	if !ok {
		s.failLog("add_edge", sourceID, 0, 0, now, ErrNotFound,
			map[string]any{"source": sourceID, "target": targetID})
		return errf(ErrNotFound, "source object %q not found", sourceID)
	}
	s.promoteLocked(sourceID, src, now)
	if _, ok := s.objects[targetID]; !ok {
		s.failLog("add_edge", sourceID, src.state, src.state, now, ErrNotFound,
			map[string]any{"source": sourceID, "target": targetID})
		return errf(ErrNotFound, "target object %q not found", targetID)
	}
	s.edges[edge{sourceID, targetID}] = struct{}{}
	if s.outOf[sourceID] == nil {
		s.outOf[sourceID] = map[string]struct{}{}
	}
	s.outOf[sourceID][targetID] = struct{}{}
	s.log(AuditEntry{At: now, Op: "add_edge", ObjectID: sourceID,
		StateBefore: src.state, StateAfter: src.state, Success: true,
		Input: map[string]any{"source": sourceID, "target": targetID}})
	return nil
}

// SoftDelete 请求 ALIVE -> GRACE。graceDeadline 必须严格晚于当前时刻。
func (s *Service) SoftDelete(id string, graceDeadline int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	o, ok := s.objects[id]
	if !ok {
		s.failLog("soft_delete", id, 0, 0, now, ErrNotFound,
			map[string]any{"grace_deadline": graceDeadline})
		return errf(ErrNotFound, "object %q not found", id)
	}
	s.promoteLocked(id, o, now)
	before := o.state
	if before != StateAlive {
		s.failLog("soft_delete", id, before, before, now, ErrIllegalTransition,
			map[string]any{"grace_deadline": graceDeadline})
		return errf(ErrIllegalTransition, "soft_delete from %s not allowed", before)
	}
	if graceDeadline <= now {
		s.failLog("soft_delete", id, before, before, now, ErrInvalidParameter,
			map[string]any{"grace_deadline": graceDeadline})
		return errf(ErrInvalidParameter, "grace deadline %d must be after now %d", graceDeadline, now)
	}
	o.state = StateGrace
	o.graceDeadline = graceDeadline
	s.log(AuditEntry{At: now, Op: "soft_delete", ObjectID: id,
		StateBefore: before, StateAfter: StateGrace, Success: true,
		Input: map[string]any{"grace_deadline": graceDeadline}})
	return nil
}

// Undelete 请求 GRACE -> ALIVE，抹去本次删除记录对后续查询的影响。
// 若宽限已到期，对象已被惰性归档，按方向不允许报错（第二类）；
// 在 FROZEN 未满时尝试撤销属于第四类错误。
func (s *Service) Undelete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	o, ok := s.objects[id]
	if !ok {
		s.failLog("undelete", id, 0, 0, now, ErrNotFound, nil)
		return errf(ErrNotFound, "object %q not found", id)
	}
	s.promoteLocked(id, o, now)
	before := o.state
	if before == StateFrozen && now < o.freezeDeadline {
		s.failLog("undelete", id, before, before, now, ErrFrozenNotExpired, nil)
		return errf(ErrFrozenNotExpired, "object frozen until %d", o.freezeDeadline)
	}
	if before != StateGrace {
		s.failLog("undelete", id, before, before, now, ErrIllegalTransition, nil)
		return errf(ErrIllegalTransition, "undelete from %s not allowed", before)
	}
	o.state = StateAlive
	o.graceDeadline = 0
	s.log(AuditEntry{At: now, Op: "undelete", ObjectID: id,
		StateBefore: before, StateAfter: StateAlive, Success: true})
	return nil
}

// Freeze 请求 ALIVE -> FROZEN。冻结时长在进入时一次性确定：
// freezeDeadline = now + duration，此后不因任何操作重算或延长。
func (s *Service) Freeze(id string, duration int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	o, ok := s.objects[id]
	if !ok {
		s.failLog("freeze", id, 0, 0, now, ErrNotFound,
			map[string]any{"duration": duration})
		return errf(ErrNotFound, "object %q not found", id)
	}
	s.promoteLocked(id, o, now)
	before := o.state
	if before != StateAlive {
		s.failLog("freeze", id, before, before, now, ErrIllegalTransition,
			map[string]any{"duration": duration})
		return errf(ErrIllegalTransition, "freeze from %s not allowed", before)
	}
	if duration <= 0 {
		s.failLog("freeze", id, before, before, now, ErrInvalidParameter,
			map[string]any{"duration": duration})
		return errf(ErrInvalidParameter, "freeze duration %d must be positive", duration)
	}
	o.state = StateFrozen
	o.freezeDeadline = now + duration
	s.log(AuditEntry{At: now, Op: "freeze", ObjectID: id,
		StateBefore: before, StateAfter: StateFrozen, Success: true,
		Input: map[string]any{"duration": duration}})
	return nil
}

// Archive 请求 FROZEN -> ARCHIVED，且仅在冻结期满（now >= freezeDeadline）
// 后放行。GRACE 状态不允许显式归档（到期由系统自动完成）。
func (s *Service) Archive(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	o, ok := s.objects[id]
	if !ok {
		s.failLog("archive", id, 0, 0, now, ErrNotFound, nil)
		return errf(ErrNotFound, "object %q not found", id)
	}
	s.promoteLocked(id, o, now)
	before := o.state
	if before == StateFrozen && now < o.freezeDeadline {
		s.failLog("archive", id, before, before, now, ErrFrozenNotExpired, nil)
		return errf(ErrFrozenNotExpired, "object frozen until %d", o.freezeDeadline)
	}
	if before != StateFrozen {
		s.failLog("archive", id, before, before, now, ErrIllegalTransition, nil)
		return errf(ErrIllegalTransition, "explicit archive from %s not allowed", before)
	}
	o.state = StateArchived
	o.freezeDeadline = 0
	s.log(AuditEntry{At: now, Op: "archive", ObjectID: id,
		StateBefore: before, StateAfter: StateArchived, Success: true})
	return nil
}

func copyAttrs(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
