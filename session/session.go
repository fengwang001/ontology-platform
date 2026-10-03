// Package session 维护协商会话表与重协商。
package session

import (
	"sync"

	"ontology/caps"
	"ontology/negotiate"
)

type sess struct {
	clientHello caps.Hello
	cr          int
	version     int
	enabled     uint32
	using       uint32
}

// Manager 是并发安全的协商器。
type Manager struct {
	mu     sync.Mutex
	table  *caps.Table
	server caps.Hello
	next   uint64
	live   map[uint64]*sess
}

// NewManager 以特性表创建协商器，服务端初始为空声明。
func NewManager(table *caps.Table) (*Manager, *caps.Error) {
	if table == nil {
		return nil, caps.ErrInvalid
	}
	return &Manager{
		table:  table,
		server: caps.Hello{Lo: 1, Hi: 1},
		next:   1,
		live:   make(map[uint64]*sess),
	}, nil
}

// SetServer 设置服务端当前声明；role 必须为 2。
// 权限不足或参数非法时不改任何状态。
func (m *Manager) SetServer(role int, h caps.Hello) *caps.Error {
	if role != 2 {
		return caps.ErrForbidden
	}
	if !caps.ValidHello(h) {
		return caps.ErrInvalid
	}
	m.mu.Lock()
	m.server = h
	m.mu.Unlock()
	return nil
}

// Negotiate 用客户端声明发起一次新协商，成功则建会话。
func (m *Manager) Negotiate(h caps.Hello, cr int) (uint64, negotiate.Result, *caps.Error) {
	if !caps.ValidHello(h) || !caps.ValidRole(cr) {
		return 0, negotiate.Result{}, caps.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	res, e := negotiate.Negotiate(m.table, h, cr, m.server, 0)
	if e != nil {
		return 0, negotiate.Result{}, e // 拒绝不建会话、不推进 sid
	}
	sid := m.next
	m.next++
	m.live[sid] = &sess{
		clientHello: h,
		cr:          cr,
		version:     res.Version,
		enabled:     res.Enabled,
	}
	return sid, res, nil
}

// Use 将会话启用集中的特性 f 标记为使用中。
func (m *Manager) Use(sid uint64, f int) *caps.Error {
	if f < 0 || f >= caps.NumFeatures {
		return caps.ErrFeature(caps.CodeInvalid, f)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.live[sid]
	if !ok {
		return caps.ErrNoSession
	}
	b := uint32(1) << uint(f)
	if s.enabled&b == 0 {
		return caps.ErrFeature(caps.CodeNotEnabled, f)
	}
	s.using |= b
	return nil
}

// Close 关闭会话。
func (m *Manager) Close(sid uint64) *caps.Error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.live[sid]; !ok {
		return caps.ErrNoSession
	}
	delete(m.live, sid)
	return nil
}

// Renegotiate 用当前服务端声明重做协商，U 并入必需集。
func (m *Manager) Renegotiate(sid uint64) (negotiate.Result, *caps.Error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.live[sid]
	if !ok {
		return negotiate.Result{}, caps.ErrNoSession
	}
	res, e := negotiate.Negotiate(m.table, s.clientHello, s.cr, m.server, s.using)
	if e != nil {
		return negotiate.Result{}, e // 失败保持原版本/E/U
	}
	s.version = res.Version
	s.enabled = res.Enabled
	// U 必然包含于新 E（前置窗口由 U 参与聚合保证），故无需改动。
	return res, nil
}

// Info 返回会话版本、启用集 E 与使用中集 U。
func (m *Manager) Info(sid uint64) (version int, enabled, using uint32, err *caps.Error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.live[sid]
	if !ok {
		return 0, 0, 0, caps.ErrNoSession
	}
	return s.version, s.enabled, s.using, nil
}
