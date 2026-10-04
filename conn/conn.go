package conn

import (
	"sync"

	"ontology/cert"
	"ontology/rotate"
)

var (
	ErrInvalid   = cert.ErrInvalid
	ErrClockBack = cert.ErrClockBack
	ErrUnknown   = cert.ErrUnknown
	ErrMismatch  = cert.ErrMismatch
	ErrRevoked   = cert.ErrRevoked
	ErrRetired   = cert.ErrRetired
	ErrLapsed    = cert.ErrLapsed
	ErrNotYet    = cert.ErrNotYet
	ErrExpired   = cert.ErrExpired
	ErrNoSession = cert.ErrNoSession
)

// Manager 管理每台设备至多一个在线会话，并把踢线/回滚挂到 rotate 内核。
// 会话实际由内核的锁串行保护；mmu 仅保证 map 在 SetHooks 之后安全初始化。
type Manager struct {
	svc *rotate.Service

	mmu      sync.Mutex
	sessions map[string]string // dev -> 当前会话所用 serial
}

// New 构造绑定到指定轮换内核的会话管理器并安装钩子。
func New(svc *rotate.Service) *Manager {
	m := &Manager{svc: svc, sessions: map[string]string{}}
	svc.SetHooks(m.onKick, m.snapshot, m.restore)
	return m
}

// Service 返回底层轮换内核（可直接调用 Issue/Revoke）。
func (m *Manager) Service() *rotate.Service { return m.svc }

// onKick 在证书落定 Revoked/Retired 时由内核持锁调用。
// 仅当该设备会话恰好使用此证时断开，返回设备名；接管产生的“旧会话”不受影响。
func (m *Manager) onKick(dev, serial string) string {
	if cur, ok := m.sessions[dev]; ok && cur == serial {
		delete(m.sessions, dev)
		return dev
	}
	return ""
}

// snapshot 返回会话表的独立副本。
func (m *Manager) snapshot() map[string]string {
	out := make(map[string]string, len(m.sessions))
	for k, v := range m.sessions {
		out[k] = v
	}
	return out
}

// restore 用事务前快照整体替换会话表（仅被拒事务调用）。
func (m *Manager) restore(snap map[string]string) {
	m.sessions = snap
}

// SessionSerial 返回设备当前会话所用序列号；无会话时 ok=false。
func (m *Manager) SessionSerial(dev string) (serial string, ok bool) {
	m.mmu.Lock()
	defer m.mmu.Unlock()
	serial, ok = m.sessions[dev]
	return
}

// admit 按规定次序只报第一个准入错误；通过返回 nil。
func admit(dev, serial string, now int64, c cert.Cert, st cert.State) error {
	if c.Dev != dev {
		return ErrMismatch
	}
	switch st {
	case cert.Revoked:
		return ErrRevoked
	case cert.Retired:
		return ErrRetired
	case cert.Lapsed:
		return ErrLapsed
	}
	if now < c.Nb {
		return ErrNotYet
	}
	if now >= c.Na {
		return ErrExpired
	}
	return nil
}

// Connect 按准入次序判定，通过后建立/接管该设备会话。
func (m *Manager) Connect(dev, serial string, now int64) ([]string, error) {
	if dev == "" || serial == "" || !cert.ValidTime(now) {
		return nil, ErrInvalid
	}
	tx, err := m.svc.Enter(now)
	if err != nil {
		return nil, err // ErrClockBack（非法时刻已在前置校验）
	}

	c, st, err := tx.Lookup(serial)
	if err != nil {
		tx.Abort()
		return nil, ErrUnknown
	}
	if err := admit(dev, serial, now, c, st); err != nil {
		tx.Abort()
		return nil, err
	}

	// Pending 首次通过准入 -> 对调；Active/Retiring 仅建立会话。
	tx.DoConfirm(serial)

	m.mmu.Lock()
	m.sessions[dev] = serial
	m.mmu.Unlock()

	return tx.Commit(), nil
}

// Disconnect 结束设备当前会话；无会话报 ErrNoSession。
func (m *Manager) Disconnect(dev string, now int64) ([]string, error) {
	if dev == "" || !cert.ValidTime(now) {
		return nil, ErrInvalid
	}
	tx, err := m.svc.Enter(now)
	if err != nil {
		return nil, err
	}

	m.mmu.Lock()
	if _, ok := m.sessions[dev]; !ok {
		m.mmu.Unlock()
		tx.Abort()
		return nil, ErrNoSession
	}
	delete(m.sessions, dev)
	m.mmu.Unlock()

	return tx.Commit(), nil
}
