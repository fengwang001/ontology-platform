// Package rotate 实现证书签发（Issue）与吊销（Revoke）。
//
// 签发：设备无 Active 且无 Pending 时新证直接 Active；有 Active 而无
// Pending 时新证为 Pending（须在续期窗口内，lapseAt = max(now,nb)+T）。
// 用后确认在 conn.Connect 准入通过时触发，此处不处理。
package rotate

import (
	"ontology/cert"
)

// Service 在共享存储上执行签发与吊销。
type Service struct {
	st *cert.Store
}

// New 构造签发服务。
func New(st *cert.Store) *Service { return &Service{st: st} }

// Issue 为设备签发新证书，返回本次结算的 Kicked 清单。
//
// 拒绝次序：ErrInvalid > ErrClockBack > ErrDupSerial > ErrPendingExists > ErrTooEarly。
func (s *Service) Issue(dev, serial string, nb, na, now int64) ([]string, error) {
	if dev == "" || serial == "" ||
		nb < 0 || nb >= na || na > cert.MaxTime ||
		now < 0 || now > cert.MaxTime {
		return nil, cert.ErrInvalid
	}
	op, err := s.st.BeginOp(now)
	if err != nil {
		return nil, err
	}
	if op.Cert(serial) != nil {
		return nil, op.Abort(cert.ErrDupSerial)
	}
	if op.Slot(dev, cert.Pending) != nil {
		return nil, op.Abort(cert.ErrPendingExists)
	}
	c := &cert.Cert{Serial: serial, Dev: dev, Nb: nb, Na: na}
	if active := op.Slot(dev, cert.Active); active != nil {
		if now < active.Na-s.st.W() {
			return nil, op.Abort(cert.ErrTooEarly)
		}
		c.State = cert.Pending
		c.LapseAt = max(now, nb) + s.st.T()
	} else {
		c.State = cert.Active
	}
	op.Insert(c)
	return op.Commit(), nil
}

// Revoke 吊销证书并踢掉正在使用它的会话，返回 Kicked 清单。
//
// 拒绝次序：ErrInvalid > ErrClockBack > ErrUnknown > ErrFinal。
// 吊销 Active 不影响 Pending，也不会使 Retiring 回升。
func (s *Service) Revoke(serial string, now int64) ([]string, error) {
	if serial == "" || now < 0 || now > cert.MaxTime {
		return nil, cert.ErrInvalid
	}
	op, err := s.st.BeginOp(now)
	if err != nil {
		return nil, err
	}
	c := op.Cert(serial)
	if c == nil {
		return nil, op.Abort(cert.ErrUnknown)
	}
	if c.State.Final() {
		return nil, op.Abort(cert.ErrFinal)
	}
	op.Transition(c, cert.Revoked, 0)
	op.KickIfUsed(c)
	return op.Commit(), nil
}
