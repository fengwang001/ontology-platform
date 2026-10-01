// Package session provides session lifecycle management: idle timeout,
// absolute timeout, and a per-user concurrent session limit.
//
// 会话有效当且仅当当前时刻同时小于「最近活动时刻+空闲期」与「创建时刻+绝对期」，
// 恰到点即失效。所有方法均可并发调用，内部以互斥锁串行化，
// 相同的操作与时钟序列产生完全相同的状态与标识。
package session

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Status 描述会话在某个串行化观察点上可区分的状态。
type Status string

const (
	// StatusActive 会话有效。
	StatusActive Status = "active"
	// StatusLoggedOut 会话已被登出。
	StatusLoggedOut Status = "logged_out"
	// StatusEvicted 会话因并发上限被驱逐。
	StatusEvicted Status = "evicted"
	// StatusExpiredAbsolute 会话因绝对超时失效（两种超时同刻成立时优先报此状态）。
	StatusExpiredAbsolute Status = "expired_absolute"
	// StatusExpiredIdle 会话因空闲超时失效。
	StatusExpiredIdle Status = "expired_idle"
)

var (
	// ErrEmptyUser 用户名为空。
	ErrEmptyUser = errors.New("session: user must not be empty")
	// ErrSessionNotFound 会话不存在。
	ErrSessionNotFound = errors.New("session: session not found")
	// ErrNonPositiveIdle 空闲期非正。
	ErrNonPositiveIdle = errors.New("session: idle timeout must be positive")
	// ErrNonPositiveAbsolute 绝对期非正。
	ErrNonPositiveAbsolute = errors.New("session: absolute timeout must be positive")
	// ErrNonPositiveMaxPerUser 每用户并发上限非正。
	ErrNonPositiveMaxPerUser = errors.New("session: max sessions per user must be positive")
)

// StateError 表示对非有效会话的操作被拒绝，携带按
// 「已登出、被驱逐、绝对超时、空闲超时」优先级判定出的状态。
type StateError struct {
	SessionID string
	Status    Status
}

func (e *StateError) Error() string {
	return fmt.Sprintf("session: operation on %s rejected: session is %s", e.SessionID, e.Status)
}

// Info 是会话在某一串行化观察点上的快照。
type Info struct {
	ID           string
	User         string
	CreatedAt    time.Time
	LastActiveAt time.Time
	Status       Status
}

// record 是会话的内部表示。state 只记录主动终态（登出/驱逐），
// 超时状态由时钟与时刻字段推导，保证随时钟推进始终可区分。
type record struct {
	id         string
	user       string
	seq        int
	createdAt  time.Time
	lastActive time.Time
	state      Status
}

// Manager 管理会话生命周期，所有方法可并发调用。
type Manager struct {
	mu         sync.Mutex
	idle       time.Duration
	absolute   time.Duration
	maxPerUser int
	now        func() time.Time
	seq        int
	sessions   map[string]*record
	byUser     map[string]map[string]struct{}
}

// Option 自定义 Manager 行为。
type Option func(*Manager)

// WithClock 注入时钟，便于测试推进时间；默认使用 time.Now。
func WithClock(now func() time.Time) Option {
	return func(m *Manager) { m.now = now }
}

