package cardinality

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrUnknownScope         = errors.New("cardinality: unknown scope")
	ErrInvalidLimit         = errors.New("cardinality: invalid limit")
	ErrInvalidCommitted     = errors.New("cardinality: negative committed baseline")
	ErrDuplicateReservation = errors.New("cardinality: duplicate reservation id")
	ErrInvalidReservationID = errors.New("cardinality: empty reservation id")
)

// reservation 是一个进行中（已占位、未终结）的新建关联尝试。
// 其名额占用在 Reserve 获批的同一临界区内即刻生效；最终由
// Commit（成功）、Abort（失败）或租约过期（调用方中断）终结。
type reservation struct {
	id       string
	scope    ScopeKey
	deadline int64
}

// scopeState 是单个基数约束作用域的全部可变状态。committed 与
// inflight 都是纯计数，不随历史关联总数或历史请求总数增长，
// 名额判定开销只与当前 inflight map 大小相关。
type scopeState struct {
	limit     Limit
	committed int64
	inflight  map[string]*reservation
}

// snapshot 给出判定瞬间的一致计数证据。excludeID 用于在获批后
// 排除本预留自身，使证据反映"判定依据"而非占位后的世界。
func (s *scopeState) snapshot(now int64, excludeID string) CounterSnapshot {
	live := 0
	for id, r := range s.inflight {
		if id == excludeID || r.deadline <= now {
			continue
		}
		live++
	}
	return CounterSnapshot{
		Committed: s.committed,
		Inflight:  live,
		Limit:     int64(s.limit),
		Remaining: int64(s.limit) - s.committed - int64(live),
		Seq:       s.committed,
	}
}

// Manager 是预留式基数名额分配器。Reserve 在同一把互斥锁内原子地
// 完成"过期回收 + 基线校验 + 名额计算 + 占位"，因此任意并发 Reserve
// 的结果都等价于按全局判定序号（审计追加位置）逐个串行执行。
type Manager struct {
	mu         sync.Mutex
	clock      Clock
	defaultTTL time.Duration
	scopes     map[ScopeKey]*scopeState
	// byID 索引全部存活预留，保证 O(1) 的 Commit/Abort/Heartbeat，
	// 并使 Sweep 开销只与当前进行中预留总数相关，与历史请求数无关。
	byID  map[string]*reservation
	audit []Event
}

// NewManager 创建管理器。leaseTTL 必须为正；clock 为 nil 时使用
// 基于单调时间的 SystemClock。
func NewManager(leaseTTL time.Duration, clock Clock) *Manager {
	if leaseTTL <= 0 {
		panic("cardinality: lease ttl must be positive")
	}
	if clock == nil {
		clock = NewSystemClock()
	}
	return &Manager{
		clock:      clock,
		defaultTTL: leaseTTL,
		scopes:     make(map[ScopeKey]*scopeState),
		byID:       make(map[string]*reservation),
	}
}

