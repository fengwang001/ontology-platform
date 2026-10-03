// Package session 维护协商会话表、使用中特性与服务端声明的重协商。
package session

import (
	"ontology/caps"
	"ontology/negotiate"
	"sync"
)

const serverAdminRole uint8 = 2

type sess struct {
	ver     uint64
	enabled uint32
	using   uint32
	client  caps.Hello
	cr      uint8
	closed  bool
}

// Info 是会话快照：版本、启用集 E、使用中集 U。
type Info struct {
	Ver     uint64
	Enabled uint32
	Using   uint32
}

// Manager 用一把读写锁串行化全部公开操作。
type Manager struct {
	mu       sync.RWMutex
	table    *caps.Table
	server   caps.Hello
	sessions map[uint64]*sess
	nextSID  uint64
}

// NewManager 构造管理器：服务端为初始空声明 lo=hi=1、sup=req=0。
func NewManager(t *caps.Table) *Manager {
	return &Manager{
		table:    t,
		server:   caps.EmptyHello(),
		sessions: make(map[uint64]*sess),
		nextSID:  1,
	}
}

// SetServer 以管理员角色 role（必须为 2）更新当前服务端声明，只影响之后的协商。
func (m *Manager) SetServer(role uint8, h caps.Hello) error {
	if err := caps.ValidateRole(role); err != nil {
		logger.Printf("SetServer rejected: invalid role=%d", role)
		return err
	}
	if role != serverAdminRole {
		logger.Printf("SetServer rejected: role=%d < 2 (ErrForbidden)", role)
		return caps.NewError(caps.ReasonForbidden, -1, "server role must be 2")
	}
	if err := caps.ValidateHello(h); err != nil {
		logger.Printf("SetServer rejected: bad hello %+v (%v)", h, err)
		return err
	}
	m.mu.Lock()
	m.server = h
	m.mu.Unlock()
	logger.Printf("SetServer ok: hello=%+v", h)
	return nil
}

// Negotiate 用客户端声明与当前服务端声明协商，成功则新建会话，sid 从 1 连续递增。
func (m *Manager) Negotiate(client caps.Hello, cr uint8) (sid uint64, info Info, err error) {
	if e := caps.ValidateHello(client); e != nil {
		logger.Printf("Negotiate rejected: bad client hello %+v (%v)", client, e)
		return 0, Info{}, e
	}
	if e := caps.ValidateRole(cr); e != nil {
		logger.Printf("Negotiate rejected: invalid cr=%d", cr)
		return 0, Info{}, e
	}

	m.mu.Lock()
	server := m.server
	out, perr := negotiate.Pick(negotiate.Params{
		Table:  m.table,
		Client: client,
		CR:     cr,
		Server: server,
	})
	if perr != nil {
		m.mu.Unlock()
		logger.Printf("Negotiate failed client=%+v cr=%d server=%+v L=%d H=%d Q=%08x: %v",
			client, cr, server, out.L, out.H, out.Q, perr)
		return 0, Info{}, perr
	}
	sid = m.nextSID
	m.nextSID++
	m.sessions[sid] = &sess{
		ver:     out.Ver,
		enabled: out.Enabled,
		using:   0,
		client:  client,
		cr:      cr,
	}
	s := m.sessions[sid]
	info = Info{Ver: s.ver, Enabled: s.enabled, Using: s.using}
	m.mu.Unlock()

	logger.Printf("Negotiate ok sid=%d client=%+v cr=%d server=%+v L=%d H=%d lower=%d upper=%d E=%08x v=%d",
		sid, client, cr, server, out.L, out.H, out.Lower, out.Upper, out.Enabled, out.Ver)
	return sid, info, nil
}

// Use 将会话启用集中的特性 f 标记为使用中；未启用报 ErrNotEnabled。
func (m *Manager) Use(sid uint64, f uint8) error {
	if e := caps.ValidateFeature(f); e != nil {
		logger.Printf("Use sid=%d f=%d rejected: %v", sid, f, e)
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sid]
	if !ok || s.closed {
		logger.Printf("Use sid=%d f=%d rejected: ErrNoSession", sid, f)
		return caps.NewError(caps.ReasonNoSession, -1, "session not found or closed")
	}
	if s.enabled&(1<<f) == 0 {
		logger.Printf("Use sid=%d f=%d rejected: ErrNotEnabled (E=%08x)", sid, f, s.enabled)
		return caps.NewError(caps.ReasonNotEnabled, int(f), "feature not enabled in session")
	}
	s.using |= 1 << f
	logger.Printf("Use ok sid=%d f=%d U=%08x", sid, f, s.using)
	return nil
}

// Close 关闭会话；关闭后该 sid 的任何操作报 ErrNoSession。
func (m *Manager) Close(sid uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sid]
	if !ok || s.closed {
		logger.Printf("Close sid=%d rejected: ErrNoSession", sid)
		return caps.NewError(caps.ReasonNoSession, -1, "session not found or closed")
	}
	s.closed = true
	delete(m.sessions, sid)
	logger.Printf("Close ok sid=%d", sid)
	return nil
}

// Renegotiate 用保存的客户端声明与当前服务端声明重做协商，必需集并入使用中特性。
// 任一判定失败则会话保持原版本、E、U 不变。
func (m *Manager) Renegotiate(sid uint64) (Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sid]
	if !ok || s.closed {
		logger.Printf("Renegotiate sid=%d rejected: ErrNoSession", sid)
		return Info{}, caps.NewError(caps.ReasonNoSession, -1, "session not found or closed")
	}

	out, perr := negotiate.Pick(negotiate.Params{
		Table:  m.table,
		Client: s.client,
		CR:     s.cr,
		Server: m.server,
		Extra:  s.using,
	})
	if perr != nil {
		logger.Printf("Renegotiate failed sid=%d keep v=%d E=%08x U=%08x L=%d H=%d Q=%08x: %v",
			sid, s.ver, s.enabled, s.using, out.L, out.H, out.Q, perr)
		return Info{}, perr
	}

	oldVer, oldE := s.ver, s.enabled
	s.ver = out.Ver
	s.enabled = out.Enabled
	logger.Printf("Renegotiate ok sid=%d v=%d->%d E=%08x->%08x U=%08x (pinned Q=%08x)",
		sid, oldVer, s.ver, oldE, s.enabled, s.using, out.Q)
	return Info{Ver: s.ver, Enabled: s.enabled, Using: s.using}, nil
}

// Info 返回会话版本、启用集 E 与使用中集 U。
func (m *Manager) Info(sid uint64) (Info, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[sid]
	if !ok || s.closed {
		return Info{}, caps.NewError(caps.ReasonNoSession, -1, "session not found or closed")
	}
	return Info{Ver: s.ver, Enabled: s.enabled, Using: s.using}, nil
}
