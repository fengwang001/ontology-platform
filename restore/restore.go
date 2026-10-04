// Package restore 实现备份点与恢复重放：Restore 以「ack(e,s) 存在且
// 大于 tb」为条件快照重放集 L，L 非空则系统进入 Restoring 并阻塞读，
// 由 ReapplyDone 逐项销账，清空后回到 Ready。
package restore

import "ontology/erase"

// Service 为备份/恢复操作入口，所有调用经 Ledger 的锁串行化。
type Service struct {
	l *erase.Ledger
}

// New 在既有账本上构造恢复服务。
func New(l *erase.Ledger) *Service { return &Service{l: l} }

// Backup 为系统 s 新建备份点（角色：系统运维），返回全局递增编号。
func (s *Service) Backup(role, sys int, now int64) (int, error) {
	if sys < 1 || sys > s.l.SysCount() || !erase.ValidNow(now) {
		return 0, erase.ErrParam
	}
	var id int
	err := s.l.Tx(now, role, erase.RoleOps, nil, func(c *erase.Core) error {
		id = c.AddBackup(sys, now)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// Restore 将系统 s 恢复到备份点 b（角色：系统运维），返回重放集 L
// （按擦除单编号升序）。b 不属于 s 报 ErrNoBackup，s 非 Ready 报
// ErrState（先 ErrNoBackup 后 ErrState）。
func (s *Service) Restore(role, sys, b int, now int64) ([]int, error) {
	if sys < 1 || sys > s.l.SysCount() || !erase.ValidNow(now) {
		return nil, erase.ErrParam
	}
	var L []int
	err := s.l.Tx(now, role, erase.RoleOps,
		func(c *erase.Core) error {
			if b < 1 || b > c.NumBackups() {
				return erase.ErrParam
			}
			return nil
		},
		func(c *erase.Core) error {
			var err error
			L, err = c.RestoreBackup(sys, b)
			return err
		})
	if err != nil {
		return nil, err
	}
	return L, nil
}

// ReapplyDone 标记重放集中的一个擦除单已补做（角色：系统运维）；
// 待办清空后系统回到 Ready。
func (s *Service) ReapplyDone(role, sys, e int, now int64) error {
	if sys < 1 || sys > s.l.SysCount() || !erase.ValidNow(now) {
		return erase.ErrParam
	}
	return s.l.Tx(now, role, erase.RoleOps,
		func(c *erase.Core) error {
			if e < 1 || e > c.NumErasures() {
				return erase.ErrParam
			}
			return nil
		},
		func(c *erase.Core) error { return c.CompleteReapply(sys, e) })
}

// Read 读取系统 s；Restoring 时报 ErrRestoring（不带 now）。
func (s *Service) Read(sys int) error {
	if sys < 1 || sys > s.l.SysCount() {
		return erase.ErrParam
	}
	return s.l.View(func(c *erase.Core) error {
		if c.SystemStatus(sys) == erase.Restoring {
			return erase.ErrRestoring
		}
		return nil
	})
}