// EnsureScope 登记（或幂等更新）一个约束作用域及其上限，可通过
// committed 预置已存在的关联基线计数。新上限不得小于已确认关联数。
func (m *Manager) EnsureScope(scope ScopeKey, limit Limit, committed int64) error {
	if limit < 0 {
		return ErrInvalidLimit
	}
	if committed < 0 {
		return ErrInvalidCommitted
	}
	if int64(limit) < committed {
		return ErrInvalidLimit
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.scopes[scope]
	if !ok {
		s = &scopeState{inflight: make(map[string]*reservation)}
		m.scopes[scope] = s
	}
	s.limit = limit
	s.committed = committed
	return nil
}

// ReserveRequest 是一次新建关联尝试的输入。ExpectedV 是调用方读取
// 链接类型时观察到的基线版本（= 当时已确认关联数）。
type ReserveRequest struct {
	Scope         ScopeKey
	ExpectedV     int64
	ReservationID string
	LeaseTTL      time.Duration
}

// Reserve 原子地判定一次新建关联尝试。判定顺序固定且互斥：
//  1. 回收已过租约的悬置预留（调用方中断的唯一裁定依据）；
//  2. 基线版本冲突（优先于一切名额判定）；
//  3. 已确认关联数达到上限（优先于进行中占用判定）；
//  4. 剩余名额被其他进行中请求占用（暂时性拒绝）。
//
// 获批时在同一临界区内立即占用一个进行中名额并返回租约截止时刻；
// 被拒绝时除追加一条审计记录（可重放证据）外，不产生任何关联、
// 版本或时钟变化。空的或与存活预留重复的 ReservationID 视为
// 编程错误并 panic。
func (m *Manager) Reserve(req ReserveRequest) (Decision, int64) {
	if req.ReservationID == "" {
		panic(ErrInvalidReservationID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock.NowNanos()
	m.sweepExpiredLocked(now)

	d := Decision{
		Scope:         req.Scope,
		ExpectedV:     req.ExpectedV,
		ObservedV:     -1,
		ReservationID: req.ReservationID,
		NowNanos:      now,
	}

	s, ok := m.scopes[req.Scope]
	if !ok {
		// 未知作用域无法匹配任何已发布基线：报基线冲突，且不隐式
		// 创建作用域，保证拒绝路径零副作用。
		d.Reason = ReasonBaselineConflict
		d.Snapshot = CounterSnapshot{Seq: -1, Remaining: -1}
		m.appendReserveAuditLocked(d)
		return d, 0
	}

	d.ObservedV = s.committed

	// 顺序 1：基线冲突优先。进行中预留不推进版本，只有成功 Commit
	// 才使作用域逻辑时钟（基线版本）+1。
	if req.ExpectedV != s.committed {
		d.Reason = ReasonBaselineConflict
		d.Snapshot = s.snapshot(now, "")
		m.appendReserveAuditLocked(d)
		return d, 0
	}

	snap := s.snapshot(now, "")

	// 顺序 2：已确认关联达到上限。
	if s.committed >= int64(s.limit) {
		d.Reason = ReasonCommittedFull
		d.Snapshot = snap
		m.appendReserveAuditLocked(d)
		return d, 0
	}

	// 顺序 3：剩余名额被进行中请求占用（暂时性拒绝）。
	if snap.Remaining <= 0 {
		d.Reason = ReasonInflightOccupied
		d.Snapshot = snap
		m.appendReserveAuditLocked(d)
		return d, 0
	}

	if _, exists := m.byID[req.ReservationID]; exists {
		panic(ErrDuplicateReservation)
	}

	ttl := req.LeaseTTL
	if ttl <= 0 {
		ttl = m.defaultTTL
	}
	deadline := now + ttl.Nanoseconds()
	r := &reservation{id: req.ReservationID, scope: req.Scope, deadline: deadline}
	s.inflight[req.ReservationID] = r
	m.byID[req.ReservationID] = r

	d.Accepted = true
	d.Reason = ReasonNone
	// 证据快照包含本预留，呈现占位后的一致世界（Remaining 已扣减
	// 本名额），与审计事件中的 inflight 计数一致；"判定时确有名额"
	// 由 Accepted=true 且 Remaining>=0 直接证明，可逐步重放。
	d.Snapshot = s.snapshot(now, "")
	m.appendReserveAuditLocked(d)
	return d, deadline
}

// Commit 将预留确定为成功：进行中名额原子地转为已确认关联，
// 作用域基线版本 +1。预留不存在、已终结或已过期返回 false，
// 且不产生任何关联变化（过期预留按"中断"被回收并记录）。
func (m *Manager) Commit(reservationID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.NowNanos()
	m.sweepExpiredLocked(now)

	r := m.byID[reservationID]
	if r == nil {
		return false
	}
	if r.deadline <= now {
		m.removeReservationLocked(r)
		m.recordFinalizationLocked(EventExpire, r, now)
		return false
	}
	s := m.scopes[r.scope]
	s.committed++
	m.removeReservationLocked(r)
	m.recordFinalizationLocked(EventCommit, r, now)
	return true
}

// Abort 将预留确定为失败：原子释放其占用的名额，释放对锁释放后
// 新到达的请求立即可见。此前被拒的请求不会被自动唤醒重试。
// 预留不存在、已终结或已过期返回 false（幂等无副作用）。
func (m *Manager) Abort(reservationID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.NowNanos()
	m.sweepExpiredLocked(now)

	r := m.byID[reservationID]
	if r == nil {
		return false
	}
	if r.deadline <= now {
		m.removeReservationLocked(r)
		m.recordFinalizationLocked(EventExpire, r, now)
		return false
	}
	m.removeReservationLocked(r)
	m.recordFinalizationLocked(EventAbort, r, now)
	return true
}

// Heartbeat 为仍在进行且尚未过期的预留续租。
// 过期或不存在的预留不能"复活"，返回 false。
func (m *Manager) Heartbeat(reservationID string, ttl time.Duration) bool {
	if ttl <= 0 {
		ttl = m.defaultTTL
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock.NowNanos()
	m.sweepExpiredLocked(now)

	r := m.byID[reservationID]
	if r == nil || r.deadline <= now {
		return false
	}
	r.deadline = now + ttl.Nanoseconds()
	return true
}

// Sweep 显式回收当前已过租约的悬置预留。租约截止是"调用方中断"
// 情形唯一的名额释放裁定依据：时钟读数越过截止时刻即可回收，
// 既不会在确证中断前提前释放，也不会让名额被永久占用。
// Reserver/Commit/Abort/Heartbeat 也会惰性触发完全相同的回收。
func (m *Manager) Sweep() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sweepExpiredLocked(m.clock.NowNanos())
}

// Stats 是管理器内部计数统计，仅用于测试与运维核查；
// 它不属于约束对外状态，不影响任何判定。
type Stats struct {
	Scopes      int
	Inflight    int
	AuditEvents int
}

// Stats 返回当前统计快照。
func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepExpiredLocked(m.clock.NowNanos())
	return Stats{
		Scopes:      len(m.scopes),
		Inflight:    len(m.byID),
		AuditEvents: len(m.audit),
	}
}

// Committed 返回某作用域当前已确认关联数（未知作用域返回 0, false）。
func (m *Manager) Committed(scope ScopeKey) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.scopes[scope]
	if !ok {
		return 0, false
	}
	return s.committed, true
}

// AuditLog 返回判定以来的完整审计记录副本，供重放核验。
// 每条记录都包含到达时刻、事件后的 committed/inflight 计数、
// 判定依据（含完整计数快照）与最终结果。
func (m *Manager) AuditLog() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.audit))
	copy(out, m.audit)
	return out
}

