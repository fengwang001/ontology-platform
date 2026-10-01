// Package session 实现会话生命周期管理：空闲超时、绝对超时与每用户并发会话上限。
//
// 会话有效当且仅当当前时刻严格小于「最近活动时刻+空闲期」且严格小于
// 「创建时刻+绝对期」，恰到点即失效。活动只推进最近活动时刻，不延长绝对期。
// 对非有效会话报告状态时优先级为：已登出 > 被驱逐 > 绝对超时 > 空闲超时。
package session

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Status 表示会话在某一时刻的状态。
type Status int

const (
	// StatusActive 会话有效。
	StatusActive Status = iota
	// StatusLoggedOut 会话已登出。
	StatusLoggedOut
	// StatusEvicted 会话因并发上限被驱逐。
	StatusEvicted
	// StatusExpiredAbsolute 会话因绝对超时失效（两种超时同刻成立时报此状态）。
	StatusExpiredAbsolute
	// StatusExpiredIdle 会话因空闲超时失效。
	StatusExpiredIdle
)

func (s Status) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusLoggedOut:
		return "logged_out"
	case StatusEvicted:
		return "evicted"
	case StatusExpiredAbsolute:
		return "expired_absolute"
	case StatusExpiredIdle:
		return "expired_idle"
	default:
		return "unknown"
	}
}

// ErrEmptyUser 表示用户名为空，操作被整体拒绝。
var ErrEmptyUser = errors.New("session: user must not be empty")

// ErrSessionNotFound 表示会话不存在，操作被整体拒绝。
var ErrSessionNotFound = errors.New("session: session not found")

// ParamError 表示构造参数非正（空闲期、绝对期或并发上限），创建管理器被拒绝。
type ParamError struct {
	Field string
}

func (e *ParamError) Error() string {
	return fmt.Sprintf("session: parameter %q must be positive", e.Field)
}

// StateError 表示对非有效会话执行活动，携带该会话的当前状态以便区分原因。
type StateError struct {
	SessionID string
	Status    Status
}

func (e *StateError) Error() string {
	return fmt.Sprintf("session: cannot touch session %s in state %s", e.SessionID, e.Status)
}

// Info 是会话在某一时刻的只读快照。
type Info struct {
	ID         string
	User       string
	CreatedAt  time.Time
	LastActive time.Time
	Status     Status
}

// session 是内部会话记录，terminal 记录终态标记（登出/驱逐），
// 超时状态不落盘，由时钟与参数在查询时计算，保证状态始终可区分。
type session struct {
	id         string
	user       string
	seq        int
	createdAt  time.Time
	lastActive time.Time
	terminal   Status // StatusActive、StatusLoggedOut 或 StatusEvicted
}

func (s *session) statusAt(now time.Time, idle, absolute time.Duration) Status {
	if s.terminal == StatusLoggedOut {
		return StatusLoggedOut
	}
	if s.terminal == StatusEvicted {
		return StatusEvicted
	}
	if !now.Before(s.createdAt.Add(absolute)) {
		return StatusExpiredAbsolute
	}
	if !now.Before(s.lastActive.Add(idle)) {
		return StatusExpiredIdle
	}
	return StatusActive
}

// Manager 管理会话生命周期，所有方法可并发调用；
// 内部以互斥锁串行化，任意串行化点上每用户有效会话数不超过上限。
type Manager struct {
	mu       sync.Mutex
	idle     time.Duration
	absolute time.Duration
	limit    int
	seq      int
	sessions map[string]*session
}

// NewManager 创建管理器。idle、absolute、limit 必须为正，否则返回 *ParamError。
func NewManager(idle, absolute time.Duration, limit int) (*Manager, error) {
	if idle <= 0 {
		return nil, &ParamError{Field: "idle"}
	}
	if absolute <= 0 {
		return nil, &ParamError{Field: "absolute"}
	}
	if limit <= 0 {
		return nil, &ParamError{Field: "limit"}
	}
	return &Manager{
		idle:     idle,
		absolute: absolute,
		limit:    limit,
		sessions: make(map[string]*session),
	}, nil
}

