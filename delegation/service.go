package delegation

import (
	"sync"
	"time"
)

// baseEvent 是一次原始权限变更事件。所有事件仅追加、不修改，
// 使得任意历史时刻的原始权限都可以被精确重建，
// 历史访问判定因此不会被后续变更追溯改变。
type baseEvent struct {
	at        time.Time
	principal string
	grant     bool // true=授予(并集) false=收缩(差集)
	perms     PermissionSet
}

// Service 是角色委托链模块的核心服务。
//
// 并发语义：所有公开方法由同一把互斥锁保护，任意并发调用的结果
// 都等价于按锁获得顺序串行执行的结果（可线性化）。
type Service struct {
	mu    sync.Mutex
	clock Clock
	log   Logger

	// baseHistory 按主体记录原始权限变更事件流（按发生顺序追加）。
	baseHistory map[string][]baseEvent

	// delegations 保存全部委托记录（仅追加，撤销以 RevokedAt 记录）。
	delegations map[DelegationID]*Delegation
	// byDelegatee 是按受托方建立的索引，保证访问判定的遍历开销
	// 只与实际支持该主体的委托路径相关，与记录总数无关。
	byDelegatee map[string][]*Delegation
	// byDelegator 是按委托方建立的索引，用于成环检测的反向遍历。
	byDelegator map[string][]*Delegation

	nextID DelegationID
}

// Option 是 Service 的可选配置。
type Option func(*Service)

// WithClock 注入时间来源（默认系统时钟）。
func WithClock(c Clock) Option {
	return func(s *Service) { s.clock = c }
}

// WithLogger 注入审计日志器（默认丢弃）。
func WithLogger(l Logger) Option {
	return func(s *Service) { s.log = l }
}

// NewService 创建委托链服务。
func NewService(opts ...Option) *Service {
	s := &Service{
		clock:       SystemClock{},
		log:         LoggerFunc(func(LogEntry) {}),
		baseHistory: make(map[string][]baseEvent),
		delegations: make(map[DelegationID]*Delegation),
		byDelegatee: make(map[string][]*Delegation),
		byDelegator: make(map[string][]*Delegation),
		nextID:      1,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// GrantBase 授予主体一批原始权限（与已有权限取并集）。
func (s *Service) GrantBase(principal string, perms PermissionSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	s.baseHistory[principal] = append(s.baseHistory[principal], baseEvent{
		at:        now,
		principal: principal,
		grant:     true,
		perms:     perms.Clone(),
	})
	s.log.Log(LogEntry{
		Time:  now,
		Op:    "GrantBase",
		Input: map[string]any{"principal": principal, "perms": perms},
	})
}

// ShrinkBase 收缩主体的原始权限（从已有权限中扣除给定部分）。
// 收缩会通过访问判定的实时求值立即级联影响所有下游委托。
func (s *Service) ShrinkBase(principal string, perms PermissionSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	s.baseHistory[principal] = append(s.baseHistory[principal], baseEvent{
		at:        now,
		principal: principal,
		grant:     false,
		perms:     perms.Clone(),
	})
	s.log.Log(LogEntry{
		Time:  now,
		Op:    "ShrinkBase",
		Input: map[string]any{"principal": principal, "perms": perms},
	})
}

// baseAt 重建主体在时刻 t 的原始权限（按事件顺序重放到 t 为止）。
// 调用方必须持有锁。
func (s *Service) baseAt(principal string, t time.Time) PermissionSet {
	out := PermissionSet{}
	for _, ev := range s.baseHistory[principal] {
		if ev.at.After(t) {
			break
		}
		if ev.grant {
			out = out.Union(ev.perms)
		} else {
			out = out.Subtract(ev.perms)
		}
	}
	return out
}

// activeAt 报告委托记录在时刻 t 是否处于生效状态：
// 已创建、未在 t 之前被撤销、且 t 落在有效期 [Start, End) 内。
// 调用方必须持有锁。
func activeAt(d *Delegation, t time.Time) bool {
	if d.CreatedAt.After(t) {
		return false
	}
	if d.RevokedAt != nil && !d.RevokedAt.After(t) {
		return false
	}
	return !t.Before(d.Start) && t.Before(d.End)
}