// NewManager 创建管理器。idle、absolute、maxPerUser 任一非正即整体拒绝。
func NewManager(idle, absolute time.Duration, maxPerUser int, opts ...Option) (*Manager, error) {
	if idle <= 0 {
		return nil, ErrNonPositiveIdle
	}
	if absolute <= 0 {
		return nil, ErrNonPositiveAbsolute
	}
	if maxPerUser <= 0 {
		return nil, ErrNonPositiveMaxPerUser
	}
	m := &Manager{
		idle:       idle,
		absolute:   absolute,
		maxPerUser: maxPerUser,
		now:        time.Now,
		sessions:   make(map[string]*record),
		byUser:     make(map[string]map[string]struct{}),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m, nil
}

// statusOf 在观察时刻 now 判定会话状态。
// 优先级：已登出 > 被驱逐 > 绝对超时 > 空闲超时 > 有效。
// 恰到点（now 等于截止时刻）即失效；两种超时同刻成立时报绝对超时。
func (m *Manager) statusOf(r *record, now time.Time) Status {
	if r.state != StatusActive {
		return r.state
	}
	if !now.Before(r.createdAt.Add(m.absolute)) {
		return StatusExpiredAbsolute
	}
	if !now.Before(r.lastActive.Add(m.idle)) {
		return StatusExpiredIdle
	}
	return StatusActive
}

// evictionLess 报告 a 是否比 b 更应被驱逐：
// 最近活动最早者优先，并列取创建更早，再并列取标识序号小。
func evictionLess(a, b *record) bool {
	if !a.lastActive.Equal(b.lastActive) {
		return a.lastActive.Before(b.lastActive)
	}
	if !a.createdAt.Equal(b.createdAt) {
		return a.createdAt.Before(b.createdAt)
	}
	return a.seq < b.seq
}

// Create 为用户创建会话，返回按全局顺序生成的标识（s1、s2……）。
// 创建时先排除已失效会话（不占名额也不被驱逐）；若该用户有效会话数
// 已达上限，驱逐其中最近活动最早者，其状态为被驱逐而非超时。
func (m *Manager) Create(user string) (string, error) {
	if user == "" {
		return "", ErrEmptyUser
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()

	var valid []*record
	for id := range m.byUser[user] {
		r := m.sessions[id]
		if m.statusOf(r, now) == StatusActive {
			valid = append(valid, r)
		}
	}
	if len(valid) >= m.maxPerUser {
		victim := valid[0]
		for _, r := range valid[1:] {
			if evictionLess(r, victim) {
				victim = r
			}
		}
		victim.state = StatusEvicted
	}

	m.seq++
	id := fmt.Sprintf("s%d", m.seq)
	m.sessions[id] = &record{
		id:         id,
		user:       user,
		seq:        m.seq,
		createdAt:  now,
		lastActive: now,
		state:      StatusActive,
	}
	if m.byUser[user] == nil {
		m.byUser[user] = make(map[string]struct{})
	}
	m.byUser[user][id] = struct{}{}
	return id, nil
}

// Activity 上报会话活动：仅对有效会话把最近活动时刻置为当前时刻
// （并发活动取各次时刻的最大值），不延长绝对期。
// 对非有效会话整体拒绝并返回 *StateError，不改变任何状态。
func (m *Manager) Activity(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	now := m.now()
	if st := m.statusOf(r, now); st != StatusActive {
		return &StateError{SessionID: id, Status: st}
	}
	if now.After(r.lastActive) {
		r.lastActive = now
	}
	return nil
}

// Logout 登出单个会话：仅有效会话置为已登出，其余状态保持不变。
func (m *Manager) Logout(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	if m.statusOf(r, m.now()) == StatusActive {
		r.state = StatusLoggedOut
	}
	return nil
}

// LogoutAll 登出某用户全部有效会话；已失效或已被驱逐的保持原状态。
func (m *Manager) LogoutAll(user string) error {
	if user == "" {
		return ErrEmptyUser
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for id := range m.byUser[user] {
		r := m.sessions[id]
		if m.statusOf(r, now) == StatusActive {
			r.state = StatusLoggedOut
		}
	}
	return nil
}

// Query 查询会话快照；非有效会话按状态优先级报告其状态。
func (m *Manager) Query(id string) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.sessions[id]
	if !ok {
		return Info{}, ErrSessionNotFound
	}
	return Info{
		ID:           r.id,
		User:         r.user,
		CreatedAt:    r.createdAt,
		LastActiveAt: r.lastActive,
		Status:       m.statusOf(r, m.now()),
	}, nil
}
