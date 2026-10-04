// Package conn 实现设备连接准入（Connect）与会话结束（Disconnect）。
//
// 准入按次序只报第一个错误：ErrUnknown > ErrMismatch > ErrRevoked >
// ErrRetired > ErrLapsed > ErrNotYet > ErrExpired。Pending 证书首次通过
// 准入时触发用后确认：自身成为 Active，原 Active 成为 Retiring
// （retireAt = min(now+G, 原 Active 的 na)），此前若还有 Retiring 则立即
// 成为 Retired 并踢掉使用它的会话。
package conn

import (
	"ontology/cert"
)

// Service 在共享存储上执行连接准入与会话管理。
type Service struct {
	st *cert.Store
}

// New 构造连接服务。
func New(st *cert.Store) *Service { return &Service{st: st} }

// Connect 为设备建立会话，返回本次结算与本操作造成的 Kicked 清单。
func (s *Service) Connect(dev, serial string, now int64) ([]string, error) {
	if dev == "" || serial == "" || now < 0 || now > cert.MaxTime {
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
	if c.Dev != dev {
		return nil, op.Abort(cert.ErrMismatch)
	}
	switch c.State {
	case cert.Revoked:
		return nil, op.Abort(cert.ErrRevoked)
	case cert.Retired:
		return nil, op.Abort(cert.ErrRetired)
	case cert.Lapsed:
		return nil, op.Abort(cert.ErrLapsed)
	}
	if now < c.Nb {
		return nil, op.Abort(cert.ErrNotYet)
	}
	if now >= c.Na {
		return nil, op.Abort(cert.ErrExpired)
	}
	if c.State == cert.Pending {
		s.confirm(op, c, now)
	}
	op.SetSession(dev, serial)
	return op.Commit(), nil
}

// confirm 执行用后确认：Pending 上位 Active，原 Active 转 Retiring，
// 此前的 Retiring 立即 Retired 并踢线。
func (s *Service) confirm(op *cert.Op, c *cert.Cert, now int64) {
	oldActive := op.Slot(c.Dev, cert.Active)
	oldRetiring := op.Slot(c.Dev, cert.Retiring)
	op.Transition(c, cert.Active, 0)
	if oldActive != nil {
		op.Transition(oldActive, cert.Retiring, min(now+s.st.G(), oldActive.Na))
	}
	if oldRetiring != nil {
		op.Transition(oldRetiring, cert.Retired, 0)
		op.KickIfUsed(oldRetiring)
	}
}

// Disconnect 结束设备会话，返回本次结算的 Kicked 清单。
func (s *Service) Disconnect(dev string, now int64) ([]string, error) {
	if dev == "" || now < 0 || now > cert.MaxTime {
		return nil, cert.ErrInvalid
	}
	op, err := s.st.BeginOp(now)
	if err != nil {
		return nil, err
	}
	if _, ok := op.Session(dev); !ok {
		return nil, op.Abort(cert.ErrNoSession)
	}
	op.EndSession(dev)
	return op.Commit(), nil
}
