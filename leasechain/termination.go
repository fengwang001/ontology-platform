package leasechain

// Terminate 主动终止一份租约（承租人或该链房东可发起），触发级联。
func (s *Service) Terminate(now int, leaseID int64, caller string) error {
	if leaseID <= 0 || caller == "" {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txLocked(func(tx *Service) error {
		if err := tx.checkClockLocked(now); err != nil {
			return err
		}
		l := tx.leases[leaseID]
		if l == nil || l.Terminated {
			return ErrLeaseUnavailable
		}
		if caller != l.TenantID && caller != l.LandlordID {
			return wrapErr(ErrIllegalState, "仅承租人或房东可终止")
		}
		tx.acceptClockLocked(now)
		tx.settleLocked(now)
		l = tx.leases[leaseID]
		if l == nil || l.Terminated {
			return ErrLeaseUnavailable
		}
		tx.cascadeLocked(l, now)
		return nil
	})
}

// Exit 承租人退出：存在有效且未独立承认的下级时拒绝；
// 退出本身不消灭任何欠费责任与追偿权。
func (s *Service) Exit(now int, leaseID int64, tenant string) error {
	if leaseID <= 0 || tenant == "" {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txLocked(func(tx *Service) error {
		if err := tx.checkClockLocked(now); err != nil {
			return err
		}
		l := tx.leases[leaseID]
		if l == nil || l.Terminated {
			return ErrLeaseUnavailable
		}
		if l.TenantID != tenant {
			return wrapErr(ErrIllegalState, "仅承租人本人可退出")
		}
		if c := tx.childLocked(l.ID); c != nil && !c.Recognized {
			return wrapErr(ErrIllegalState, "存在未获独立承认的有效下级")
		}
		tx.acceptClockLocked(now)
		tx.settleLocked(now)
		l = tx.leases[leaseID]
		if l == nil || l.Terminated {
			return ErrLeaseUnavailable
		}
		if c := tx.childLocked(l.ID); c != nil && !c.Recognized {
			return wrapErr(ErrIllegalState, "存在未获独立承认的有效下级")
		}
		tx.cascadeLocked(l, now)
		return nil
	})
}