// Create 为用户 user 在时刻 now 创建会话，返回按全局顺序生成的标识（s1、s2……）。
// 创建时先排除已失效会话；若该用户有效会话数已达上限，驱逐其中最近活动最早者
// （并列取创建更早，再并列取标识序号小），被驱逐者状态为 StatusEvicted。
func (m *Manager) Create(user string, now time.Time) (string, error) {
	if user == "" {
		return "", ErrEmptyUser
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var active []*session
	for _, s := range m.sessions {
		if s.user == user && s.statusAt(now, m.idle, m.absolute) == StatusActive {
			active = append(active, s)
		}
	}
	if len(active) >= m.limit {
		oldest := active[0]
		for _, s := range active[1:] {
			if evictionLess(s, oldest) {
				oldest = s
			}
		}
		oldest.terminal = StatusEvicted
	}

	m.seq++
	id := fmt.Sprintf("s%d", m.seq)
	m.sessions[id] = &session{
		id:         id,
		user:       user,
		seq:        m.seq,
		createdAt:  now,
		lastActive: now,
	}
	return id, nil
}

// evictionLess 报告 a 是否应比 b 更先被驱逐：
// 最近活动更早者优先，并列取创建更早者，再并列取标识序号小者。
func evictionLess(a, b *session) bool {
	if !a.lastActive.Equal(b.lastActive) {
		return a.lastActive.Before(b.lastActive)
	}
	if !a.createdAt.Equal(b.createdAt) {
		return a.createdAt.Before(b.createdAt)
	}
	return a.seq < b.seq
}

// Activity 在时刻 now 对会话 id 记录活动。会话必须存在且有效，
// 否则返回 ErrSessionNotFound 或 *StateError，且不改变任何状态。
// 并发活动以各次时刻的最大值推进最近活动时刻。
func (m *Manager) Activity(id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	if st := s.statusAt(now, m.idle, m.absolute); st != StatusActive {
		return &StateError{SessionID: id, Status: st}
	}
	if now.After(s.lastActive) {
		s.lastActive = now
	}
	return nil
}

// Logout 在时刻 now 登出会话 id。会话不存在时返回 ErrSessionNotFound；
// 仅当会话当前有效时置为已登出，已失效或已被驱逐的会话保持原状态。
func (m *Manager) Logout(id string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	if s.statusAt(now, m.idle, m.absolute) == StatusActive {
		s.terminal = StatusLoggedOut
	}
	return nil
}

// LogoutAll 在时刻 now 登出用户 user 的全部有效会话；
// 已失效或已被驱逐的会话保持原状态。用户为空时返回 ErrEmptyUser。
func (m *Manager) LogoutAll(user string, now time.Time) error {
	if user == "" {
		return ErrEmptyUser
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.sessions {
		if s.user == user && s.statusAt(now, m.idle, m.absolute) == StatusActive {
			s.terminal = StatusLoggedOut
		}
	}
	return nil
}

// Status 返回会话 id 在时刻 now 的状态；会话不存在时返回 ErrSessionNotFound。
// 非有效会话按「已登出、被驱逐、绝对超时、空闲超时」的优先级报告。
func (m *Manager) Status(id string, now time.Time) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return StatusActive, ErrSessionNotFound
	}
	return s.statusAt(now, m.idle, m.absolute), nil
}

// Info 返回会话 id 在时刻 now 的快照；会话不存在时返回 ErrSessionNotFound。
func (m *Manager) Info(id string, now time.Time) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return Info{}, ErrSessionNotFound
	}
	return Info{
		ID:         s.id,
		User:       s.user,
		CreatedAt:  s.createdAt,
		LastActive: s.lastActive,
		Status:     s.statusAt(now, m.idle, m.absolute),
	}, nil
}