// sweepExpiredLocked 回收全部 deadline <= now 的预留并追加审计事件。
// 遍历规模只与"当前仍登记的进行中预留数"相关，与历史请求总数无关。
func (m *Manager) sweepExpiredLocked(now int64) []string {
	var expired []string
	for id, r := range m.byID {
		if r.deadline <= now {
			expired = append(expired, id)
		}
	}
	for _, id := range expired {
		r := m.byID[id]
		m.removeReservationLocked(r)
		m.recordFinalizationLocked(EventExpire, r, now)
	}
	return expired
}

func (m *Manager) removeReservationLocked(r *reservation) {
	delete(m.byID, r.id)
	if s, ok := m.scopes[r.scope]; ok {
		delete(s.inflight, r.id)
	}
}

func (m *Manager) appendReserveAuditLocked(d Decision) {
	d.Seq = int64(len(m.audit))
	e := Event{
		Kind:          EventReserve,
		Seq:           d.Seq,
		Scope:         d.Scope,
		ReservationID: d.ReservationID,
		Decision:      &d,
		NowNanos:      d.NowNanos,
	}
	if s, ok := m.scopes[d.Scope]; ok {
		e.Committed = s.committed
		e.Inflight = len(s.inflight)
	}
	m.audit = append(m.audit, e)
}

func (m *Manager) recordFinalizationLocked(kind EventKind, r *reservation, now int64) {
	e := Event{
		Kind:          kind,
		Seq:           int64(len(m.audit)),
		Scope:         r.scope,
		ReservationID: r.id,
		NowNanos:      now,
	}
	if s, ok := m.scopes[r.scope]; ok {
		e.Committed = s.committed
		e.Inflight = len(s.inflight)
	}
	m.audit = append(m.audit, e)
}
