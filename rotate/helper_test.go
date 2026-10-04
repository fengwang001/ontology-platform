package rotate

import "ontology/cert"

// 包内白盒测试不能导入 conn（测试期导入环）。
// sessMgr 用与 conn.Manager 完全相同的钩子语义在测试中扮演会话表。

type sessMgr struct {
	s        *Service
	sessions map[string]string
}

func newSessMgr(s *Service) *sessMgr {
	m := &sessMgr{s: s, sessions: map[string]string{}}
	s.SetHooks(m.onKick, m.snapshot, m.restore)
	return m
}

func (m *sessMgr) onKick(dev, serial string) string {
	if cur, ok := m.sessions[dev]; ok && cur == serial {
		delete(m.sessions, dev)
		return dev
	}
	return ""
}

func (m *sessMgr) snapshot() map[string]string {
	out := make(map[string]string, len(m.sessions))
	for k, v := range m.sessions {
		out[k] = v
	}
	return out
}

func (m *sessMgr) restore(snap map[string]string) {
	m.sessions = snap
}

func (m *sessMgr) SessionSerial(dev string) (string, bool) {
	ser, ok := m.sessions[dev]
	return ser, ok
}

func (m *sessMgr) Connect(dev, serial string, now int64) ([]string, error) {
	if dev == "" || serial == "" || !cert.ValidTime(now) {
		return nil, cert.ErrInvalid
	}
	tx, err := m.s.Enter(now)
	if err != nil {
		return nil, err
	}
	c, st, err := tx.Lookup(serial)
	if err != nil {
		tx.Abort()
		return nil, ErrUnknown
	}
	if err := testAdmit(dev, now, c, st); err != nil {
		tx.Abort()
		return nil, err
	}
	tx.DoConfirm(serial)
	m.sessions[dev] = serial
	return tx.Commit(), nil
}

func (m *sessMgr) Disconnect(dev string, now int64) ([]string, error) {
	if dev == "" || !cert.ValidTime(now) {
		return nil, cert.ErrInvalid
	}
	tx, err := m.s.Enter(now)
	if err != nil {
		return nil, err
	}
	if _, ok := m.sessions[dev]; !ok {
		tx.Abort()
		return nil, cert.ErrNoSession
	}
	delete(m.sessions, dev)
	return tx.Commit(), nil
}

func testAdmit(dev string, now int64, c cert.Cert, st cert.State) error {
	if c.Dev != dev {
		return cert.ErrMismatch
	}
	switch st {
	case cert.Revoked:
		return cert.ErrRevoked
	case cert.Retired:
		return cert.ErrRetired
	case cert.Lapsed:
		return cert.ErrLapsed
	}
	if now < c.Nb {
		return cert.ErrNotYet
	}
	if now >= c.Na {
		return cert.ErrExpired
	}
	return nil
}
