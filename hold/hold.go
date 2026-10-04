// Package hold 实现法律保留：保留期间主体的新擦除单进入 Deferred
// （无时限），解除保留时 Deferred 单转 Active 且时限自解除起算；
// 已 Active 的擦除单不受保留影响。
package hold

import "ontology/erase"

// Service 为法律保留操作入口，所有调用经 Ledger 的锁串行化。
type Service struct {
	l *erase.Ledger
}

// New 在既有账本上构造保留服务。
func New(l *erase.Ledger) *Service { return &Service{l: l} }

// Hold 置主体法律保留（角色：法务）；已保留则 ErrAlready。
func (s *Service) Hold(role int, subject int64, now int64) error {
	if err := erase.CheckSubjectNow(subject, now); err != nil {
		return err
	}
	return s.l.Tx(now, role, erase.RoleLegal, nil, func(c *erase.Core) error {
		return c.HoldSubject(subject)
	})
}

// Release 解除主体法律保留（角色：法务）；未保留则 ErrNotHeld，
// 该主体的 Deferred 擦除单变 Active，deadline=now+T。
func (s *Service) Release(role int, subject int64, now int64) error {
	if err := erase.CheckSubjectNow(subject, now); err != nil {
		return err
	}
	return s.l.Tx(now, role, erase.RoleLegal, nil, func(c *erase.Core) error {
		return c.ReleaseSubject(subject, now)
	})
}
